FROM ubuntu:20.04
ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update
RUN apt-get -y install python3-pip build-essential
RUN apt-get -y install gcc-multilib
RUN pip3 install ninja2
RUN install -d -m 0700 /challenge
# End of shared layers for all flask challenges

COPY Dockerfile packages.txt* ./
RUN if [ -f packages.txt ]; then xargs -a packages.txt apt-get install -y; fi

COPY Dockerfile requirements.txt* ./
RUN if [ -f requirements.txt ]; then pip3 install -r requirements.txt; fi

COPY . /app
WORKDIR /app

# End of share layers for all builds of the same flask challenge

ARG FLAG_FORMAT
ARG FLAG
ARG SEED

RUN printf 'import os\n\
import random\n\
import stat\n\
\n\
import json\n\
import jinja2\n\
\n\
random.seed(os.environ["SEED"])\n\
try:\n\
    import build\n\
    b = build.Builder()\n\
except e:\n\
    print(e)\n\
    b = type("", (), {})()\n\
\n\
b.flag = os.environ["FLAG"]\n\
b.flag_format = os.environ["FLAG_FORMAT"]\n\
\n\
if hasattr(b, "prebuild"):\n\
    b.prebuild()\n\
print(b.flag)\n\
cflags = ["-DFLAG=%%s" %% b.flag, "-DFLAG_FORMAT=%%s" %% b.flag_format]\n\
if hasattr(b, "x86_64") and not b.x86_64:\n\
    cflags.append("-m32")\n\
\n\
if hasattr(b, "executable_stack") and b.executable_stack:\n\
    cflags.append("-zexecstack")\n\
\n\
if hasattr(b, "stack_guards") and not b.stack_guards:\n\
    cflags.append("-fno-stack-protector")\n\
    cflags.append("-D_FORTIFY_SOURCE=0")\n\
else:\n\
    cflags.append("-D_FORTIFY_SOURCE=2")\n\
    cflags.append("-fstack-clash-protection")\n\
    cflags.append("-fstack-protector-strong")\n\
\n\
if hasattr(b, "strip") and b.strip:\n\
    cflags.append("-s")\n\
\n\
if hasattr(b, "debug") and b.debug:\n\
    cflags.append("-g")\n\
\n\
if hasattr(b, "pie") and b.pie:\n\
    cflags.append("-fPIE")\n\
    cflags.append("-pie")\n\
    cflags.append("-Wl,-pie")\n\
\n\
cflags = " ".join(cflags)\n\
if hasattr(b, "extra_flags"):\n\
    cflags = cflags + " " + " ".join(b.extra_flags)\n\
\n\
os.environ["ASFLAGS"] = cflags\n\
os.environ["CFLAGS"] = cflags\n\
os.environ["CXXFLAGS"] = cflags\n\
\n\
if hasattr(b, "aslr") and not b.aslr:\n\
    with open("no-aslr", "w") as f:\n\
        f.write("no-aslr")\n\
\n\
if not hasattr(b, "dont_template"):\n\
    b.dont_template = []\n\
b.dont_template.append("problem.md")\n\
b.dont_template.append("problem.json")\n\
\n\
for curr_dir, sub_dirs, files in os.walk("."):\n\
    if curr_dir in b.dont_template:\n\
        continue\n\
    for fname in files:\n\
        fpath = os.path.join(curr_dir, fname)\n\
        if fpath[2:] in b.dont_template:\n\
            continue\n\
        print(fpath)\n\
\n\
        try:\n\
            with open(fpath) as f:\n\
                contents = f.read()\n\
        except:\n\
            continue\n\
\n\
        template = jinja2.Template(contents)\n\
\n\
        with open(fpath, "w") as f:\n\
            f.write(template.render(b.__dict__))\n\
\n\
if hasattr(b, "build"):\n\
    b.build()\n\
else:\n\
    if hasattr(b, "program_name"):\n\
        os.system("make %%s" %% b.program_name)\n\
    else:\n\
        os.system("make")\n\
\n\
if not hasattr(b, "program_name"):\n\
    b.program_name = "main"\n\
\n\
if not hasattr(b, "exec"):\n\
    b.exec = "./" + b.program_name\n\
if hasattr(b, "aslr") and not b.aslr:\n\
    b.exec = "setarch -R " + b.exec\n\
\n\
with open("start.sh", "w") as f:\n\
    f.write("#!/bin/bash\\n%%s\\n" %% b.exec)\n\
os.chmod("start.sh", stat.S_IRWXU | stat.S_IXGRP | stat.S_IXOTH)\n\
\n\
if hasattr(b, "artifacts"):\n\
    os.system("tar czvf /challenge/artifacts.tar.gz " + " ".join(b.artifacts))\n\
\n\
if hasattr(b, "postbuild"):\n\
    b.postbuild()\n\
\n\
if not hasattr(b, "lookups"):\n\
    b.lookups = {"flag": b.flag}\n\
elif "flag" not in b.lookups:\n\
    b.lookups["flag"] = b.flag\n\
\n\
with open("/challenge/metadata.json", "w") as f:\n\
    f.write(json.dumps(b.lookups))\n\
\n\
if hasattr(b, "remove"):\n\
    for f in b.remove:\n\
        os.remove(f)\n\
' | python3

RUN chmod +x start.sh
