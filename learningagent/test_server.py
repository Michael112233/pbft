import queue
import threading
import unittest
from unittest import mock

import grpc
import numpy as np

from learningagent import learning_agent_pb2
from learningagent import learning_agent_pb2_grpc
from learningagent.server import (
    LearningData,
    GRACEFUL_STOP_SECONDS,
    LearningAgentService,
    build_server,
    run_decision_worker,
    run_server,
)


class RecordingNodeStub:
    def __init__(self, response):
        self.response = response
        self.calls = []

    def SendDecision(self, request, timeout=None):
        self.calls.append((request, timeout))
        return self.response


class OutcomeNodeStub:
    def __init__(self, outcomes):
        self.outcomes = list(outcomes)
        self.calls = []

    def SendDecision(self, request, timeout=None):
        self.calls.append((request, timeout))
        outcome = self.outcomes.pop(0)
        if isinstance(outcome, BaseException):
            raise outcome
        return outcome


class TestRPCError(grpc.RpcError):
    pass


class RecordingCMAB:
    def __init__(self, predictions):
        self.predictions = iter(predictions)
        self.recorded = []
        self.trained = []

    def record_state_action_reward(self, learning_data):
        self.recorded.append(learning_data)

    def train(self, protocol):
        self.trained.append(protocol)

    def predict(self, _state):
        return next(self.predictions)


