#!/usr/bin/env python3
"""Exercise the TCP forwarder's transport semantics on loopback."""
import runpy
import socket
import struct
import threading
import unittest
from pathlib import Path
from unittest.mock import patch

relay = runpy.run_path(str(Path(__file__).with_name("omni-socks-forward.py")))


class SocksHandshakeTest(unittest.TestCase):
    def check_reply(self, address_field):
        class Reply:
            timeout = 10
            def __init__(self):
                self.data = bytearray(b"\x05\x00\x05\x00\x00" + address_field + struct.pack("!H", 4321) + b"payload")
            def recv(self, size):
                part = self.data[:min(1, size)]
                del self.data[:len(part)]
                return bytes(part)
            def sendall(self, data):
                pass
            def settimeout(self, value):
                self.timeout = value
            def close(self):
                pass

        reply = Reply()
        with patch("socket.create_connection", return_value=reply):
            backend = relay["connect_backend"](("127.0.0.1", 1), ("localhost", 80))
        self.assertIsNone(backend.timeout)
        self.assertEqual(reply.data, b"payload")

    def test_fragmented_replies(self):
        for addr in (b"\x01" + bytes(4), b"\x03\x03abc", b"\x04" + bytes(16)):
            with self.subTest(kind=addr[0]):
                self.check_reply(addr)

    def test_server_half_close_preserves_later_upload(self):
        client, downstream = socket.socketpair()
        upstream, service = socket.socketpair()
        with client, downstream, upstream, service:
            client.settimeout(3)
            service.settimeout(3)
            with patch.dict(relay["handle"].__globals__, connect_backend=lambda *_: upstream):
                worker = threading.Thread(target=relay["handle"], args=(downstream, None, None), daemon=True)
                worker.start()
                service.sendall(b"reply")
                service.shutdown(socket.SHUT_WR)
                self.assertEqual(relay["read_exact"](client, 5), b"reply")
                self.assertEqual(client.recv(1), b"")
                client.sendall(b"later upload")
                client.shutdown(socket.SHUT_WR)
                self.assertEqual(relay["read_exact"](service, 12), b"later upload")
                self.assertEqual(service.recv(1), b"")
                worker.join(3)
                self.assertFalse(worker.is_alive())


if __name__ == "__main__":
    unittest.main()
