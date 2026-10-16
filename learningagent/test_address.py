import json
from pathlib import Path
import tempfile
import unittest

from learningagent.address import load_hosts, node_address, server_address


class ServerAddressTest(unittest.TestCase):
    def test_local_addresses_use_one_port_per_node(self):
        self.assertEqual(server_address(1, "local"), "127.0.0.1:29001")
        self.assertEqual(server_address(4, "local"), "127.0.0.1:29004")

    def test_loopback_addresses_use_one_ip_per_node(self):
        self.assertEqual(server_address(1, "loopbackip"), "127.0.0.2:29000")
        self.assertEqual(server_address(4, "loopbackip"), "127.0.0.5:29000")

    def test_local_node_addresses_match_go_configuration(self):
        self.assertEqual(node_address(1, "local"), "127.0.0.1:28100")
        self.assertEqual(node_address(4, "local"), "127.0.0.1:28400")

    def test_loopback_node_addresses_match_go_configuration(self):
        self.assertEqual(node_address(1, "loopbackip"), "127.0.0.2:28100")
        self.assertEqual(node_address(4, "loopbackip"), "127.0.0.5:28400")

    def test_invalid_node_and_mode_are_rejected(self):
        with self.assertRaises(ValueError):
            server_address(0, "local")
        with self.assertRaises(ValueError):
            server_address(1, "remote")
        with self.assertRaises(ValueError):
            node_address(0, "local")
        with self.assertRaises(ValueError):
            node_address(1, "remote")
        with self.assertRaises(ValueError):
            server_address(1, "wan")

    def test_remote_addresses_come_from_the_hosts_file(self):
        hosts_file = {
            "run_dir": "pbft-run",
            "nodes": {
                str(i): {"ip": f"10.10.1.{i}", "ssh": f"node{i}"} for i in range(1, 8)
            },
            "clients": {"1": {"ip": "10.10.1.8", "ssh": "client"}},
        }
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "hosts.json"
            path.write_text(json.dumps(hosts_file))
            hosts = load_hosts(path)

        self.assertEqual(server_address(7, "remote", hosts), "10.10.1.7:29000")
        # Same port formula as config.GenerateRemoteNetwork.
        self.assertEqual(node_address(1, "remote", hosts), "10.10.1.1:28100")
        self.assertEqual(node_address(7, "remote", hosts), "10.10.1.7:28700")
        with self.assertRaises(ValueError):
            server_address(8, "remote", hosts)


if __name__ == "__main__":
    unittest.main()
