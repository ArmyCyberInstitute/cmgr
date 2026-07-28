"""Process helpers compatible with the picoCTF hacksport API.

Adapted from picoCTF release v19.2.10, commit
fb09fa2cb745c2db007dc4be8f95e37a1788c830. See LICENSE.picoCTF.
"""

import subprocess
from dataclasses import dataclass


class TimeoutError(Exception):
    pass


@dataclass(frozen=True)
class ExecutionResult:
    return_code: int
    output: bytes
    stderr_output: bytes


def execute(cmd, timeout=600, **kwargs):
    if isinstance(cmd, str):
        cmd = ["bash", "-c", cmd]

    supported = {
        key: value for key, value in kwargs.items() if key in {"cwd", "env", "input"}
    }
    try:
        result = subprocess.run(
            cmd,
            check=True,
            capture_output=True,
            timeout=timeout,
            **supported,
        )
    except subprocess.TimeoutExpired as error:
        raise TimeoutError(cmd, timeout) from error
    return ExecutionResult(result.returncode, result.stdout, result.stderr)


def create_user(_username):
    raise RuntimeError(
        "hacksport create_user is unavailable inside a cmgr challenge build"
    )
