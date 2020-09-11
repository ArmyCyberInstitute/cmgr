# Challenges

## Supported Specification Formats

As an evolution on the original `hacksport` library, we provide two formats for specifying a challenge:  JSON and Markdown.

### problem.json

We attempt to support backwards compatability with the previous `hacksport` JSON format and will use the compatability mode for any `problem.json` file that has a `challenge.py` file in the same directory.  However, these challenge definitions are unable to take full advantage of new challenge types and metadata control.  So it is preferred to use the newer format if you want to use JSON for specifying the metadata.

### problem.md

In an effort to make it easier to develop content, we've implemented a Markdown specification that imposes some restraints on the document layout but should be far easier to read and maintain than the JSON format.  For a detailed breakdown of the new format, see the [specification](markdown_challenges.md).

## Schemas

"Schemas" are a mechanism for declaratively specifying the desired state for a set of builds and instances.  Builds and the associated instances that are created by a schema are locked out from manual control and should be the preferred way to manage a large number of builds and instances for events.  However, they are still event agnostic and can be used for managing other groupings of resources as appropriate.  Two equivalent schema specifications can be found [here](schemas/).  It is worth noting that a `-1` for instance count specifies that instances are still manually controlled and allows the CLI or `cmgrd` to dynamically increase or decrease the number of running instances (useful for mapping instances uniquely to end-users without having a large number of unused containers).

## Supported Challenge Types

One of the primary goals of _cmgr_ is to make it easier to implement new challenge types.  One notable lack of support right now, however, is the inability to mount and manipulate block devices.  This is currently a limitation of the underlying container system (i.e. Docker and containerd).  Unless otherwise specified, challenge types that expose a port have the associated service running as a non-root user inside the container who does not have permission to read `/challenge` (and hence a known location of the flag).

As a general rule, all challenge types (except "custom" and "hacksport") support two mechanisms for installing dependencies.  First, they will look for a `packages.txt` file in the challenge's root directory.  This file should have a single "apt" package per line (versioning not supported) and will be installed before any other code is run.  Additionally, challenges that require/support Python (e.g. "flask") will also look for the standard `requirements.txt` file and install those dependencies with "pip".

If you are interested in the underlying mechanics for a challenge type, all of the associated Dockerfiles can be found [here](../cmgr/dockerfiles/).

### custom

This is by far the most flexible of the challenge types, but also the one with the least amount of structured support.  In addition to the challenge specification file (e.g. `problem.md`), the challenge author must supply a complete Dockerfile.  The Dockerfile will be supplied with three build arguments when it is first invoked: `FLAG`, `SEED`, and `FLAG_FORMAT`.

The Dockerfile is responsible for using these inputs to build the templated challenge and format the image appropriately for _cmgr_ to retrieve the artifacts and build metadata.  In particular, any artifacts competitors should see **must** be in a GZIP-ed tar archive located at `/challenge/artifacts.tar.gz`.  Additionally, there **must** be a `/challenge/metadata.json` file that has a field for the flag (named `flag`) as well as any other lookup values the challenge references in its details and hints.  Finally, if the Dockerfile expects any ports to be exposed directly to end-users, then there must be a comment line of the form `# PUBLISH <port number> AS <port name>` in the Dockerfile.

You can find an example [here](custom/).  The ["multi"](multi/) challenge example demonstrates the full range of customization you can leverage by demonstrating multi-container challenges and custom per-build lookup values.

### flask

This is a simple wrapper around the "flask" web framework designed to require minimal adjustment from standard practice for challenge authors to create new content.  An example with a more detailed readme can be found [here](flask/).

### hacksport

This is a shim around the legacy `hacksport` framework.  It should "just work" for those challenges, but is also likely to be fragile on more complicated ones.  In particular, the "docker" challenges are not supported (but should be easily portable to a new "custom" one) and calls to "mount" are not supported.

### node

This is a simple wrapper around the _Node.js_ framework using their published LTS base image.  An example with more details can be found [here](node/).

