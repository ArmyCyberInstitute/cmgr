#!/usr/bin/python3
import argparse
import socket

parser = argparse.ArgumentParser(description="solve script for 'BinEx101'")
parser.add_argument('--host', default="challenge", help="the host for the instance")
parser.add_argument('--port', type=int, default=5000, help="the port of the instance")
parser.add_argument('--print', action='store_true', help="print flag to stdout rather than saving to file")
args = parser.parse_args()

def recv_until(sock, pattern):
    response = sock.recv(4096).decode()
    while len(response) < len(pattern) or response[-len(pattern):] != pattern:
        response += sock.recv(4096).decode()
    return response

target = 2**32 - 1
# print("target =", target)
p = 2
q = 0
while p*p < target:
    if target % p == 0:
        q = target // p
        break
    p += 1

if p*q != target:
    print("error: could not factor the target number")
    exit(-1)

c = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
c.connect((args.host, args.port))
recv_until(c, ":\r\n")
c.sendall(("%d\n" % p).encode())
recv_until(c, ":\r\n")
c.sendall(("%d\n" % q).encode())
response = recv_until(c, "'\r\n")

flag = response.split("'")[-2]

if args.print:
    print(f"flag: {flag}")
else:
    with open('flag', 'w') as f:
        f.write(flag)
