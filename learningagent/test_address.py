import unittest

from learningagent.address import server_address


class ServerAddressTest(unittest.TestCase):
    def test_local_addresses_use_one_port_per_node(self):
        self.assertEqual(server_address(1, "local"), "127.0.0.1:29001")
        self.assertEqual(server_address(4, "local"), "127.0.0.1:29004")

    def test_loopback_addresses_use_one_ip_per_node(self):
        self.assertEqual(server_address(1, "loopbackip"), "127.0.0.2:29000")
        self.assertEqual(server_address(4, "loopbackip"), "127.0.0.5:29000")

    def test_invalid_node_and_mode_are_rejected(self):
        with self.assertRaises(ValueError):
            server_address(0, "local")
        with self.assertRaises(ValueError):
            server_address(1, "remote")


if __name__ == "__main__":
    unittest.main()

