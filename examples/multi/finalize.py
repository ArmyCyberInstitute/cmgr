#!/usr/bin/env python3
import json
import os
import random

random.seed(os.environ["SEED"])

metadata = {"flag": os.environ["FLAG"]}


def generate_password(length=10):
    ALPHABET = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_+=."
    return "".join(random.choices(ALPHABET, k=length))


alice_pass = generate_password()
eve_pass = generate_password()

metadata["username"] = "esmythe"
metadata["password"] = eve_pass

password_script = """
chpasswd << EOF
asmith:%s
esmythe:%s
EOF
""" % (
    alice_pass,
    eve_pass,
)

with open("set-passwords.sh", "w") as f:
    f.write(password_script)

with open("/challenge/metadata.json", "w") as f:
    f.write(json.dumps(metadata))