### php

This is a simple framework for launching a web server/application built using PHP.  An example with more details can be found [here](php/).

### Compiled Challenges

Many challenges require compilation as part of their build.  To allow maximum flexibility while trying to minimize outside requirements on the build process, we have two different "drivers" for compiled challenges as well as three different styles for how competitors will interact with them.

#### make

The make "driver" is the simplest of the drivers.  The build process will call `make main`, `make artifacts.tar.gz`, and `make metadata.json` in that order to build the challenge and necessary components.  Additionally, challenges with a network component will have `make run` called to start as the entrypoint.

#### pybuild

The `pybuild` driver is an attempt to provide the power of templating (similar to what was available in `hacksport`) while still getting out of the way for the most part.  It allows authors to hook the build process at various points by creating a file named `build.py` which defines a class named `Builder`.  This class will then be referenced during the build process for the challenge.

Probably the most powerful part of this driver is the use of `jinja2` to template the challenge directory into the build image.  In particular, any attribute of the `Builder` is directly available for templating.  If there are files that should not be templated, you can specify them as a list of strings (full filepath from the challenge root to the file) assigned to `self.dont_template`.  Additionally, you can specify files to remove after executing the build (`self.remove`) as well as specify compiler flags for the default target.

There are three functions that can be used to manipulate the build process: `prebuild(self)`, `build(self)`, and `postbuild(self)`.  They run in that order and all are completely optional (if `build` is omitted the build step defaults to shelling out to `make {{program_name}}`).  Of particular note, `prebuild` is called after `self.flag` and `self.flag_format` have been populated and `random` has been seeded with the appropriate seed for the build.  In contrast, `postbuild` is called after `artifacts.tar.gz` has been assembled but before `metadata.json` has been created.

Pre-defined attributes (all over-ridable)
- `flag`: the auto-generated flag for the problem
- `flag_format`: the requested format for what the flag should look like
- `x86_64`: a boolean indicating whether this should be a 64-bit build (true, and the default) or a 32-bit build.
- `executable_stack`: a boolean indicating whether the stack is executable (default is false)
- `stack_guards`: a boolean indicating whether compiler-injected stack-guards should be used (default is true)
- `strip`: a boolean indicating whether the final binary should be stripped (default is false)
- `debug`: a boolean indicating whether DWARF information should be included with the final binary (default is false)
- `pie`: a boolean indicating whether the final binary should be built as a position-independent executable (default is false)
- `extra_flags`: a list of strings which will be appended to the `CFLAGS`, `CXXFLAGS`, and `ASFLAGS` environment variables (and hence override auto-generated flags)
- `dont_template`: a list of filepaths to skip when applying the templating logic
- `program_name`: **REQUIRED if "build" function not defined**: specifies the name of the binary to build.  By default, it will try to use make's implicit build rules to build it by calling `make {{program_name}}` (defaults to "main").
- `exec`: (remote/service only) the command that should be used as the entrypoint for the server (defaults to `./{{program_name}})`
- `artifacts`: The list of files (after "build" step) that should be packaged into `artifacts.tar.gz` (defaults to an empty list)
- `lookups`: a dictionary of string key-value pairs which will be made available to the front-end's templating engine
- `remove`: a list of files to remove prior to starting the server (useful for removing sensitive build files from the build directory)

For debugging purposes, the Python script used to drive this logic is available [here](../support/pybuild.py).

#### remote

The "remote" set of challenge types will take a program that uses stdin/stdout to communicate and connect it to a port so that every new TCP connection gets forked into a new process with stdin/stdout piped to the network.

#### service

The "service" set of challenge types will start the program exactly once and expect it to accept and handle TCP connections completely on its own.  The program is expected to read the "PORT" environment variable to ensure it listens on the correct port (but should be safe to hardcode as 5000).

#### static

The "static" set of challenge types have no network component and should be solvable solely by using the `artifacts.tar.gz` and `metadata.json` created during the build process.
