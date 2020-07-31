import random

class Builder:
    program_name = "read_it"
    artifacts = [program_name]

    # Set attributes before templating occurs
    def prebuild(self):
        # Clamp the flag to a size that fits in the binary.
        if len(self.flag) > 31:
            fill = ''.join(random.choice("0123456789abcdef") for i in range(32 - len(self.flag_format)))
            self.flag = self.flag_format % fill
        with open("flag", "w") as f:
            f.write(self.flag)

        # Generate the secrets and key for the binary
        alphabet = [chr(c) for c in range(ord('('), ord('\\'))] + [chr(c) for c in range(ord(']'), 127)]
        self.key = ''.join(random.choice(alphabet) for i in range(16))
        self.secret1 = ''.join(random.choice(alphabet) for i in range(16))
        self.secret2 = ''.join(random.choice(alphabet) for i in range(16))

