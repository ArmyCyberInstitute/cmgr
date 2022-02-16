#!/usr/bin/env python3
import argparse
import binascii
import socket
import sys

from pwnlib.elf import ELF

parser = argparse.ArgumentParser(description="solve script for 'read_it'")
parser.add_argument("--host", default="challenge", help="the host for the instance")
parser.add_argument("--port", type=int, default=5000, help="the port of the instance")
args = parser.parse_args()

elf = ELF("read_it")

key = elf.string(elf.symbols["key"])
secret1 = elf.string(elf.symbols["secret1"])
secret2 = elf.string(elf.symbols["secret2"])
print("key:    ", key.decode())
print("secret1:", secret1.decode())
print("secret2:", secret2.decode())


def encode_first(data):
    r = bytearray()
    for i in range(0, 16):
        r.append(data[i] ^ 0x17)
    return r


def encode_second(data):
    k = ""
    for i in range(0, 16):
        k += chr((key[i] >> 4) % 0x7F)

    r = bytearray()
    for i in range(0, 16):
        r.append((data[i] ^ ord(k[i])))
    return r


client = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
client.connect((args.host, args.port))

if len(key) != 16 or len(secret1) != 16 or len(secret2) != 16:
    print("error: unexpected key/secret length")
    exit(-1)


data = client.recv(1024)

part1 = encode_first(secret1)
part2 = encode_second(secret2)

f = part1 + part2
print("message:", f.decode())
client.send(f + b"\n")
flag = client.recv(1024).decode().split("\n")[-1]

print("flag:   ", flag)
with open("flag", "w") as f:
    f.write(flag)
