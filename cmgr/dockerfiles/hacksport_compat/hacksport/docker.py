"""Import-compatible boundary for legacy hacksport DockerChallenge classes.

The API names are based on picoCTF release v19.2.10. cmgr deliberately does
not expose a Docker socket during a challenge build. See LICENSE.picoCTF.
"""


class DockerChallenge:
    ports = {}

    def __init__(self):
        raise RuntimeError(
            "hacksport DockerChallenge cannot run inside the cmgr compatibility "
            "builder because nested Docker access would require exposing the "
            "daemon. Convert the challenge Dockerfile to challenge_type "
            "'custom', or use an explicitly isolated legacy build workflow."
        )

    def initialize_docker(self, *_args, **_kwargs):
        raise RuntimeError(
            "hacksport DockerChallenge is not available in the cmgr "
            "compatibility builder"
        )

    def copy_from_image(self, *_args, **_kwargs):
        raise RuntimeError(
            "hacksport DockerChallenge image extraction is not available in cmgr"
        )


class HTTP:
    def __init__(self, desc, path="", link_text=""):
        self.desc = desc
        self.path = path
        self.link_text = link_text

    def dict(self):
        url = "http://{host}:{{port}}" + self.path
        text = url if self.link_text == "" else self.link_text
        return {
            "fmt": "<a href='{}' target='_blank'>{}</a>".format(url, text),
            "desc": self.desc,
        }


class Netcat:
    def __init__(self, desc):
        self.desc = desc

    def dict(self):
        return {"fmt": "<code>nc {host} {{port}}</code>", "desc": self.desc}


class Plain:
    def __init__(self, desc):
        self.desc = desc

    def dict(self):
        return {"fmt": "<code>{host}:{{port}}</code>", "desc": self.desc}


class Custom:
    def __init__(self, fmt, desc):
        self.desc = desc
        self.fmt = fmt

    def dict(self):
        return {"fmt": self.fmt, "desc": self.desc}
