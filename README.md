# ~Hacksport 2.0~ Challenge Management Interface

## Overview

**Note:** This document is currently focused primarily on the front-end API of
the system.  As we refactor the challenge developer API, we will start
incorporating more of that documentation into this repository and probably
split the documentation into multiple files.  At the moment, the goal is
modify as little of the challenge-facing API as possible to allow maximum
portability of legacy challenges.

This is designed to be an overhaul of the `shell_manager` from "hacksport" in
picoCTF.  The project consists of three main components: a Python library
(cmgrlib), a CLI (cmgr), and a REST API service (cmgrd).  These can be used to
manage the life-cycle of CTF challenges both during development as well as
during events such as competitions or open-ended training sessions.

### Design Goals

- Streamline challenge development with a useful, standalone CLI tool

- Provide a simple interface for adding automated solvers to facilitate use of
CI/CD in content development

- Simplify challenge management by discovering challenges in a folder rather
than explicitly adding each challenge individually

- Every challenge exists in a container (no cross-contamination of system)

- Provide easy on-ramp for legacy hacksport content.

- Maintain a front-end agnostic challenge server.  A corollary to this is
clearly defining the separation of concerns between challenge management and
the display of information in an event.

### Design Non-Goals

- Directly support user shells.  This caused significant security challenges
on the old "shell-server" concept.  If this functionality is needed, then it
should exist on a dedicated server or inside containers (could be hacked into
this framework via a "challenge" that provides `wetty` and `ssh` ports).

- Full backwards compatibility hacksport.  The previous version tightly
coupled low-level  build directives with the challenge orchestration.  This
tries to separate the two concepts with build directives being covered by a
Dockerfile.  Backwards compatibility is achieved by templating out a Dockerfile
that shims existing challenges into a containerized build using the old
hacksport library.

- Handle authentication on the REST API.  This is easily (and probably more
securely) achieved through the use of a reverse proxy such as _nginx_ and/or network security groups and firewall policies.

### Description Templating

To maximize deployment flexibility, the challenge server does not perform
templating on challenge details and hints and instead defers that to the web
server while providing all of the necessary information to construct a
response.

The primary reason for this decision, it enables far more flexibility on how
to serve content such as allowing proxying of the dynamic websites and ports
or remote hosting of the downloadable artifacts.  In particular, this easily
allows a web front-end to host static artifacts directly and therefore
restrict downloads to authenticated users assigned to that instance.  It also
enables exploration of adding authentication mechanisms to instance connection
through authenticated proxying (for web challenges) and dynamic port mappings
(for other network challenges).  These mechanisms could potentially reduce the
attack surface of hosting a competition while also providing possible hook
points for improving metrics collection for research purposes.

## Library API

### Configuration

Basic concept is to use environment variables with sane defaults to control
basic implementation details.

`CMGR_DIR` path to the directory which contains all known challenges
(nested arbitrarily).

`CMGR_ARTIFACT_DIR` path to a writable directory for storing artifact tarballs.

`CMGR_DB` path to the SQLite database file storing state
(handles concurrency by enforcing table locks before file modifications)

**Note:** There are a number of docker configuration options that need to be added.

### Metadata

#### Challenge Metadata

Authoritative:

- Name

- Namespace (optional, '/' separated)

- challenge identifier (sanitized name + hash of relative file-path from `PICO_CHALLENGE_DIR` for "problem.json")

- Description (static information only)

- Details template (called "Description" in current hacksport)

- Hints template

- Version (root hash of Merkle tree of source material)

- HasSolveScript

Informational:

- Templatable (do multiple "builds" make sense for a single event - e.g. does
it have a static flag)

- max-users (0 = no concerns, 1 = instance-per-user, 2+ = recommendation on
limit)

- Category

- Points

- Tags

- Attributes

    - LearningObjective

    - DesignOverview

    - Organization

    - Author

#### Build Metadata

- flag

- seed

- last-checked (date-time) [last auto-solve of any instance for this build]

#### Instance Metadata

- Port information (dictionary of port "name" to port on challenge server)

- last-checked (date-time)

#### Templating Details

In order to keep the front-end's requirements well-defined, the only allowed
templating directives are `url_for(filename, display=None)`,
`port(name=None)`, `http_base`, `hostname`, and `lookup(key_string)`.  The
front-end is responsible for providing the `http_base` and `hostname` string
definitions as well as function definitions for `url_for`, `port`, and
`lookup` while the challenge server is responsible for providing a dictionary
of "names" to ports and a dictionary of "keys" to "values" via the instance's
metadata.  The templates provided by the challenge server are Jinja2 HTML
templates.

### Library Functions (cmgrlib)

#### `detect_changes(challenges)`

`challenges` is a list of challenge identifiers for known challenges

Performs basic change detection on the challenges listed and reports back all
changes that _would_ be made by `update()`.  If given the empty list
(default), it crawls the entire challenge directory, updating everything as
necessary.

Returns a list of errors that occurred during validation of challenge
directories (empty list if successful)

#### `update(challenges)`

`challenges` is a list of challenge identifiers for known challenges

Performs basic change detection on the challenges listed and updates any
existing state (builds or deploys) if necessary.  If given the empty list
(default), it crawls the entire challenge directory, updating everything as
necessary.

Returns a list of errors that occurred during deployment (empty list if
successful)

