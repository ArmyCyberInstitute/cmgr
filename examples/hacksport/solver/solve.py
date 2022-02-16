#!/usr/bin/env python3

import base64
import os
import socket

from Crypto.Cipher import AES


def write_flag(flag):
    with open("flag", "w") as f:
        f.write(flag)


def solve(host, port):
    # connect to service
    c = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    c.connect((host, port))
    lines = c.recv(4096).decode().splitlines()

    # extract relevant data
    key, msg = [x.split(":")[1].strip() for x in lines[3:5]]

    # decrypt
    cipher = AES.new(base64.b16decode(key, casefold=True), AES.MODE_ECB)
    flag = cipher.decrypt(base64.b16decode(msg)).decode("utf-8")

    # sabe result
    write_flag(flag)


if __name__ == "__main__":
    host = os.environ["HOST"] if "HOST" in os.environ else "challenge"
    port = int(os.environ["PORT"]) if "PORT" in os.environ else 5000
    solve(host, port)
