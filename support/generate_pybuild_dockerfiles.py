from base64 import b64encode
import hashlib
import os

TEMPLATE_EXT = ".template"

with open("../../support/pybuild.py", "rb") as f:
    pybuild_source = b64encode(f.read()).decode()


def generate_dockerfile(target_file):
    with open(target_file + TEMPLATE_EXT, "r") as f:
        template = f.read()
    template = "# Generated from a template file. DO NOT EDIT.\n" + template.replace(
        "{{PYBUILD}}", pybuild_source
    )
    with open(target_file, "w") as f:
        f.write(template)


for (path, _, files) in os.walk("."):
    for f in files:
        basename, extension = os.path.splitext(f)
        if extension == TEMPLATE_EXT and ("pybuild" in path or "pybuild" in basename):
            generate_dockerfile(os.path.join(path, basename))
