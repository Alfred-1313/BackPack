#!/usr/bin/env python3
"""Forward one TCP port through the local Hysteria SOCKS5 listener."""
import socket
import struct
import sys
import threading


def read_exact(sock, size):
    data = bytearray()
    while len(data) < size:
        piece = sock.recv(size - len(data))
        if not piece:
            raise EOFError("SOCKS5 connection closed during handshake")
        data.extend(piece)
    return bytes(data)


def connect_backend(proxy, target):
    sock = socket.create_connection(proxy, timeout=10)
    try:
        sock.sendall(b"\x05\x01\x00")
        if read_exact(sock, 2) != b"\x05\x00":
            raise OSError("SOCKS5 authentication refused")
        host = target[0].encode("idna")
        sock.sendall(b"\x05\x01\x00\x03" + bytes([len(host)]) + host
                     + struct.pack("!H", target[1]))
        version, status, reserved, address_type = read_exact(sock, 4)
        if (version, status, reserved) != (5, 0, 0):
            raise OSError("SOCKS5 CONNECT refused")
        if address_type == 3:
            length = read_exact(sock, 1)[0]
        elif address_type in (1, 4):
            length = 4 if address_type == 1 else 16
        else:
            raise OSError("unknown SOCKS5 address type")
        read_exact(sock, length + 2)
        sock.settimeout(None)
        return sock
    except (OSError, ValueError, EOFError):
        sock.close()
        raise


def shutdown(sock, how):
    try:
        sock.shutdown(how)
    except OSError:
        pass


def copy_stream(source, dest):
    try:
        while True:
            data = source.recv(65536)
            if not data:
                shutdown(dest, socket.SHUT_WR)
                return
            dest.sendall(data)
    except OSError:
        # Unblock the other direction if this connection has failed.
        shutdown(source, socket.SHUT_RDWR)
        shutdown(dest, socket.SHUT_RDWR)


def handle(client, proxy, target):
    with client:
        try:
            backend = connect_backend(proxy, target)
        except (OSError, ValueError, EOFError):
            return
        with backend:
            upload = threading.Thread(target=copy_stream, args=(client, backend))
            upload.start()
            copy_stream(backend, client)
            upload.join()


def main(args):
    bind, proxy, target = [(args[i], int(args[i + 1])) for i in (0, 2, 4)]
    with socket.socket() as listener:
        listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        listener.bind(bind)
        listener.listen(256)
        while True:
            client, _ = listener.accept()
            threading.Thread(target=handle, args=(client, proxy, target), daemon=True).start()


if __name__ == "__main__":
    main(sys.argv[1:])
