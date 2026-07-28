"""Compatibility API for picoCTF hacksport release v19.2.10.

This module is an adaptation for cmgr of the MIT-licensed picoCTF shell API at
commit fb09fa2cb745c2db007dc4be8f95e37a1788c830. See LICENSE.picoCTF.
"""

from hacksport.problem import (
    Challenge,
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
    WebService,
    files_from_directory,
)

__all__ = [
    "Challenge",
    "Compiled",
    "Directory",
    "ExecutableFile",
    "File",
    "FlaskApp",
    "GroupWriteDirectory",
    "PHPApp",
    "PreTemplatedFile",
    "ProtectedFile",
    "Remote",
    "Service",
    "WebService",
    "files_from_directory",
]
