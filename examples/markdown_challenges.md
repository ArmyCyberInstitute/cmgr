# Markdown Challenge Specification

- Namespace: cmgr/examples
- Type: flask
- Category: example
- Points: 1
- Templatable: yes
- MaxUsers: 0

## Description

This is a static description of the challenge that is intended to be shown to
the user and will be the same across all instances of the challenge.

## Details

This is templated information for the challenge that can use additional
build-specific information to present information.  In particular, the following
templates are allowed (anything else is invalid):
- `{{url_for("file", "display text")}}`
- `{{http_base("port_name")}}` (URL prefix for HTTP requests to the named port)
- `{{port("port_name")}}` (The specific port number competitors will see which
may not be the same number as exposed by Docker if the front-end is proxying
connections.)
- `{{server("port_name")}}` (hostname which hosts for connecting to the
associated port for the challenge)
- `{{lookup("key")}}` ("key" must have been published in `metadata.json` when creating a build)
- `{{link("port_name", "/url/in/challenge")}}` (convenience wrapper for generating an HTML link)
- `{{link_as("port_name", "/url/in/challenge", "display text")}}` (convenience
wrapper for generating an HTML link with text different from the URL)

**Note:** As a convenience, `port_name` can be omitted any time the challenge only
publishes a single port to competitors.  For templates that only take
`port_name` as an argument, the parentheses should be omitted when using this
convenience.  The template strings exposed to front-ends will always be
normalized to include `port_name` and have replaced `link` and `linkAs` with
an HTML href tag that uses `http_base`.

## Hints

- A list of hints for the end user.
- The hints are all templatable.
- Whether there is a cost for displaying them is up to the front-end system

## Tags

- example
- markdown

## Attributes

- Organization: ACI
- Created: 2020-06-24

## Challenge Options

This optional section can be used to apply additional restrictions to instances of this challenge
via a ```` ```yaml```` fenced code block. The available options are listed below, along with an
example.

For [multi-container](./custom/README.md) challenges, by default any specified options apply to all
containers. However, it is possible to specify separate options for each host (build stage) via an
`overrides:` key, as seen in [this example](./multi/problem.md). Note that when an override is
specified, it serves as a fully distinct set of challenge options for that container and will not be
merged with any specified top-level options.

Container options are never applied to the ["builder"](./custom/README.md) stage or to solver
containers.

- The `init` option runs an init process as PID 1 inside the container. This can be useful if your
  challenge process forks, and will ensure that zombie processes are reaped. This is equivalent to
  passing the [`--init`](https://docs.docker.com/engine/reference/run/#specify-an-init-process) flag
  to `docker run`. Specify a boolean value, as shown in the example below. Defaults to `false`.

- The `cpus` option specifies a maximum number of CPU cores that a container can utilize at full
  capacity. This may be useful in order to prevent computationally-heavy challenge instances from
  dominating the host. This is equivalent to passing the
  [`--cpus`](https://docs.docker.com/engine/reference/run/#cpu-period-constraint) option to `docker
  run`. Specify a floating-point value, as shown in the example below. Unset by default.

- The `memory` option specifies the maximum amount of memory available to a container. Attempting to
  exceed this limit at runtime may cause the container to restart, depending on how the challenge
  process handles allocation failures. This is useful in order to put an upper bound on the memory
  available to each challenge instance, preventing memory leaks from crashing the Docker host. This
  is equivalent to passing the
  [`--memory`](https://docs.docker.com/engine/reference/run/#user-memory-constraints) option to
  `docker run`. Specify an integer value with unit, as shown in the example below. Unset by default.

- The `ulimits` option can be used to specify various [resource
  limits](https://access.redhat.com/solutions/61334) inside the container. Note that the `nproc`
  ulimit is not supported, for reasons described
  [here](https://docs.docker.com/engine/reference/commandline/run/#for-nproc-usage) (use the
  `pidslimit` option instead). This is equivalent to passing
  [`--ulimit`](https://docs.docker.com/engine/reference/commandline/run/#set-ulimits-in-container---ulimit)
  options to `docker run`. Specify a list of limit names and limits, as shown in the example below.
  Unset by default.

- The `pidslimit` option specifies the maximum number of simultaneous processes inside the
  container. This is useful in order to prevent forkbombs from crashing the Docker host. This is
  equivalent to passing the
  [`--pids-limit`](https://docs.docker.com/engine/reference/commandline/run/) option to `docker
  run`. Specify an integer value, as shown in the example below. Unset by default.

- The `readonlyrootfs` option can be used to mount the container's root filesystem as read-only. If
  your challenge does not need to write to disk outside of `/dev/shm`, this is an easy way to
  improve the security of your challenge containers. This is equivalent to passing the
  [`--read-only`](https://docs.docker.com/engine/reference/commandline/run/) flag to `docker run`.
  Specify a boolean value, as shown in the example below. Defaults to `false`.

- The `droppedcaps` option can be used to drop additional Linux capabilities inside the container
  beyond Docker's
  [defaults](https://docs.docker.com/engine/reference/run/#runtime-privilege-and-linux-capabilities).
  This is equivalent to passing
  [`--cap-drop`](https://docs.docker.com/engine/reference/run/#runtime-privilege-and-linux-capabilities)
  options to `docker run`. Specify a list of uppercase capability names, as shown in the example
  below. Unset by default.

- The `nonewprivileges` option can be used to
  [prevent](https://www.kernel.org/doc/html/latest/userspace-api/no_new_privs.html) processes inside
  the container from gaining additional privileges via `execve()` calls (by exploiting setuid
  binaries, etc). This is equivalent to passing the
  [`--security-opt="no-new-privileges:true"`](https://docs.docker.com/engine/reference/run/#security-configuration)
  option to `docker run`. Specify a boolean value, as shown in the example below. Defaults to
  `false`.

- The `diskquota` option can be used to limit the maximum size of the container's writable layer.
  This is equivalent to passing the [`--storage-opt
  size`](https://docs.docker.com/engine/reference/commandline/run/#set-storage-driver-options-per-container)
  option to `docker run`.

  Note that this option is **only supported** when using the `overlay2` Docker storage driver and
  [pquota-enabled](https://access.redhat.com/documentation/en-us/red_hat_enterprise_linux/7/html/storage_administration_guide/xfsquota)
  XFS backing storage (see this [Docker Engine PR](https://github.com/moby/moby/pull/24771) for more
  details.) If these requirements are not met, container creation will fail at runtime.

  To help prevent this issue, the `diskquota` option only takes effect if the
  `CMGR_ENABLE_DISK_QUOTAS` environment variable is set.

  Specify an integer value with unit, as shown in the example below. Unset by default.

- The `cgroupparent` option can be used to manually specify the cgroup that a container will run in.
  This is equivalent to passing the
  [`--cgroup-parent`](https://docs.docker.com/engine/reference/run/#specify-custom-cgroups) flag to
  `docker run`.

  Note that it is also possible to set a default parent cgroup for all containers at the [daemon
  level](https://docs.docker.com/engine/reference/commandline/dockerd/#default-cgroup-parent).

  Specify a cgroup name, as shown in the example below. Unset by default.

```yaml
# sample challenge options:
init: true
cpus: 0.5
memory: 512m
ulimits:
    - nofile=512:1024
    - stack=4096
    - fsize=2048
