class Builder:
    x86_64 = False
    aslr = False
    pie = False
    program_name = "re101"
    artifacts = [program_name]
    extra_flags = ["-nostdlib", "-static"]