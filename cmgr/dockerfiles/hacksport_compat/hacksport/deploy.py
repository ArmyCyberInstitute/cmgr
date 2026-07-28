"""Small, container-oriented subset of the picoCTF hacksport deploy API.

Adapted from picoCTF release v19.2.10, commit
fb09fa2cb745c2db007dc4be8f95e37a1788c830. See LICENSE.picoCTF.
"""

import os
from pathlib import Path

from jinja2 import Environment, FileSystemLoader, Template

CONTAINER_PORT = 5000
FLAG_FMT = os.environ.get("FLAG_FORMAT", "%s")
containerize = True


def flag_fmt():
    return FLAG_FMT


def give_port():
    return CONTAINER_PORT


def template_string(template, **kwargs):
    return Template(template).render(**kwargs)


def template_file(in_file_path, out_file_path, **kwargs):
    source = Path(in_file_path)
    environment = Environment(
        loader=FileSystemLoader(str(source.parent)),
        keep_trailing_newline=True,
    )
    output = environment.get_template(source.name).render(**kwargs)
    Path(out_file_path).write_text(output)
