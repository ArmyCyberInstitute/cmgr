# picoCTF hacksport example

## Overview

cmgr includes a Python 3 compatibility implementation of the picoCTF
v19.2.10 *hacksport* APIs and build lifecycle. It supports the common challenge,
compiled, service, remote, Flask, PHP, templating, artifact, and dependency
interfaces without exposing the Docker daemon to challenge builds.

Challenges that directly use `DockerChallenge`, Python 2-only dependencies, or
custom host deployment operations need to be converted to a native `custom`
challenge or explicitly use the separately built `hacksport-legacy` image.
New challenges should generally use native cmgr challenge types.

## Example Details

This is an example challenge from the [picoCTF][] repository.

[picoCTF]:https://github.com/picoCTF/picoCTF/tree/master/problems/examples/cryptography/ecb-1

This demonstrates the ability to support existing content with no or minor
modification. Its abandoned `pycrypto` dependency is mapped explicitly to
PyCryptodome; the example passes bytes to the modern cipher API so the
substitution remains intentional and testable.
