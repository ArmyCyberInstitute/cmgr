import os
import random
import stat

import json
import jinja2

random.seed(os.environ["SEED"])
try:
    import build
    b = build.Builder()
except Exception as e:
    print(e)
    b = type("", (), {})()

b.flag = os.environ["FLAG"]
b.flag_format = os.environ["FLAG_FORMAT"]

if hasattr(b, "prebuild"):
    b.prebuild()

cflags = ["-DFLAG=%s" % b.flag, "-DFLAG_FORMAT=%s" % b.flag_format]
if hasattr(b, "x86_64") and not b.x86_64:
    cflags.append("-m32")

if hasattr(b, "executable_stack") and b.executable_stack:
    cflags.append("-zexecstack")

if hasattr(b, "stack_guards") and not b.stack_guards:
    cflags.append("-fno-stack-protector")
    cflags.append("-D_FORTIFY_SOURCE=0")
else:
    cflags.append("-D_FORTIFY_SOURCE=2")
    cflags.append("-fstack-clash-protection")
    cflags.append("-fstack-protector-strong")

if hasattr(b, "strip") and b.strip:
    cflags.append("-s")

if hasattr(b, "debug") and b.debug:
    cflags.append("-g")

if hasattr(b, "pie") and b.pie:
    cflags.append("-fPIE")
    cflags.append("-pie")
    cflags.append("-Wl,-pie")
else:
    cflags.append("-no-pie")

cflags = " ".join(cflags)
if hasattr(b, "extra_flags"):
    cflags = cflags + " " + " ".join(b.extra_flags)

os.environ["ASFLAGS"] = cflags
os.environ["CFLAGS"] = cflags
os.environ["CXXFLAGS"] = cflags

if not hasattr(b, "dont_template"):
    b.dont_template = []
b.dont_template.append("problem.md")
b.dont_template.append("problem.json")
b.dont_template.append("build.py")

for curr_dir, sub_dirs, files in os.walk("."):
    if curr_dir in b.dont_template:
        continue
    for fname in files:
        fpath = os.path.join(curr_dir, fname)
        if fpath[2:] in b.dont_template:
            continue
        print(fpath[2:])

        try:
            with open(fpath) as f:
                contents = f.read()
        except:
            continue

        template = jinja2.Template(contents)

        with open(fpath, "w") as f:
            f.write(template.render(b.__dict__))

if hasattr(b, "build"):
    b.build()
else:
    if hasattr(b, "program_name"):
        os.system("make %s" % b.program_name)
    else:
        os.system("make")

if not hasattr(b, "program_name"):
    b.program_name = "main"

if not hasattr(b, "exec"):
    b.exec = "./" + b.program_name
if hasattr(b, "aslr") and not b.aslr:
    b.exec = "setarch -R " + b.exec

with open("start.sh", "w") as f:
    f.write("#!/bin/bash\n%s\n" % b.exec)
os.chmod("start.sh", stat.S_IRWXU | stat.S_IXGRP | stat.S_IXOTH)

if hasattr(b, "artifacts"):
    os.system("tar czvf artifacts.tar.gz " + " ".join(b.artifacts))

if hasattr(b, "postbuild"):
    b.postbuild()

if not hasattr(b, "lookups"):
    b.lookups = {"flag": b.flag}
elif "flag" not in b.lookups:
    b.lookups["flag"] = b.flag

with open("metadata.json", "w") as f:
    f.write(json.dumps(b.lookups))

if hasattr(b, "remove"):
    for f in b.remove:
        os.remove(f)
