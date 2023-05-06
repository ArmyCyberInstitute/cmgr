import secrets
import string

from hacksport.problem import ProtectedFile, Remote, Challenge
from hacksport.deploy import flag_fmt


class Problem(Remote):
    program_name = "ecb.py"
    files = [ProtectedFile("flag"), ProtectedFile("key")]

    def initialize(self):
        # generate random 32 hexadecimal characters
        self.enc_key = "".join(
            self.random.choice(string.digits + "abcdef") for _ in range(32)
        )

        self.welcome_message = (
            "Welcome to Secure Encryption Service version 1.{}".format(
                self.random.randint(0, 10)
            )
        )

    # flag length must be a multiple of 16
    def generate_flag(self, random):
        flag = (flag_fmt() % secrets.token_hex(32))[:32]
        if "{" in flag:
            flag = flag[:31] + "}"
        assert len(flag) % 16 == 0
        return flag
