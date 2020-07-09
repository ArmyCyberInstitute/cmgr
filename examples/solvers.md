# Automated Solvers

One of the goals for `cmgr` is to further improve the state of CI/CD for CTFs.
Part of this is creating a framework for automated solvescripts that can check
the status and solvability of deployed challenges.  To enable this, we have
implemented a standardized mechanism for solve scripts that can be easily run
against individual challenge instances.

## Usage

To create a solver, a challenge author only needs to create a `solver` directory
inside of their challenge directory and add a `solve.py` script (Python 3) that
implements the solution (both regular build tools and `pwntools` are installed by default).

The `cmgr` interface will ensure any requested dependencies are installed prior to launching the script and will launch the script from a container in the same network as the challenge itself.  This allows the challenge author to leverage the standardized DNS naming convention (`challenge` for the container hosting the challenge and `solver` for the solve script container) as well as the static ports in use (5000/tcp for `cmgr` challenge types and whatever was chosen for "custom" challenges).

In addition to the files in the `solver` directory, `cmgr` will extract the artifacts given to competitors into the working directory of the solve script prior to launch.

Once the flag has been retrieved, the solve script should write the flag to a file named `flag` in the working directory and exit.

## Installing Dependencies

There are two means for installing additional dependencies that the solver
requires: `packages.txt` for `apt` packages and `requirements.txt` for `pip3`
modules.  Both files consist of a single dependency per line.  Note: although
`requirements.txt` supports pinning the version of each dependency, there is
currently no support for similar functionality in `packages.txt`.
