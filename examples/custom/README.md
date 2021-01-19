# `custom` Challenge Type

This challenge type allows the most flexibility for challenge authors at the
expense of providing the least amount of supporting framework.  Aside from the
`problem.json` or `problem.md` file, the only requirement is that the author
provides a `Dockerfile` and any associated files to build their challenge
using Docker.

## Added Behavior for Docker files

In order to make challenge types as reusable as possible, `cmgr` adds some
additional concepts to a normal `Dockerfile` that need to be considered when
building the `Dockerfile` for a custom challenge.

### Build stage named 'builder'

If any stage in the Dockerfile is labeled `builder`, then that staged
(rather thand the last stage) must contain the `/challenge/metadata.json`
and, if applicable, `/challenge/artifacts.tar.gz` files.

### Publishing ports

Docker has a distinction between "exposed" ports and "published" ports.  To
avoid repetitive boilerplate in `problem.md` files, `cmgr` detects which
exposed ports should be published by requiring a comment of the form
`# PUBLISH {port} AS {name}` (case sensitive) to occur in the Dockerfile after
the `EXPOSE` directive.  This allows challenge authors to bring in base images
that already expose ports in Docker (e.g., the PostgreSQL image) without
requiring that the port be directly exposed to the competitor.

### Launching more than one container

In order to support challenges that launch multiple containers for a
challenge, `cmgr` introduces a comment of the form `# LAUNCH {build_stage} ...`
which will launch an instance of each listed stage with the stage name as
its Docker DNS name and place them on their own internal network.  For a
specific example of this, see the [multi example](../multi).  When using
multiple containers, it is important that each `PUBLISH` directive (above)
appears in the same build stage as the `EXPOSE` directive it is referencing.

## Docker Build Arguments

At build time, the Dockerfile will receive three arguments: `FLAG_FORMAT`,
`SEED`, and `FLAG`.  Any randomized component of the build process should
ensure that it uses a static derivation of `SEED` (a decimal number in string
format) in order to allow reproducible builds of their challenges.  The `FLAG`
is a recommended flag to ease the authors development process, and
`FLAG_FORMAT` is the requested flag format if the author does not use the
provided flag for some reason.  It is not required to follow `FLAG_FORMAT`,
but it is highly recommended as it makes the challenge more easily integrated
into events.

## Requirements

During the build phase of creating the associated Docker image, the challenge
author is responsible for creating two files:  `/challenge/metadata.json` and
`/challenge/artifacts.tar.gz`.

The `metadata.json` file is mandatory and consists of a single JSON object
which must contain a field called "flag" which provides the flag that the
challenge will produce.  If the challenge description requires additional
templating information such as a username and password that is generated at
build time, then these should be additional fields and string values in
`metadata.json`.

The `artifact.tar.gz` file contains all artifaction that should be presented
to competitors for them to download, and they must be packaged directly into
the archive and not in a subdirectory.  Expanding this archive should place
the artifacts in the same directory from which `tar` was invoked.

In addition to the two files, any ports that should be directly exposed to
competitors should have a corresponding comment line in the form of `# PUBLISH
{port} AS {name}` in the Dockerfile in order for the engine to identify as
those as intended for public visibility.
