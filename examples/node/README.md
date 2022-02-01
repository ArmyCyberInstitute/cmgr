# `node` Challenge Type

This challenge provides the basic framework for creating web-challenges that
run as a node server.

## Usage

In addition the the challenge description in `problem.json`, challenge authors
need to provide the node application and supporting resources with the
challenge directory as the root.  In particular `package.json` and
`package-lock.json` must both be present because the application is installed
into the container using `npm ci --only=production` prior to running it.
Additionally, the server entrypoint should use the value of `process.env.PORT`
for the port to bind on rather than hard-coding a particular port.

## Automatic Templating

There are two values that `cmgr` will template into every regular file:
`{{flag}}` and `{{seed}}`.  These values are provided to minimize the need for
boilerplate code that challenge authors need.  If there is a need for random
values beyond the flag, challenge authors should ensure they use`{{seed}}` as
the initial seed for any randomization functions they use in order to ensure
reproducible builds.

## Installing Dependencies

If the challenge requires any additional packages, the corresponding `apt`
packages can be listed with one per line in `packages.txt` and they will be
installed before the application is launched.
