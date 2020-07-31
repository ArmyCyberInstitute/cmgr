#!/usr/bin/python3
import socket

target = 2**32 - 1
print("target =", target)
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
c.connect(("challenge", 5000))
c.recv(4096)
c.sendall(("%d\n" % p).encode())
c.recv(4096)
c.sendall(("%d\n" % q).encode())
response = c.recv(4096).decode()

flag = response.split("'")[-2]

with open('flag', 'w') as f:
    f.write(flag)
