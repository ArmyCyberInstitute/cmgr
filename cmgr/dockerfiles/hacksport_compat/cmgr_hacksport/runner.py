#!/usr/bin/env python3
"""Build a picoCTF hacksport challenge into cmgr's container contract.

The lifecycle and API behavior are adapted from the MIT-licensed picoCTF
hacksport deploy implementation, release v19.2.10 at commit
fb09fa2cb745c2db007dc4be8f95e37a1788c830. See LICENSE.picoCTF.
"""

import argparse
import functools
import hashlib
import importlib.util
import json
import os
import pwd
import grp
import re
import shutil
import stat
import tarfile
import tempfile
from pathlib import Path
from random import Random

from jinja2 import Environment, FileSystemLoader, Template

import hacksport.deploy
from hacksport.docker import DockerChallenge
from hacksport.problem import (
    Compiled,
    Directory,
    ExecutableFile,
    File,
    FlaskApp,
    GroupWriteDirectory,
    PHPApp,
    PreTemplatedFile,
    ProtectedFile,
    Remote,
    Service,
)

LOOKUP_PATTERN = re.compile(r"""{{\s*lookup\(\s*['"]([a-zA-Z0-9_]+)['"]\s*\)\s*}}""")
REQUIRED_TWEAKS_KEY = "__cmgr_seccomp_tweaks"
ALLOW_DISABLE_ASLR = "allow-disable-aslr"
DEPLOY_SECRET = hashlib.md5(b"foo\n").hexdigest()


def contained(root, relative, *, must_exist=True):
    relative_path = Path(relative)
    if relative_path.is_absolute() or ".." in relative_path.parts:
        raise ValueError(
            "hacksport file path must stay inside the challenge: {!r}".format(
                str(relative)
            )
        )
    candidate = root / relative_path
    resolved_root = root.resolve()
    resolved = candidate.resolve(strict=must_exist)
    if resolved != resolved_root and resolved_root not in resolved.parents:
        raise ValueError(
            "hacksport file path escapes the challenge: {!r}".format(str(relative))
        )
    return candidate


def public_attributes(problem):
    return {
        name: getattr(problem, name)
        for name in dir(problem)
        if not name.startswith("_")
    }


