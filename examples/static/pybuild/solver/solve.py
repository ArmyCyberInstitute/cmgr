import subprocess

# Note: This validates the correct flag is embedded but uses the password
#   rather than properly reversing the function call to printf.
re101 = subprocess.run(
    ["gdb", "--batch", "-x", "gdb-script", "re101"],
    text=True,
    capture_output=True
)

# Flag is after the first equal sign...
res = re101.stdout.split("=")[1]

# ... on the same line as it ...
res = res.split("\n")[0]

# ... and in between the last pair of quotes.
res = res.split('"')[-2]

with open("flag", "w") as f:
    f.write(res)
