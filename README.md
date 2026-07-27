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
wget https://github.com/ArmyCyberInstitute/cmgr/releases/latest/download/cmgr_`uname -s | tr '[:upper:]' '[:lower:]'`_amd64.tar.gz
tar xzvf examples.tar.gz
cd examples
tar xzvf ../cmgr_`uname -s | tr '[:upper:]' '[:lower:]'`_amd64.tar.gz
./cmgr update
CMGR_LOGGING=info ./cmgr test --require-solve
```

**NOTE:** If you are running this on an ARM-based computer, you will need to change `amd64` in the cmgr tarball to `arm64`.

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

- *CMGR\_LOGGING*: logging verbosity for command clients (defaults to
'disabled' for `cmgr` and 'warn' for `cmgrd`; valid options are `debug`,
`info`, `warn`, `error`, and `disabled`)

- *CMGR\_INTERFACE*: the host interface/address to which published challenge
ports should be bound (defaults to '0.0.0.0') (_Note_: if the specified
address is not bound to the host running the Docker daemon, this value gets
silently ignored by Docker and the exposed ports will be bound to the loopback
interface.)

- *CMGR\_PORTS*: the range of ports that are dedicated for serving challenges;
cmgr will assume that it fully owns these ports and nothing else will try
to use them (i.e., not in ephemeral range or overlapping with a service
running on the host); format is '1000-1000'.  Ephemeral ports on a Linux host
can be enumerated with `cat /proc/sys/net/ipv4/ip_local_port_range` and adjusted
with `sysctl`.  Some programs (e.g., `docker`) will need to be restarted after
adjusting the kernel parameter.

- *CMGR\_ENABLE\_DISK\_QUOTAS*: enables the [disk
  quota](examples/markdown_challenges.md#challenge-options) container option when set. Disk quotas
  are only functional when using the `overlay2` Docker storage driver and
  [pquota-enabled](https://access.redhat.com/documentation/en-us/red_hat_enterprise_linux/7/html/storage_administration_guide/xfsquota)
  XFS backing storage. Otherwise, the creation of containers with disk quotas will fail at runtime.
  When unset, any specified quotas are ignored.

Additionally, we rely on the Docker SDK's ability to self-configure base off
environment variables.  The documentation for those variables can be found at
[https://docs.docker.com/engine/reference/commandline/cli/](https://docs.docker.com/engine/reference/commandline/cli/).

### Seccomp OCI interceptor

Most deployments do not need additional seccomp setup: when a challenge omits
the `seccomp` option, Docker applies its current default profile directly.
Legacy and complete challenge-provided profiles also use Docker's normal
`security-opt` support.

Named seccomp `tweaks` require the `cmgr-oci-interceptor` binary to be installed
on the Linux host running the Docker daemon. The interceptor receives the OCI
configuration after Docker has expanded its current seccomp default, applies
the requested narrow change, removes cmgr's control value from the container
environment, and then invokes `runc`.

Install the binary from the cmgr release archive in a root-owned executable
directory on the Docker host:

```sh
sudo install -o root -g root -m 0755 cmgr-oci-interceptor /usr/local/bin/cmgr-oci-interceptor
```

Then have the interceptor safely merge itself into Docker's configuration and
reload the daemon:

```sh
sudo cmgr-oci-interceptor register
```

The resulting entry in `/etc/docker/daemon.json` is equivalent to:

```json
{
  "runtimes": {
    "cmgr-oci-interceptor": {
      "path": "/usr/local/bin/cmgr-oci-interceptor",
      "runtimeArgs": [
        "--cmgr-interceptor-protocol=seccomp-v1",
        "--cmgr-runtime-path=/usr/bin/runc"
      ]
    }
  }
}
```

The exact paths depend on the host. By default, the command resolves canonical
absolute paths for both the invoked `cmgr-oci-interceptor` executable and
`runc`. Use `--runtime-path=/absolute/path/to/cmgr-oci-interceptor` or
`--runc-path=/absolute/path/to/runc` to select different installed
executables explicitly. Other options allow an alternate `--config` path,
replacing a conflicting registration with `--force`, or deferring the Docker
reload with `--no-reload`.

For the system Docker configuration, registration rejects executables or
parent directories that are not root-owned or are group- or world-writable.
It updates `daemon.json` atomically while holding a registration lock, reloads
Docker, and verifies that the daemon reports the exact path and protocol
arguments. When the installed `dockerd` supports configuration validation, the
command also validates the merged file before reload. If validation, reload,
or verification fails, it restores the previous configuration; after a reload
attempt, it reloads the restored configuration as well.

For a remote Docker daemon, install both binaries and run the registration
command on the daemon host, not merely on the Docker client machine.

At launch, cmgr warns if the named runtime is not registered and prints the
registration command. This does not prevent challenges without tweaks from
running. cmgr selects the runtime only for challenge containers with a
`seccomp.tweaks` setting and fails rather than silently omitting a requested
tweak if the runtime is unavailable. Builder, artifact, and solver containers
continue to use Docker's default runtime.

The interceptor design is adapted from
[`picoCTF/oci-interceptor`](https://github.com/picoCTF/oci-interceptor), used
under the Apache License 2.0. The specific source attribution is recorded in
the interceptor package and the project `NOTICE`.

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

Back-end development requires Go 1.25 or newer; releases are built with Go
1.26. The SQLite driver is pure Go, so a C toolchain is not required. cmgr's
supported Docker daemon floor is Engine 25. To get started, run:

```sh
git clone https://github.com/ArmyCyberInstitute/cmgr
cd cmgr
go mod download
go mod verify
mkdir bin
go build -trimpath -o bin/ ./cmd/...
go test -v ./...
```

## Generative AI Notice

Please note that the release starting at v0.14.0 was 
modernized with the assistance of generative AI. Each commit message
in which generative AI describes the model, harness, and configuration
used in the process. Additionally, generative AI is used to run deterministic
regression tests across a suite of challenges.

Specificially in v0.14.0, generative AI enabled a modification to 
support custom seccomp profiles per individual container and to
default to the docker runtime seccomp profile. Further, this release
features a version bump to 2026 across libraries and example challenges.

## Acknowledgments

This project is heavily inspired by the
[picoCTF](https://github.com/picoCTF/picoCTF) platform and seeks to be a next
generation implementation of the _hacksport_ back-end for CTF platforms built
in its style.

## Contributing

Please carefully read the [NOTICE](NOTICE), [CONTRIBUTING](CONTRIBUTING.md),
[DISCLAIMER](DISCLAIMER.md), and [LICENSE](LICENSE) files for details on how
to contribute as well as the copyright and licensing situations when
contributing to the project.
