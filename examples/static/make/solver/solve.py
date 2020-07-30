import subprocess

# Note: This validates the correct flag is embedded but uses the password
#   rather than properly reversing the function call to printf.
lockbox = subprocess.run(
    ["./lockbox"],
    text=True,
    input="correct horse battery staple\n",
    capture_output=True
)

with open("flag", "w") as f:
    f.write(lockbox.stdout.split(" ")[-1])