#### `build(challenge, seeds, flag_format="flag{%s}")`

`challenge` is the challenge identifier of a known challenge

`seeds` is a list of byte strings to use as the seed for randomness.  If an
empty list, then the instance names are used as the seeds instead.

Builds the docker image(s), artifact tarball, and flag for the challenge using
the given seeds for randomness.

Throws an exception on an error.

Returns a list of `build_id`s (opaque integers).

#### `start(build_id)`

`build_id` is the identifier for the build returned by `build()`

Throws an error if challenge not already built or if errors occur during
deployment process (should be very rare).

Returns a list of `instance_id`s (opaque integers).

#### `stop(instance_ids)`

`instance_ids` is a list of valid instance identifiers.

Terminates any active processes (i.e. docker containers) associated with the
instance.

No return value.

#### `destroy(build_ids)`

`build_ids` is a list of build identifiers as returned by `build`

Will delete the artifacts and docker images for all indicated instances
(freeing up disk space on challenge server).

Throws an error if any instances marked for destruction are currently running.

No return value.

#### `check_instances(instance_id)`

Creates and runs a docker container in a new directory with the static
artifact files as well as the instance metadata (without flag) saved in
`metadata.json`.  The container should output a single line with the flag on
stdout which the function then compares against the known flag.

Throws an error if there is no solve script.

Returns a list of `true`, `false` corresponding to the solve state
for each instance.

#### `get_challenge_metadata(challenges)`

`challenges` is a list of challenge identifiers for known challenges.  If empty, then all
challenges are selected.

Throws an error if any challenge identifiers are invalid.

Returns a list of the raw, untemplated challenge metadata.

#### `get_build_metadata(build_ids)`

`build_ids` is the build identifier being queried

Throws an error if the identifiers are invalid.

Returns a list of associated static metadata in the same order as passed.

#### `get_instance_metadata(build_id, instance_ids)`

`build_id` is the build identifier being queried

`instance_ids` are valid instance identifiers

Throws an error if any identifiers are invalid.

Returns a list of associated instance metadata in the same order as passed.

#### `dump_challenge_state(challenges)`

**Note:** This function is intended for administration/monitoring of the
challenge system as a whole and not for routine challenge management.

`challenges` is a list of challenge identifiers for known challenges.  If empty, then all
challenges are selected.

Throws an error if any challenge challenge identifiers are invalid.

Returns a list of challenge state.  Each state is a map of known `build_id`s
to their static metadata as well as a map of their associated `instance_id`s
to their instance metadata.

### Database Schema

See `schemaQuery` in [database.go](cmgr/database.go).

## Command Line Utility (`cmgr`)

### `cmgr list [--verbose]`

Lists the identifiers of all known challenges.  Verbose will include their unsanitized name in the output.

### `cmgr info <challenge_path>`

Pretty prints the challenge metadata for all challenges whose definitions start with that path.

### `cmgr update [--verbose] [--dry-run] [<challenge>...]`

Wrapper around `update`.  By default it will only print the names of
challenges that have changed in status (new, updated, or deleted).  With
verbose, it will print the name of every challenge as it processes it and
print the name of every instance as it rebuilds or redeploys it.  With
"dry-run", it will report back the planned changes without actually executing.

### `cmgr build <challenge_id> [--flag-format=<format string>] <seed>[,<seed>...]`

### `cmgr start <build_id>`

### `cmgr check <instance_id> [<instance_id>...]`

### `cmgr stop <instance_id> [<instance_id>...]`

### `cmgr destroy <build_id> [<build_id>...]`

### `cmgr reset [--verbose]`

Stops all known instances and then destroys all builds.

### `cmgr test <challenge_path> [--no-solve|--require-solve]`

Short-cut for running update, build, start, solve, stop, and destroy in
sequence (breaking the sequence and printing the relevant IDs on the first
error).  If `--no-solve` is used, then the command only goes through `start`
and prints the `build_id` and `instance_id` to stdout to allow the developer
to interact with the instance.  If `--require-solve` is used, then missing
solve scripts are treated as solve failures.

### `cmgr system-dump [--verbose] [--quiet] [--json] [<challenge>...]`

By default, will pretty-print all known challenges (or just the ones
indicated) with their built static IDs and running instances.  `--verbose`
will expand it to include all metadata while `--quiet` will consolidate it to
just show every known challenge annotated with a count of static builds and a
total count of running instances.

## REST API Service (`cmgrd`)

### `/challenges`

#### GET

Returns a JSON list of challenge challenge identifiers that are known to the server.

### `/challenges/<challenge>`

#### GET

Calls `get_challenge_metadata()` on the passed challenge name

#### POST

Takes a JSON with a `seeds` fields which is a list strings which and an optional `flag_format` field which are passed to the `build()` library call.
Returns a JSON dictionary mapping the new `build_id`s to their metadata.

### `/builds/<build_id>`

#### GET

Returns the results of `get_build_metadata()`

#### POST

Starts a new instance of the build.  Returns new instance's metadata.

#### DELETE

Destroys the build.

### `/builds/<build_id>/artifacts.tar.gz`

#### GET

Returns tarball of static artifacts (not in a subdirectory) for the build ID.

### `/instances/<instance_id>`

#### GET

Returns the results of `get_instance_metadata()`

#### POST

Runs the automated solve checker against the instance.

#### DELETE

Stops the instance.
