#!/usr/bin/python3
import subprocess

subprocess.run(["/bin/sh","patch_guestfish.sh"])
subprocess.run(["tar","xf","disks.tar.gz"])
subprocess.run(["/usr/bin/guestfish","-f","recover_flag.gfsh"])
