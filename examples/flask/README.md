# `flask` Challenge Type

This challenge provides the basic framework for creating web-challenges that
run as a flask server.

## Usage

In addition to the challenge description in `problem.json`, challenge authors
need to provide the flask app with the main entry file named as `app.py`.

## Automatic Templating

There are three values that `cmgr` will template into `app.py`:  `{{flag}}`,
`{{secret_key}}`, and `{{seed}}`.  These values are provided to minimize the
need for boilerplate code that challenge authors need.  If there is a need for
random values beyond the flag and Flask's secret key for session cookies,
challenge authors should ensure they use `{{seed}}` as the initial seed for
Python's random library in order to ensure reproducible builds.

**NOTE:** Because both Flask and `cmgr` use Jinja2-style formatting
directives, `cmgr` *only* templates `app.py` to avoid trampling any
formatting directives intended for the Flask application itself.

## Installing Dependencies

There are two means for installing additional dependencies that the challenge
requires: `packages.txt` for `apt` packages and `requirements.txt` for `pip3`
modules.  Both files consist of a single dependency per line.  Note: although
`requirements.txt` supports pinning the version of each dependency, there is
currently no support for similar functionality in `packages.txt`.