def load_problem_class(work):
    module_path = work / "challenge.py"
    if not module_path.is_file():
        raise RuntimeError("hacksport challenge is missing challenge.py")
    spec = importlib.util.spec_from_file_location("challenge", module_path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    if not hasattr(module, "Problem"):
        raise RuntimeError("challenge.py does not define Problem")
    if issubclass(module.Problem, DockerChallenge):
        raise RuntimeError(
            "hacksport DockerChallenge cannot be built without exposing a "
            "Docker daemon. Convert its Dockerfile to challenge_type 'custom'."
        )
    return module.Problem


def injected_problem_class(problem_class, metadata, seed, application):
    attributes = dict(metadata)
    attributes.update(
        {
            "random": Random(seed),
            "user": "challenge",
            "directory": str(application),
            "server": "challenge",
            "hostname": "challenge",
            "web_server": "http://challenge:5000",
            "web_root": "/challenge/static",
            "default_user": "root",
            "problem_directory_root": str(application),
            "obfuscate_problem_directories": False,
            "deploy_secret": DEPLOY_SECRET,
            "instance_number": int(os.environ.get("SEED", "0")),
        }
    )
    return type(
        "{}CmgrCompatibility".format(problem_class.__name__),
        (problem_class,),
        attributes,
    )


def template_file(path, context):
    try:
        text = path.read_text()
    except UnicodeDecodeError:
        return
    environment = Environment(
        loader=FileSystemLoader(str(path.parent)),
        keep_trailing_newline=True,
    )
    rendered = environment.get_template(path.name).render(**context)
    path.write_text(rendered)


def template_tree(work, problem):
    excluded = {
        "app/templates",
        "problem.json",
        "problem.md",
        "challenge.py",
        "templates",
        "__pre_templated",
        ".cmgr",
    }
    excluded.update(str(path).rstrip("/") for path in problem.dont_template)

    for root, dirnames, filenames in os.walk(work):
        relative_root = Path(root).relative_to(work)
        dirnames[:] = [
            dirname
            for dirname in dirnames
            if str(relative_root / dirname) not in excluded
        ]
        if any(
            relative_root == Path(item) or Path(item) in relative_root.parents
            for item in excluded
        ):
            continue
        for filename in filenames:
            relative = relative_root / filename
            if str(relative) in excluded:
                continue
            template_file(work / relative, public_attributes(problem))


def copy_file_descriptor(
    descriptor,
    work,
    pretemplated,
    application,
    challenge_uid,
    challenge_gid,
):
    destination = contained(application, descriptor.path, must_exist=False)
    destination.parent.mkdir(parents=True, exist_ok=True)

    if isinstance(descriptor, Directory):
        destination.mkdir(parents=True, exist_ok=True)
    else:
        source_root = pretemplated if isinstance(descriptor, PreTemplatedFile) else work
        source = contained(source_root, descriptor.path)
        if not source.is_file():
            raise RuntimeError(
                "declared hacksport file does not exist: {!r}".format(descriptor.path)
            )
        shutil.copy2(source, destination)

    if isinstance(
        descriptor,
        (ProtectedFile, ExecutableFile, GroupWriteDirectory),
    ):
        uid = 0
        gid = challenge_gid
    else:
        uid = 0 if descriptor.user is None else pwd.getpwnam(descriptor.user).pw_uid
        gid = 0 if descriptor.group is None else grp.getgrnam(descriptor.group).gr_gid
    os.chown(destination, uid, gid)
    os.chmod(destination, descriptor.permissions)


def collect_files(problem):
    files = list(problem.files)
    if isinstance(problem, Compiled):
        files.extend(problem.compiled_files)
    if isinstance(problem, Service):
        files.extend(problem.service_files)
    if not all(isinstance(item, File) for item in files):
        raise RuntimeError("all deployed hacksport files must use the File API")
    return files


def configure_runtime(problem, application):
    runtime = {"kind": "idle"}
    if isinstance(problem, FlaskApp):
        runtime = {"kind": "flask", "app": problem.app}
    elif isinstance(problem, PHPApp):
        document_root = contained(
            application,
            problem.php_root,
            must_exist=False,
        )
        runtime = {"kind": "php", "document_root": str(document_root)}
    elif isinstance(problem, Service):
        entrypoint = Path(problem.start_cmd)
        if not entrypoint.is_absolute():
            entrypoint = contained(application, entrypoint)
        if not entrypoint.is_file():
            raise RuntimeError(
                "hacksport service entrypoint does not exist: {}".format(entrypoint)
            )
        runtime = {"kind": "socket", "entrypoint": str(entrypoint)}

    (application / ".cmgr-hacksport-runtime.json").write_text(
        json.dumps(runtime, sort_keys=True)
    )
    os.chown(
        application / ".cmgr-hacksport-runtime.json",
        0,
        grp.getgrnam("challenge").gr_gid,
    )
    os.chmod(application / ".cmgr-hacksport-runtime.json", 0o440)


def render_metadata(problem, metadata, artifacts):
    context = public_attributes(problem)
    description = Template(str(metadata.get("description", ""))).render(**context)
    hints = [
        Template(str(hint)).render(**context) for hint in (metadata.get("hints") or [])
    ]
    return "\n".join([description] + hints), artifacts


def write_outputs(problem, metadata, artifacts, challenge_output):
    challenge_output.mkdir(parents=True, exist_ok=True)
    output = {"flag": problem.flag}

    templated_metadata, _ = render_metadata(problem, metadata, artifacts)
    lookup_names = set(LOOKUP_PATTERN.findall(templated_metadata))
    lookup_names.update(
        LOOKUP_PATTERN.findall(
            str(metadata.get("details", ""))
            + "\n"
            + "\n".join(str(hint) for hint in (metadata.get("hints") or []))
        )
    )
    for name in sorted(lookup_names):
        if not hasattr(problem, name):
            raise RuntimeError(
                "challenge metadata references missing lookup {!r}".format(name)
            )
        value = getattr(problem, name)
        if isinstance(value, (str, int, float, bool)):
            output[name] = str(value)
        else:
            raise RuntimeError(
                "challenge lookup {!r} is not a scalar value".format(name)
            )

    extra_metadata = getattr(problem, "metadata", {})
    if isinstance(extra_metadata, dict):
        for name, value in extra_metadata.items():
            if name in {REQUIRED_TWEAKS_KEY, "flag"}:
                continue
            if isinstance(value, (str, int, float, bool)):
                output[name] = str(value)

    if isinstance(problem, Remote) and problem.remove_aslr:
        output[REQUIRED_TWEAKS_KEY] = ALLOW_DISABLE_ASLR

    (challenge_output / "metadata.json").write_text(json.dumps(output, sort_keys=True))

    if artifacts:
        with tarfile.open(
            challenge_output / "artifacts.tar.gz",
            "w:gz",
        ) as archive:
            for name, source in sorted(artifacts.items()):
                archive.add(source, arcname=name, recursive=False)


def compatibility_seed(metadata):
    instance = os.environ.get("SEED", "0")
    value = "{}{}{}".format(metadata["name"], DEPLOY_SECRET, instance)
    return hashlib.md5(value.encode("utf-8")).hexdigest()


def build(args):
    source = Path(args.source).resolve()
    application = Path(args.application).resolve()
    challenge_output = Path(args.challenge_output).resolve()
    metadata = json.loads(Path(args.metadata).read_text())

    if metadata.get("pip_python_version") == "2":
        raise RuntimeError(
            "Python 2 hacksport challenges require challenge_type " "hacksport-legacy"
        )

    with tempfile.TemporaryDirectory(prefix="cmgr-hacksport-") as temporary:
        work = Path(temporary) / "work"
        shutil.copytree(
            source,
            work,
            symlinks=True,
            ignore=shutil.ignore_patterns(".cmgr", "Dockerfile"),
        )
        os.chdir(work)

        problem_class = load_problem_class(work)
        problem_class = injected_problem_class(
            problem_class,
            metadata,
            compatibility_seed(metadata),
            application,
        )
        problem = problem_class()
        for attribute in (
            "files",
            "dont_template",
            "compiled_files",
            "service_files",
        ):
            if hasattr(problem, attribute):
                setattr(problem, attribute, list(getattr(problem, attribute)))

        flag_random = Random(compatibility_seed(metadata))
        problem.flag = problem.generate_flag(flag_random)
        problem.flag_sha1 = hashlib.sha1(problem.flag.encode("utf-8")).hexdigest()
        problem.metadata = dict(getattr(problem, "metadata", {}))

        problem.initialize()

        pretemplated = Path(temporary) / "pretemplated"
        shutil.copytree(work, pretemplated, symlinks=True)

        artifacts = {}

        def url_for(source_name, display=None, raw=False, pre_templated=False):
            source_root = pretemplated if pre_templated else work
            source_path = contained(source_root, source_name)
            if not source_path.is_file():
                raise RuntimeError(
                    "published hacksport artifact does not exist: {!r}".format(
                        source_name
                    )
                )
            artifacts[source_name] = source_path
            if raw:
                return source_name
            label = source_name if display is None else display
            return "<a href='{}'>{}</a>".format(source_name, label)

        problem.url_for = url_for
        problem.url = functools.partial(url_for, raw=True)
        problem.lookup = lambda name: '{{lookup("{}")}}'.format(name)

        template_tree(work, problem)

        if isinstance(problem, Compiled):
            problem.compiler_setup()
        if isinstance(problem, Remote):
            problem.remote_setup()
        if isinstance(problem, FlaskApp):
            problem.flask_setup()
        if isinstance(problem, PHPApp):
            problem.php_setup()
        if isinstance(problem, Service):
            problem.service_setup()

        problem.setup()

        application.mkdir(parents=True, exist_ok=True)
        challenge_uid = pwd.getpwnam("challenge").pw_uid
        challenge_gid = grp.getgrnam("challenge").gr_gid
        os.chown(application, 0, challenge_gid)
        os.chmod(application, 0o750)

        for descriptor in collect_files(problem):
            copy_file_descriptor(
                descriptor,
                work,
                pretemplated,
                application,
                challenge_uid,
                challenge_gid,
            )

        configure_runtime(problem, application)
        write_outputs(problem, metadata, artifacts, challenge_output)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", required=True)
    parser.add_argument("--metadata", required=True)
    parser.add_argument("--application", required=True)
    parser.add_argument("--challenge-output", required=True)
    build(parser.parse_args())


if __name__ == "__main__":
    main()