class LearningAgentServerTest(unittest.TestCase):
    def setUp(self):
        self.decision_queue = queue.Queue()
        self.server, port = build_server(
            2,
            "127.0.0.1:0",
            decision_queue=self.decision_queue,
        )
        self.server.start()
        self.channel = grpc.insecure_channel(f"127.0.0.1:{port}")
        self.stub = learning_agent_pb2_grpc.LearningAgentStub(self.channel)

    def tearDown(self):
        self.channel.close()
        self.server.stop(0).wait()

    def test_exchange_echoes_binary_payload(self):
        payload = b"\x00startup\xff"
        response = self.stub.Exchange(
            learning_agent_pb2.NodeRequest(node_id=2, payload=payload),
            timeout=1,
        )
        self.assertEqual(response.node_id, 2)
        self.assertEqual(response.payload, payload)

    def test_exchange_rejects_another_node(self):
        with self.assertRaises(grpc.RpcError) as raised:
            self.stub.Exchange(
                learning_agent_pb2.NodeRequest(node_id=1, payload=b"wrong-node"),
                timeout=1,
            )
        self.assertEqual(raised.exception.code(), grpc.StatusCode.INVALID_ARGUMENT)

    def test_send_learning_data_extracts_reward_and_ordered_state(self):
        response = self.stub.SendLearningData(
            learning_agent_pb2.LearningDecision(
                node_id=2,
                sequence_id=17,
                next_protocol="hotstuff",
                data={
                    "req_size": 128.0,
                    "reward": 4.5,
                    "proposal_interval": 0.25,
                },
            ),
            timeout=1,
        )

        self.assertTrue(response.accepted)
        self.assertEqual(response.node_id, 2)
        self.assertEqual(response.sequence_id, 17)

        task = self.decision_queue.get_nowait()
        self.assertEqual(task.sequence_id, 17)
        self.assertEqual(task.current_protocol, "hotstuff")
        self.assertEqual(task.reward, 4.5)
        self.assertEqual(task.state.tolist(), [0.25, 128.0])
        self.decision_queue.task_done()

    def test_send_learning_data_rejects_missing_map_key(self):
        with self.assertRaises(grpc.RpcError) as raised:
            self.stub.SendLearningData(
                learning_agent_pb2.LearningDecision(
                    node_id=2,
                    sequence_id=17,
                    next_protocol="hotstuff",
                    data={
                        "reward": 4.5,
                        "proposal_interval": 0.25,
                    },
                ),
                timeout=1,
            )

        self.assertEqual(raised.exception.code(), grpc.StatusCode.INVALID_ARGUMENT)
        self.assertIn("req_size", raised.exception.details())
        self.assertTrue(self.decision_queue.empty())

    def test_send_decision_uses_injected_node_stub(self):
        expected = learning_agent_pb2.DecisionAck(
            node_id=2,
            sequence_id=17,
            accepted=True,
        )
        node_stub = RecordingNodeStub(expected)
        service = LearningAgentService(2, node_stub)

        response = service.send_decision(17, "hotstuff", timeout=2.5)

        self.assertIs(response, expected)
        self.assertEqual(len(node_stub.calls), 1)
        request, timeout = node_stub.calls[0]
        self.assertEqual(request.node_id, 2)
        self.assertEqual(request.sequence_id, 17)
        self.assertEqual(request.next_protocol, "hotstuff")
        self.assertEqual(timeout, 2.5)

    def test_send_decision_requires_node_stub(self):
        service = LearningAgentService(2)

        with self.assertRaisesRegex(RuntimeError, "callback client is not configured"):
            service.send_decision(1, "pbft")

    def test_enqueue_decision_adds_task(self):
        decision_queue = queue.Queue()
        service = LearningAgentService(2, decision_queue=decision_queue)
        state = np.asarray([0.25, 128.0], dtype=np.float64)

        service.enqueue_decision(23, "hotstuff", 4.5, state)

        task = decision_queue.get_nowait()
        self.assertEqual(task.sequence_id, 23)
        self.assertEqual(task.current_protocol, "hotstuff")
        self.assertEqual(task.reward, 4.5)
        np.testing.assert_array_equal(task.state, state)
        decision_queue.task_done()

    def test_enqueue_decision_requires_worker_queue(self):
        service = LearningAgentService(2)

        with self.assertRaisesRegex(RuntimeError, "worker queue is not configured"):
            service.enqueue_decision(
                1,
                "pbft",
                1.0,
                np.asarray([0.1, 64.0], dtype=np.float64),
            )

    def test_decision_worker_processes_tasks_in_fifo_order(self):
        node_stub = RecordingNodeStub(
            learning_agent_pb2.DecisionAck(accepted=True)
        )
        decision_queue = queue.Queue()
        decision_queue.put(LearningData(1, "pbft", 0.0, np.asarray([0.1, 64.0])))
        decision_queue.put(
            LearningData(2, "hotstuff", 2.0, np.asarray([0.2, 128.0]))
        )
        decision_queue.put(None)
        cmab = RecordingCMAB(["pbft", "hotstuff"])

        worker = threading.Thread(
            target=run_decision_worker,
            args=(2, node_stub, decision_queue, cmab),
        )
        worker.start()
        decision_queue.join()
        worker.join(timeout=1)

        self.assertFalse(worker.is_alive())
        self.assertEqual(
            [
                (
                    request.node_id,
                    request.sequence_id,
                    request.next_protocol,
                    timeout,
                )
                for request, timeout in node_stub.calls
            ],
            [
                (2, 1, "pbft", 1.0),
                (2, 2, "hotstuff", 1.0),
            ],
        )

    def test_decision_worker_logs_failures_and_continues(self):
        node_stub = OutcomeNodeStub(
            [
                TestRPCError("offline"),
                learning_agent_pb2.DecisionAck(
                    accepted=False,
                    error="rejected",
                ),
                learning_agent_pb2.DecisionAck(accepted=True),
            ]
        )
        decision_queue = queue.Queue()
        decision_queue.put(LearningData(1, "pbft", 0.0, np.asarray([0.1, 64.0])))
        decision_queue.put(
            LearningData(2, "hotstuff", 2.0, np.asarray([0.2, 128.0]))
        )
        decision_queue.put(LearningData(3, "prime", 3.0, np.asarray([0.3, 256.0])))
        decision_queue.put(None)
        cmab = RecordingCMAB(["pbft", "hotstuff", "prime"])

        with self.assertLogs("learningagent.server", level="INFO") as captured:
            worker = threading.Thread(
                target=run_decision_worker,
                args=(2, node_stub, decision_queue, cmab),
            )
            worker.start()
            decision_queue.join()
            worker.join(timeout=1)

        self.assertFalse(worker.is_alive())
        self.assertEqual(
            [request.sequence_id for request, _timeout in node_stub.calls],
            [1, 2, 3],
        )
        logs = "\n".join(captured.output)
        self.assertIn("RPC failed", logs)
        self.assertIn("rejected decision sequence 2", logs)
        self.assertIn("accepted decision sequence 3", logs)

    def test_run_server_wires_node_stub_and_closes_channel(self):
        events = []
        channel = mock.Mock()
        channel.close.side_effect = lambda: events.append("close-channel")
        node_stub = mock.Mock()
        node_stub.SendDecision.side_effect = lambda *_args, **_kwargs: (
            events.append("send-decision")
            or learning_agent_pb2.DecisionAck(accepted=True)
        )
        server = mock.Mock()
        server.start.side_effect = lambda: events.append("start-server")
        stopped = mock.Mock()
        stopped.wait.side_effect = lambda: events.append("wait-server")

        def stop_server(_grace):
            events.append("stop-server")
            return stopped

        server.stop.side_effect = stop_server
        stop_event = mock.Mock()
        stop_event.wait.return_value = True

        def build_test_server(_node_id, _address, _node_stub, decision_queue):
            decision_queue.put(
                LearningData(31, "pbft", 0.0, np.asarray([0.1, 64.0]))
            )
            return server, 29002

        with (
            mock.patch(
                "learningagent.server.grpc.insecure_channel",
                return_value=channel,
            ) as insecure_channel,
            mock.patch(
                "learningagent.server.learning_agent_pb2_grpc.LearningAgentNodeStub",
                return_value=node_stub,
            ) as stub_factory,
            mock.patch(
                "learningagent.server.build_server",
                side_effect=build_test_server,
            ) as server_factory,
            mock.patch(
                "learningagent.server.MultiRF",
                return_value=RecordingCMAB(["pbft"]),
            ),
        ):
            run_server(2, "local", stop_event)

        insecure_channel.assert_called_once_with("127.0.0.1:28200")
        stub_factory.assert_called_once_with(channel)
        server_factory.assert_called_once()
        build_args = server_factory.call_args.args
        self.assertEqual(build_args[:3], (2, "127.0.0.1:29002", node_stub))
        self.assertIsInstance(build_args[3], queue.Queue)
        server.stop.assert_called_once_with(GRACEFUL_STOP_SECONDS)
        stopped.wait.assert_called_once_with()
        stop_event.wait.assert_called_once_with()
        node_stub.SendDecision.assert_called_once()
        channel.close.assert_called_once_with()
        self.assertLess(events.index("stop-server"), events.index("close-channel"))
        self.assertLess(events.index("send-decision"), events.index("close-channel"))

    def test_run_server_closes_channel_when_server_build_fails(self):
        channel = mock.Mock()

        with (
            mock.patch(
                "learningagent.server.grpc.insecure_channel",
                return_value=channel,
            ),
            mock.patch(
                "learningagent.server.learning_agent_pb2_grpc.LearningAgentNodeStub",
            ),
            mock.patch(
                "learningagent.server.build_server",
                side_effect=RuntimeError("build failed"),
            ),
        ):
            with self.assertRaisesRegex(RuntimeError, "build failed"):
                run_server(1, "local", mock.Mock())

        channel.close.assert_called_once_with()


if __name__ == "__main__":
    unittest.main()
