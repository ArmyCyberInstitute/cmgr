#!/usr/bin/env python3
"""Prepare legacy hacksport package metadata for a modern Ubuntu image."""

import argparse
import json
import re
from pathlib import Path

APT_PACKAGE_PATTERN = re.compile(r"^[a-zA-Z0-9][a-zA-Z0-9+.-]*$")
PYCRYPTO_PATTERN = re.compile(
    r"^\s*pycrypto(?:\s*(?:===|==|~=|!=|<=|>=|<|>).*)?$",
    re.IGNORECASE,
)

APT_ALIASES = {
    "php7.2-sqlite3": "php-sqlite3",
}


def read_lines(path):
    return [
        line.strip()
        for line in Path(path).read_text().splitlines()
        if line.strip() and not line.lstrip().startswith("#")
    ]


def normalize_apt_packages(metadata, packages_path):
    packages = list(metadata.get("pkg_dependencies", []))
    packages.extend(read_lines(packages_path))

    normalized = []
    seen = set()
    for package in packages:
        if not isinstance(package, str):
            raise ValueError("hacksport package dependencies must be strings")
        package = APT_ALIASES.get(package.strip(), package.strip())
        if not APT_PACKAGE_PATTERN.fullmatch(package):
            raise ValueError(
                "unsupported hacksport package dependency {!r}; modern "
                "compatibility builds accept package names only. Use "
                "hacksport-legacy for Debian relationship expressions.".format(package)
            )
        if package not in seen:
            normalized.append(package)
            seen.add(package)
    return normalized


def normalize_requirements(metadata, requirements_path):
    listed = metadata.get("pip_requirements", [])
    source = read_lines(requirements_path)
    if listed and source:
        raise ValueError(
            "hacksport challenge specifies both pip_requirements and "
            "requirements.txt"
        )
    if not isinstance(listed, list) or not all(
        isinstance(requirement, str) for requirement in listed
    ):
        raise ValueError("hacksport pip_requirements must be a list of strings")

    python_version = str(metadata.get("pip_python_version", "3"))
    if python_version == "2":
        raise ValueError(
            "Python 2 hacksport dependencies require challenge_type " "hacksport-legacy"
        )

    requirements = list(listed or source)
    normalized = []
    for requirement in requirements:
        if PYCRYPTO_PATTERN.fullmatch(requirement):
            normalized.append(
                "pycryptodome==3.23.0 "
                "# cmgr compatibility alias for abandoned pycrypto"
            )
        else:
            normalized.append(requirement)
    return normalized


def prepare(args):
    metadata = json.loads(Path(args.metadata).read_text())
    output = Path(args.output)
    output.mkdir(parents=True, exist_ok=True)

    apt_packages = normalize_apt_packages(metadata, args.packages)
    requirements = normalize_requirements(metadata, args.requirements)

    (output / "apt-packages.txt").write_text(
        "".join("{}\n".format(package) for package in apt_packages)
    )
    (output / "requirements.txt").write_text(
        "".join("{}\n".format(requirement) for requirement in requirements)
    )


def main():
    parser = argparse.ArgumentParser()
    subcommands = parser.add_subparsers(dest="command", required=True)
    prepare_parser = subcommands.add_parser("prepare")
    prepare_parser.add_argument("--metadata", required=True)
    prepare_parser.add_argument("--packages", required=True)
    prepare_parser.add_argument("--requirements", required=True)
    prepare_parser.add_argument("--output", required=True)
    args = parser.parse_args()

    if args.command == "prepare":
        prepare(args)


if __name__ == "__main__":
    main()
