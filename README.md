# cmgr

**cmgr** is a new backend designed to simplify challenge development and
management for Jeopardy-style CTFs.  It provides a CLI (`cmgr`) intended for
development and managing available challenges on a back-end challenge server
as well as a REST server (`cmgrd`) which exposes the minimal set of commands
necessary for a front-end web interface to leverage it to host a competition
or training platform.

## Quickstart

Assuming you already have Docker installed, the following code snippet will
download example challenges and the **cmgr** binaries, initialize a
database file that tracks the metadata for those challenges, and then run the
test suite to ensure a working system.  The test suite can take several minutes
to run and is not required to start working.  However, running the suite can
identify permissions and other errors and is highly recommended for the first
time you use `cmgr` on a system.

```sh
wget https://github.com/ArmyCyberInstitute/cmgr/releases/latest/download/examples.tar.gz
wget https://github.com/ArmyCyberInstitute/cmgr/releases/latest/download/cmgr.tar.gz
tar xzvf examples.tar.gz
cd examples
tar xzvf ../cmgr.tar.gz
./cmgr update
./cmgr test --require-solve
```

At this point, you can start checking out problems by finding the challenge ID
of one you would like to play and running `./cmgr playtest <challenge>`.  This
will build and start the challenge and run a minimal webserver (`localhost:4200`
by default) that you can use to view and interact with the content.  You could
also launch the REST server on port 4200 with `./cmgrd` or launch all of the
examples from the CLI with `./cmgr test --no-solve` which will launch an
instance of each example challenge and print the associated port information.

## Configuration

**cmgr** is configured using environment variables.  In particular, it
currently uses the following variables:

- *CMGR\_DB*: path to cmgr's database file (defaults to 'cmgr.db')

- *CMGR\_DIR*: directory containing all challenges (defaults to '.')

- *CMGR\_ARTIFACT\_DIR*: directory for storing artifact bundles (defaults to '.')

- *CMGR\_LOGGING*: logging verbosity for command clients (defaults to 'disabled' for `cmgr` and `warn` for `cmgrd`; valid options are `debug`, `info`, `warn`, `error`, and `disabled`)

Additionally, we rely on the Docker SDK's ability to self-configure base off
environment variables.  The documentation for those variables can be found at
[https://docs.docker.com/engine/reference/commandline/cli/](https://docs.docker.com/engine/reference/commandline/cli/).

## Developing...

### Challenges

One of our design goals is to make developing challenges for CTFs as simple as
possible so that developers can focus on the content and not quirks of the
platform.  We have specific challenge types that make it as easy as possible to
create new challenges of a particular flavor, and the documentation for each
type and how to use them are in the [examples](examples/) directory.

Additionally, we have a simple interface for creating automated solvers for
your challenges.  It is as simple as creating a directory named `solver` with
a Python script called `solve.py`.  This script will get its own Docker
container on the same network as the instance it is checking and start with
all of the artifact files and additional information provided to competitors in
its working directory.  Once it solves the challenge, it just needs to write
the flag value to a file named `flag` in its current working directory and
**cmgr** will validate the answer and report it back to the user.

In both the challenge and solver cases, we support challenge authors using
custom Dockerfiles to support creative challenges that go beyond the most
common types of challenges.  In order to support the other automation aspects
of the system, there are some requirements for certain files to be created
during the build phase of the Docker image and are documented in the `custom`
challenge type example.

Testing challenges is meant to be as easy as executing `cmgr test` from the
directory of an individual challenge or the directory containing all of the
challenges for an event.  This is intended to support quick feedback cycles
for developers as well as enabling automated quality control during the
preparation for an event.

### Front-Ends

Another design of this project is to make it easier for custom front-end
interfaces for CTFs to reuse existing content/challenges rather than forcing
organizers to port between systems.  To make this possible, `cmgrd` exposes a
very simple REST API which allows a front-end to manage all of the important
tasks of running a competition or training environment.  The OpenAPI specification
can be found [here](cmd/cmgrd/swagger.yaml).

### Back-End

If you're interested in contributing, modifying, or extending **cmgr**, the
core functionality of the project is implemented in a single Go library under
the `cmgr` directory.  You can view the API documentation on
[go.dev](https://pkg.go.dev/github.com/ArmyCyberInstitute/cmgr/cmgr).
Additionally, the _SQLite3_ database is intended to function as a read-only
API and its schema can be found [here](cmgr/database.go).

In order to work on the back-end, you will need to have _Go_ installed and
_cgo_ enabled for at least the initial build where the _sqlite3_ driver is
built and installed.  To get started, you can run:

```sh
git clone https://github.com/ArmyCyberInstitute/cmgr
cd cmgr
go get -v -t -d ./...
mkdir bin
go build -v -o bin ./...
go test -v ./...
```

## Acknowledgments

This project is heavily inspired by the
[picoCTF](https://github.com/picoCTF/picoCTF) platform and seeks to be a next
generation implementation of the _hacksport_ back-end for CTF platforms built
in its style.

## Contributing

Please carefully read the [NOTICE](Notice), [CONTRIBUTING](CONTRIBUTING.md),
[DISCLAIMER](DISCLAIMER.md), and [LICENSE](LICENSE) files for details on how
to contribute as well as the copyright and licensing situations when
contributing to the project.
