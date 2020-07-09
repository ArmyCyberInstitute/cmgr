# `php` Challenge Type

This challenge provides the basic framework for creating web-challenges that
run as a PHP server.

## Usage

In addition the the challenge description in `problem.json`, challenge authors
need to provide the php application and supporting resources with the
challenge directory as the root.

## Automatic Templating

There are two values that `cmgr` will template into every file that ends in
`.php`, `.txt`, or `.html`:  `{{flag}}` and `{{seed}}`.  These values are
provided to minimize the need for boilerplate code that challenge authors
need.  If there is a need for random values beyond the flag, challenge authors
should ensure they use `{{seed}}` as the initial seed for any PHP
randomization library they use in order to ensure reproducible builds.

## Installing Dependencies

If the challenge requires any additional packages, the corresponding `apt`
packages can be listed with one per line in `packages.txt` and they will be
installed before the application is launched.