pidslimit: 5
readonlyrootfs: true
droppedcaps:
    - CHOWN
    - SETPCAP
    - SETUID
nonewprivileges: true
diskquota: 256m
cgroupparent: customcgroup.slice

# only relevant for multi-container challenges:
overrides:
    work:
        pidslimit: 10
    randomDnsName:
        cpus: 0.25
```

## Extra Sections

Any `h2` sections (i.e. lines starting with `##`) that don't match one of the
headers above (not including "Extra Sections") will be parsed and added as
additional attributes where the header text is the key and the value is raw
text (i.e. no Markdown conversions) up to but not including the next `h2`
header.  Whitespace at the start and end of this block of text is stripped,
but all other whitespace is preserved exactly as written.

## Mandatory Sections

There are only a few mandatory parts of this structure: the title line which
is interpreted as the challenge name, the "type" entry (must be a list bullet
in the block immediately following the title), and at least one templated
reference to each artifact file and port exposed to the competitor (most
likely in the "details" section).  Although not required, the "namespace"
entry is highly encouraged as it minimizes the likelihood of naming conflicts
if challenges are released and/or merged with other sources.

## Renaming Challenges

Challenge IDs are usually determined by sanitizing the user-facing challenge name
and prepending the provided namespace (if any).

However, this means that changing a challenge's name and running `cmgr update` will be
interpreted as removing the formerly-named challenge and adding a new one. This can be problematic
when challenges have existing references to their former IDs in schemas or front-end software.

To avoid this issue, it is possible to specify an ID separately from the user-facing name
by adding an "ID" list bullet to the block immediately following the title. When specified, the
value of this ID field, rather than the challenge's name, is sanitized and prepended with the
namespace to determine the challenge ID.

This makes it possible to update the user-facing name of a deployed challenge without
affecting existing schema or front-end references by explicitly specifying the challenge's
former name as its ID when changing its name.
