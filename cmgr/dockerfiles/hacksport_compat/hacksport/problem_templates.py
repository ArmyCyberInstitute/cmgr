"""High-level challenge templates compatible with picoCTF hacksport v19.2.10.

Adapted from the MIT-licensed picoCTF source at commit
fb09fa2cb745c2db007dc4be8f95e37a1788c830. See LICENSE.picoCTF.
"""

import os

from hacksport.problem import Compiled, File, ProtectedFile, Remote


def CompiledBinary(
    makefile=None,
    compiler="gcc",
    sources=None,
    binary_name=None,
    is_32_bit=True,
    executable_stack=True,
    no_stack_protector=True,
    aslr=False,
    compiler_flags=None,
    flag_file=None,
    static_flag=None,
    share_source=False,
    remote=False,
    no_pie=True,
):
    sources = [] if sources is None else list(sources)
    compiler_flags = [] if compiler_flags is None else list(compiler_flags)

    if is_32_bit and "-m32" not in compiler_flags:
        compiler_flags.append("-m32")
    if executable_stack and "-zexecstack" not in compiler_flags:
        compiler_flags.append("-zexecstack")
    if no_stack_protector and "-fno-stack-protector" not in compiler_flags:
        compiler_flags.append("-fno-stack-protector")
    if no_stack_protector and "-D_FORTIFY_SOURCE=0" not in compiler_flags:
        compiler_flags.append("-D_FORTIFY_SOURCE=0")
    if no_pie and "-no-pie" not in compiler_flags:
        compiler_flags.append("-no-pie")

    if makefile is None and not sources:
        raise ValueError("You must provide either a makefile or a sources list")
    if makefile is not None and binary_name is None:
        raise ValueError("You must provide the binary name if you use a makefile")
    if flag_file is None:
        flag_file = "flag.txt"

    base_classes = [Compiled]
    if remote:
        base_classes.append(Remote)

    class Problem(*base_classes):
        files = [File(source) for source in sources] if share_source else []
        remove_aslr = not aslr
        program_name = (
            binary_name if binary_name is not None else os.path.splitext(sources[0])[0]
        )

        def __init__(self):
            self.makefile = makefile
            self.compiler = compiler
            self.compiler_sources = list(sources)
            self.compiler_flags = list(compiler_flags)
            self.files = list(type(self).files)
            if not os.path.isfile(flag_file):
                with open(flag_file, "w") as flag_output:
                    flag_output.write("{{flag}}\n")
            if static_flag is not None:
                self.generate_flag = lambda _random: static_flag
            self.files.append(ProtectedFile(flag_file))

    return Problem
