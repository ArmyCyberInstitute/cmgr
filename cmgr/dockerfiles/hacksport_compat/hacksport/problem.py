"""Challenge and file APIs compatible with picoCTF hacksport v19.2.10.

This is a modified container-focused implementation derived from the
MIT-licensed picoCTF source at commit
fb09fa2cb745c2db007dc4be8f95e37a1788c830. See LICENSE.picoCTF.
"""

import os
import string
from abc import ABCMeta, abstractmethod
from hashlib import md5
from os.path import join
from random import Random

from hacksport.deploy import flag_fmt, give_port
from hacksport.operations import execute

XINETD_SCRIPT = """#!/bin/bash
cd "$(dirname "$0")"
exec timeout -sKILL 3m %s
"""
XINETD_WEB_SCRIPT = """#!/bin/bash
cd "$(dirname "$0")"
%s
"""


class File:
    def __init__(self, path, permissions=0o664, user=None, group=None):
        self.path = path
        self.permissions = permissions
        self.user = user
        self.group = group

    def __repr__(self):
        return "{}({},{})".format(
            self.__class__.__name__, repr(self.path), oct(self.permissions)
        )

    def to_dict(self):
        return {
            "path": self.path,
            "permissions": self.permissions,
            "user": self.user,
            "group": self.group,
        }


class Directory(File):
    pass


class GroupWriteDirectory(Directory):
    def __init__(self, path, permissions=0o770):
        super().__init__(path, permissions=permissions)


class PreTemplatedFile(File):
    def __init__(self, path, permissions=0o664):
        super().__init__(path, permissions=permissions)


class ExecutableFile(File):
    def __init__(self, path, permissions=0o2755):
        super().__init__(path, permissions=permissions)


class ProtectedFile(File):
    def __init__(self, path, permissions=0o440):
        super().__init__(path, permissions=permissions)


def files_from_directory(directory, recurse=True, permissions=0o664):
    result = []
    for root, _dirnames, filenames in os.walk(directory):
        for filename in filenames:
            result.append(File(join(root, filename), permissions))
        if not recurse:
            break
    return result


class Challenge(metaclass=ABCMeta):
    files = []
    dont_template = []

    def generate_flag(self, random):
        token = str(random.randint(1, int(1e12)))
        return flag_fmt() % md5(token.encode("utf-8")).hexdigest()

    def std_flag_format(self, msg="", numrand=0):
        value = msg
        if numrand > 0:
            if value:
                value += "_"
            random = getattr(self, "random", Random())
            value += "".join(
                random.choice(string.ascii_letters + string.digits)
                for _ in range(numrand)
            )
        return flag_fmt() % value

    def initialize(self):
        pass

    @abstractmethod
    def setup(self):
        pass

    def service(self):
        return {"Type": "oneshot", "ExecStart": "/bin/bash -c 'echo started'"}


class Compiled(Challenge):
    compiler = "gcc"
    compiler_flags = []
    compiler_sources = []
    makefile = None
    program_name = None
    compiled_files = []

    def setup(self):
        pass

    def compiler_setup(self):
        if self.program_name is None:
            raise RuntimeError("Must specify program_name for compiled challenge.")
        if self.makefile is not None:
            execute(["make", "-f", self.makefile])
        elif self.compiler_sources:
            execute(
                [self.compiler]
                + list(self.compiler_flags)
                + list(self.compiler_sources)
                + ["-o", self.program_name]
            )
        if not isinstance(self, Remote):
            self.compiled_files = [ExecutableFile(self.program_name)]


class Service(Challenge):
    service_files = []
    start_cmd = None

    def setup(self):
        pass

    def service_setup(self):
        if self.start_cmd is None:
            raise RuntimeError("Must specify start_cmd for services.")
        with open("xinet_startup.sh", "w") as startup:
            startup.write(XINETD_SCRIPT % self.start_cmd)
        self.start_cmd = join(self.directory, "xinet_startup.sh")
        self.service_files.append(ExecutableFile("xinet_startup.sh"))

    @property
    def port(self):
        if not hasattr(self, "_port"):
            self._port = give_port()
        return self._port

    def service(self):
        return {"Type": "simple", "ExecStart": self.start_cmd}


class Remote(Service):
    remove_aslr = False

    def remote_setup(self):
        if self.program_name is None:
            raise RuntimeError("Must specify program_name for remote challenge.")
        if self.remove_aslr:
            self.service_files = [File(self.program_name, permissions=0o755)]
            self.program_name = self.make_no_aslr_wrapper(
                join(self.directory, self.program_name),
                output="{}_no_aslr".format(self.program_name),
            )
        else:
            self.service_files = [ExecutableFile(self.program_name)]
        self.start_cmd = join(self.directory, self.program_name)

    def make_no_aslr_wrapper(self, exec_path, output="no_aslr_wrapper"):
        source = os.path.join(os.path.dirname(__file__), "static", "no_aslr_wrapper.c")
        execute(
            [
                "gcc",
                "-o",
                output,
                '-DBINARY_PATH="{}"'.format(exec_path),
                source,
            ]
        )
        self.files.append(ExecutableFile(output))
        return output

    def service(self):
        return {"Type": "oneshot", "ExecStart": self.start_cmd}


class WebService(Service):
    def service_setup(self):
        if self.start_cmd is None:
            raise RuntimeError("Must specify start_cmd for services.")
        with open("xinet_startup.sh", "w") as startup:
            startup.write(XINETD_WEB_SCRIPT % self.start_cmd)
        self.start_cmd = join(self.directory, "xinet_startup.sh")
        self.service_files.append(ExecutableFile("xinet_startup.sh"))


class FlaskApp(WebService):
    python_version = "3"
    app = "server:app"
    num_workers = 1

    @property
    def flask_secret(self):
        if not hasattr(self, "_flask_secret"):
            token = str(self.random.randint(1, int(1e16)))
            self._flask_secret = md5(token.encode("utf-8")).hexdigest()
        return self._flask_secret

    def flask_setup(self):
        if str(self.python_version) not in {"3", "3.7"}:
            raise RuntimeError(
                "Python 2 hacksport Flask applications require "
                "challenge_type hacksport-legacy"
            )
        self.app_file = "{}.py".format(self.app.split(":")[0])
        if not os.path.isfile(self.app_file):
            raise RuntimeError("Flask application module must exist")
        self.service_files = [File(self.app_file)]
        self.start_cmd = "python -m flask --app {} run".format(self.app)


class PHPApp(WebService):
    php_root = ""
    num_workers = 1

    def php_setup(self):
        self.start_cmd = "php -S 0.0.0.0:5000 -t {}".format(
            join(self.directory, self.php_root)
        )
