# picoCTF hacksport example

## Overview

This challenge class serves as a shim for existing *hacksport* compatible
challenges that makes them runnable with `cmgr`.  This shim should generally
work for existing challenges as long as they do neither utilize Docker
directly (i.e. the "Docker" challenge class) nor rely on the ability to mount
disk images (e.g. loopback mounts for creating forensic artifacts).  The shim
uses the *hacksport* library from the 2019 branch of `picoCTF` and will only
be updated to include regression fixes in that library or the shim code.  New
challenges should generally use the native `cmgr` challenge types rather than
`hacksport`.

## Example Details

This is an example challenge from the [picoCTF][] repository.

[picoCTF]:https://github.com/picoCTF/picoCTF/tree/master/problems/examples/cryptography/ecb-1

This demonstrates the ability to support existing content with no, or minor,
modification.
