package main

import (
	"fmt"
	"log"
	"os"

	"github.com/ArmyCyberInstitute/cmgr/cmgr"
)

const (
	USAGE_ERROR   int = -2
	RUNTIME_ERROR int = -1
	NO_ERROR      int = 0
)

func main() {
	if len(os.Args) == 1 {
		printUsage()
		os.Exit(NO_ERROR)
	}

	var logLevel cmgr.LogLevel
	log_env, _ := os.LookupEnv(cmgr.LOGGING_ENV)
	switch log_env {
	case "debug":
		logLevel = cmgr.DEBUG
	case "info":
		logLevel = cmgr.INFO
	case "warn":
		logLevel = cmgr.WARN
	case "error":
		logLevel = cmgr.ERROR
	default:
		logLevel = cmgr.DISABLED
	}

	log.SetFlags(0)
	mgr := cmgr.NewManager(logLevel)
	cmdArgs := os.Args[2:]

	var exitCode int
	switch os.Args[1] {
	case "list":
		exitCode = listChallenges(mgr, cmdArgs)
	case "search":
		exitCode = searchChallenges(mgr, cmdArgs)
	case "info":
		exitCode = displayChallengeInfo(mgr, cmdArgs)
	case "update":
		exitCode = updateChallengeInfo(mgr, cmdArgs)
	case "build":
		exitCode = doBuild(mgr, cmdArgs)
	case "start":
		exitCode = startInstance(mgr, cmdArgs)
	case "check":
		exitCode = checkInstances(mgr, cmdArgs)
	case "stop":
		exitCode = stopInstance(mgr, cmdArgs)
	case "destroy":
		exitCode = destroyBuilds(mgr, cmdArgs)
	case "reset":
		exitCode = resetSystemState(mgr, cmdArgs)
	case "test":
		exitCode = testChallenges(mgr, cmdArgs)
	case "system-dump":
		exitCode = dumpSystemState(mgr, cmdArgs)
	default:
		fmt.Println("error: unrecognized command")
		exitCode = USAGE_ERROR
	}

	if exitCode == USAGE_ERROR {
		printUsage()
	}

	os.Exit(exitCode)
}

func printUsage() {
	fmt.Printf(`
Usage: %s <command> [<args>]

Available commands:
  list [--verbose]
      lists all of the challenges currently indexed

  search [--verbose] [tag ...]
      lists the challenges that match on all of the listed tags (alias of list
      if no tags provided)

  info [<path>]
      provides information on all challenges underneath the provided path
      (defaults to current directory if not provided)

  update [--verbose] [--dry-run] [<path>]
      updates the metadata for all challenges underneath the provided path and
      rebuilds/restarts and existing builds/insances of those challenges; the
      '--dry-run' flag will print the changes it detects without updating the
      system state; path defaults to the root challenge directory if omitted.

  build [--flag-format=<format_string>] <challenge> <seed> [<seed>...]
      creates a new, templated build of the challenge using the provided flag
      format for each seed provided; the flag format defaults to 'flag{%%s}'
      if not provided; prints a list of Build IDs that were created

  start <build identfier>
      creates a new running instance of the build and prints its ID to stdout

  stop <instance identifier>
      stops the given instance

  destroy <build identifier>
      destroys the given build if no instances are running, otherwise it exits
      with a non-zero exit code and does nothing; reclaims disk space used by
      Docker images and artifact files

  reset [--verbose]
      stops all known instances and destroys all known builds

  test [--no-solve|--require-solve] [<path>]
      shortcut for calling 'update' on the given path followed by build,
      start, check, stop, destroy for each challenge in the directory;
      'no-solve' will skip the last three steps and leave an instance of each
      challenge running while 'require-solve' will treat the absence of a
      solve script as an error.

  system-dump [--summary|--json] [<challenge> ...]
      lists the challenges along with their builds and instances; only counts
      are given for 'summary' and all metadata is given in JSON format for
      'json'; all challenges are listed if no challenge IDs are provided

Relevant environment variables:
  CMGR_DB - path to cmgr's database file (defaults to 'cmgr.db')

  CMGR_DIR - directory containing all challenges (defaults to '.')

  CMGR_ARTIFACT_DIR - directory for storing artifact bundles (defaults to '.')

  Note: The Docker client is configured via Docker's standard environment
      variables.  See https://docs.docker.com/engine/reference/commandline/cli/
      for specific details.

`, os.Args[0])
}
