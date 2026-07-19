import unittest

import grpc

from learningagent import learning_agent_pb2
from learningagent import learning_agent_pb2_grpc
from learningagent.server import build_server


class LearningAgentServerTest(unittest.TestCase):
    def setUp(self):
        self.server, port = build_server(2, "127.0.0.1:0")
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


if __name__ == "__main__":
    unittest.main()

