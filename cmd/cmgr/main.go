package main

import (
	"flag"
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
	if len(os.Args) == 1 || os.Args[1] == "help" {
		printOuterUsage(os.Args[0])
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
	case "disabled":
		logLevel = cmgr.DISABLED
	default:
		logLevel = cmgr.WARN
	}

	log.SetFlags(0)
	mgr := cmgr.NewManager(logLevel)
	if mgr == nil {
		os.Exit(RUNTIME_ERROR)
	}
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
	case "list-schemas":
		exitCode = listSchemas(mgr, cmdArgs)
	case "add-schema":
		exitCode = addSchema(mgr, cmdArgs)
	case "update-schema":
		exitCode = updateSchema(mgr, cmdArgs)
	case "remove-schema":
		exitCode = removeSchema(mgr, cmdArgs)
	case "show-schema":
		exitCode = showSchema(mgr, cmdArgs)
	case "playtest":
		exitCode = playtestChallenge(mgr, cmdArgs)
	case "version":
		fmt.Println(cmgr.Version())
		exitCode = NO_ERROR
	case "help":
		printOuterUsage(os.Args[0])
		exitCode = NO_ERROR
	default:
		fmt.Println("error: unrecognized command")
		printOuterUsage(os.Args[0])
		exitCode = USAGE_ERROR
	}

	os.Exit(exitCode)
}

func updateUsage(flagSet *flag.FlagSet, positionalArgs string) {
	flagSet.Usage = func() {
		fmt.Fprintf(
			flagSet.Output(),
			"Usage: %s %s [<options>] %s\n",
			os.Args[0],
			flagSet.Name(),
			positionalArgs,
		)
		flagSet.PrintDefaults()
	}
}

func printOuterUsage(command string) {
	fmt.Printf(`
Usage: %s <subcommand>

  For each subcommand, '-h', '-help', or '--help' will print specific usage
  information along with the full list of options available for that
  subcommand.

Available commands:
  list
      lists all of the challenges currently indexed

  search [<tag> ...]
      lists challenges that match the given tags

  info [<path>]
      provides information on all challenges underneath the provided path
      (defaults to current directory if not provided)

  update [<path>]
      updates the metadata for all challenges underneath the provided path and
      rebuilds/restarts and existing builds/insances of those challenges; path
      defaults to the root challenge directory if omitted.

  build <challenge> <seed> [...]
      creates a new, templated build of the challenge using the provided flag
      format for each seed provided; the flag format defaults to 'flag{%%s}'
      if not provided; prints a list of Build IDs that were created

  start <build identfier>
      creates a new running instance of the build and prints its ID to stdout

  check <instance identifier>
      runs the associated solve script against the instance

  stop <instance identifier>
      stops the given instance

  destroy <build identifier> [...]
      destroys the given build if no instances are running, otherwise it exits
      with a non-zero exit code and does nothing; reclaims disk space used by
      Docker images and artifact files

  list-schemas
      Lists all of the current schemas.

  add-schema <schema file>
      Processes the identified schema file (must end in '.json' or '.yaml' and
      be formatted accordigly) and creates the requested builds and instances.
      These resources are locked from "manual" modifications with the exception
      of starting and stopping instances of a build with an 'instance_count' of
      -1.  If there is already a schema with the same name, no resources are
      modified.

  update-schema <schema file>
      Updates the schema with this name to match the new definition and will
      make the minimal amount of changes to converge on the new definition.

  remove-schema <schema name>
      Removes the definition and associated resources for the schema with this
      name.

  show-schema <schema name>
      Returns all of the associated challenge, build, and instance metadata for
      the named schema in JSON format.

  reset
      stops all known instances and destroys all known builds

  test [<path>]
      Shortcut for calling 'update' on the given path followed by build,
      start, check, stop, destroy for each challenge in the directory.

  playtest <challenge>
      Creates a build and instance of the challenge and then starts a simple
      http front-end scoped to only that instance.

  system-dump [<challenge> ...]
      Lists the challenges along with their builds and instances; all
      challenges are listed if no challenge IDs are provided.

  version
      Prints version information and exits.

Relevant environment variables:
  CMGR_DB - path to cmgr's database file (defaults to 'cmgr.db')

  CMGR_DIR - directory containing all challenges (defaults to '.')

  CMGR_ARTIFACT_DIR - directory for storing artifact bundles (defaults to '.')

  CMGR_LOGGING - controls the verbosity of the internal logging infrastructure
      and should be one of the following: debug, info, warn, error, or disabled
      (defaults to 'disabled')

  CMGR_INTERFACE - the host interface/address to which published challenge
      ports should be bound (defaults to '0.0.0.0'); if the specified interface
      does not exist on the host running the Docker daemon, Docker will silently
      ignore this value and instead bind to the loopback address

  Note: The Docker client is configured via Docker's standard environment
      variables.  See https://docs.docker.com/engine/reference/commandline/cli/
      for specific details.

`, command)
}
