#!/usr/bin/env python3
"""Launch the runtime selected by the hacksport compatibility build."""

import json
import os
from pathlib import Path

RUNTIME_PATH = Path("/app/.cmgr-hacksport-runtime.json")


def execute(arguments):
    os.execvpe(arguments[0], arguments, os.environ)


def main():
    runtime = json.loads(RUNTIME_PATH.read_text())
    kind = runtime["kind"]

    if kind == "socket":
        execute(
            [
                "socat",
                "TCP-LISTEN:5000,reuseaddr,fork",
                "EXEC:{},stderr".format(runtime["entrypoint"]),
            ]
        )
    if kind == "flask":
        execute(
            [
                "/opt/cmgr-hacksport-venv/bin/python",
                "-m",
                "flask",
                "--app",
                runtime["app"],
                "run",
                "--host",
                "0.0.0.0",
                "--port",
                "5000",
            ]
        )
    if kind == "php":
        execute(
            [
                "php",
                "-S",
                "0.0.0.0:5000",
                "-t",
                runtime["document_root"],
            ]
        )
    if kind == "idle":
        execute(["tail", "-f", "/dev/null"])
    raise RuntimeError("unsupported hacksport runtime kind {!r}".format(kind))


if __name__ == "__main__":
    main()
