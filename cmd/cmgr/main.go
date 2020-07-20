package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/ArmyCyberInstitute/cmgr/cmgr"
)

func main() {
	if len(os.Args) == 1 {
		printUsage()
		os.Exit(0)
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

	switch os.Args[1] {
	case "list":
		if len(os.Args) > 3 {
			fmt.Println("error: too many arguments")
			printUsage()
			os.Exit(-1)
		}

		if len(os.Args) > 2 && os.Args[2] != "--verbose" {
			fmt.Printf("error: unrecognized argument of '%s'\n", os.Args[2])
			printUsage()
			os.Exit(-1)
		}

		verbose := len(os.Args) > 2 && os.Args[2] == "--verbose"
		challenges := mgr.ListChallenges()
		printChallenges(challenges, verbose)
		os.Exit(0)
	case "search":
		verbose := false
		tags := []string{}
		if len(os.Args) > 2 {
			verbose = os.Args[2] == "--verbose"
			idx := 2
			if verbose {
				idx += 1
			}
			tags = os.Args[idx:]
		}

		challenges := mgr.SearchChallenges(tags)
		printChallenges(challenges, verbose)
		os.Exit(0)
	case "info":
		if len(os.Args) > 3 {
			fmt.Println("error: too many arguments")
			printUsage()
			os.Exit(-1)
		}

		path := "."
		if len(os.Args) == 3 {
			path = os.Args[2]
		}

		metalist := getMetaByDir(mgr, path)

		for _, cMeta := range metalist {
			fmt.Printf("%s\n", cMeta.Id)

			fmt.Printf("    Name: %s\n", cMeta.Name)
			fmt.Printf("    Challenge Type: %s\n", cMeta.ChallengeType)
			fmt.Printf("    Category: %s\n", cMeta.Category)
			fmt.Printf("    Points: %d\n", cMeta.Points)

			fmt.Println("\n    Description:")
			fmt.Printf("        %s\n", cMeta.Description)

			fmt.Println("\n    Details:")
			fmt.Printf("        %s\n", cMeta.Details)

			if len(cMeta.Hints) > 0 {
				fmt.Println("\n    Hints")
				for _, hint := range cMeta.Hints {
					fmt.Printf("        - %s\n", hint)
				}
			}
		}
		os.Exit(0)
	case "update":
		path := ""
		verbose := false
		dryRun := false
		switch len(os.Args) {
		case 2:
			// Keep the defaults
		case 5:
			if os.Args[4] == "--verbose" {
				verbose = true
			} else if os.Args[4] == "--dry-run" {
				dryRun = true
			} else {
				path = os.Args[4]
			}
			fallthrough
		case 4:
			if os.Args[3] == "--verbose" {
				verbose = true
			} else if os.Args[3] == "--dry-run" {
				dryRun = true
			} else {
				path = os.Args[3]
			}
			fallthrough
		case 3:
			if os.Args[2] == "--verbose" {
				verbose = true
			} else if os.Args[2] == "--dry-run" {
				dryRun = true
			} else {
				path = os.Args[2]
			}
		default:
			fmt.Println("error: too many arguments")
			printUsage()
			os.Exit(-1)
		}

		var updates *cmgr.ChallengeUpdates
		if dryRun {
			updates = mgr.DetectChanges(path)
		} else {
			updates = mgr.Update(path)
		}

		printChanges(updates, verbose)
		exitCode := 0
		if len(updates.Errors) > 0 {
			exitCode = -1
		}
		os.Exit(exitCode)
	case "build":
		if len(os.Args) == 2 {
			fmt.Println("error: build command requires arguments")
			printUsage()
			os.Exit(-1)
		}

		idx := 2
		format := "flag{%s}"
		flagLen := len("--flag-format=")
		if len(os.Args[idx]) > flagLen && os.Args[idx][:flagLen] == "--flag-format=" {
			format = os.Args[idx][len("--flag-format="):]
			idx++
		}

		if len(os.Args) < idx+2 {
			fmt.Println("error: challenge id and at least one seed required")
			printUsage()
			os.Exit(-1)
		}

		challenge := cmgr.ChallengeId(os.Args[idx])
		idx++

		seeds := []int{}
		for idx < len(os.Args) {
			seed, err := strconv.ParseInt(os.Args[idx], 0, 0)
			if err != nil {
				fmt.Printf("error: could not convert seed value of '%s' to an integer\n", os.Args[idx])
				printUsage()
				os.Exit(-1)
			}
			idx++
			seeds = append(seeds, int(seed))
		}

		builds, err := mgr.Build(challenge, seeds, format)
		if err != nil {
			fmt.Printf("error: %s\n", err)
			os.Exit(-1)
		}

		fmt.Println("Build IDs:")
		for _, build := range builds {
			fmt.Printf("    %d\n", build.Id)
		}
		os.Exit(0)
	case "start":
		if len(os.Args) != 3 {
			fmt.Println("error: expected exactly one additional argument")
			printUsage()
			os.Exit(-1)
		}

		build, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Printf("error: could not interpret build id: %s\n", err)
			os.Exit(-1)
		}

		instance, err := mgr.Start(cmgr.BuildId(build))
		if err != nil {
			fmt.Printf("error: could not start instance: %s\n", err)
			os.Exit(-1)
		}

		fmt.Printf("Instance ID: %d\n", instance)
		os.Exit(0)
	case "check":
		if len(os.Args) == 2 {
			fmt.Println("error: expected at least one instance ID")
			printUsage()
			os.Exit(-1)
		}

		instances := []cmgr.InstanceId{}
		for _, instanceStr := range os.Args[2:] {
			instanceInt, err := strconv.Atoi(instanceStr)
			if err != nil {
				fmt.Printf("error: invalid instance ID of '%s'\n", instanceStr)
				os.Exit(-1)
			}
			instances = append(instances, cmgr.InstanceId(instanceInt))
		}

		exitCode := 0
		for _, instance := range instances {
			err := mgr.CheckInstance(instance)
			if err != nil {
				exitCode = -1
				fmt.Printf("check of instance %d failed with: %s\n", instance, err)
			}
		}
		os.Exit(exitCode)
	case "stop":
		if len(os.Args) != 3 {
			fmt.Println("error: expected exactly one additional argument")
			printUsage()
			os.Exit(-1)
		}

		instance, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Printf("error: could not interpret instance id: %s\n", err)
			os.Exit(-1)
		}

		err = mgr.Stop(cmgr.InstanceId(instance))
		if err != nil {
			fmt.Printf("error: could not stop instance: %s\n", err)
			os.Exit(-1)
		}

		os.Exit(0)

	case "destroy":
		if len(os.Args) < 3 {
			fmt.Println("error: expected at least one build id")
			printUsage()
			os.Exit(-1)
		}

		exitCode := 0

		for _, arg := range os.Args[2:] {

			build, err := strconv.Atoi(arg)
			if err != nil {
				fmt.Printf("error: could not interpret build id: %s\n", err)
				exitCode = -1
			}

			err = mgr.Destroy(cmgr.BuildId(build))
			if err != nil {
				fmt.Printf("error: could not destroy build: %s\n", err)
				exitCode = -1
			}
		}

		os.Exit(exitCode)
	case "reset":
		if len(os.Args) > 3 {
			fmt.Println("error: too many arguments")
			printUsage()
			os.Exit(-1)
		}

		verbose := false
		exitCode := 0

		if len(os.Args) == 3 && os.Args[2] != "--verbose" {
			fmt.Printf("error: unrecognized argument '%s'\n", os.Args[2])
			printUsage()
			os.Exit(-1)
		} else if len(os.Args) == 3 {
			verbose = true
		}
		state, err := mgr.DumpState(nil)
		if err != nil {
			fmt.Printf("error: %s\n", err)
			os.Exit(-1)
		}

		for _, challenge := range state {
			for _, build := range challenge.Builds {
				for _, instance := range build.Instances {
					if verbose {
						fmt.Printf("Stopping %s|%d|%d...\n",
							challenge.Id,
							build.Id,
							instance.Id)
					}
					err = mgr.Stop(instance.Id)
					if err != nil {
						fmt.Printf("error stopping %s|%d|%d: %s\n",
							challenge.Id,
							build.Id,
							instance.Id,
							err)
						exitCode = -1
					}
				}
				if verbose {
					fmt.Printf("Destroying %s|%d...\n",
						challenge.Id,
						build.Id)
				}
				err = mgr.Destroy(build.Id)
				if err != nil {
					fmt.Printf("error destroying %s|%d: %s\n",
						challenge.Id,
						build.Id,
						err)
					exitCode = -1
				}
			}
		}

		os.Exit(exitCode)
	case "test":
		path := "."
		solve := true
		required := false

		switch len(os.Args) {
		case 2:
			// Just use defaults
		case 3:
			solve = os.Args[2] != "--no-solve"
			required = os.Args[2] == "--require-solve"

			if solve && !required { // Didn't match a flag, so treat as path
				path = os.Args[2]
			}
		case 4:
			path = os.Args[3]

			if os.Args[2] != "--no-solve" && os.Args[2] != "--require-solve" {
				fmt.Printf("error: unrecognized argument of '%s'\n", os.Args[2])
				printUsage()
				os.Exit(-1)
			}

			solve = os.Args[2] != "--no-solve"
			required = os.Args[2] == "--require-solve"
		default:
			fmt.Println("error: too many arguments")
			printUsage()
			os.Exit(-1)
		}

		cu := mgr.Update(path)
		printChanges(cu, false)
		if len(cu.Errors) > 0 {
			os.Exit(-1)
		}

		metalist := getMetaByDir(mgr, path)

		exitCode := 0
		for _, cMeta := range metalist {
			if !runTest(mgr, cMeta, solve, required) {
				exitCode = -1
			}
		}
		os.Exit(exitCode)
	case "system-dump":
		challenges := []cmgr.ChallengeId{}
		displayFormat := "normal"
		if len(os.Args) > 2 {
			idx := 2
			if len(os.Args[idx]) >= 2 && os.Args[idx][:2] == "--" {
				displayFormat = os.Args[idx][2:]
				idx++
			}

			for idx < len(os.Args) {
				challenges = append(challenges, cmgr.ChallengeId(os.Args[idx]))
				idx++
			}
		}

		state, err := mgr.DumpState(challenges)
		if err != nil {
			fmt.Printf("error: %s\n", err)
			os.Exit(-1)
		}

		switch displayFormat {
		case "normal":
			for _, challenge := range state {
				fmt.Printf("%s:\n", challenge.Id)
				for _, build := range challenge.Builds {
					fmt.Printf("    Build ID: %d\n", build.Id)
					for _, instance := range build.Instances {
						fmt.Printf("        %d\n", instance.Id)
					}
				}
			}
		case "json":
			data, err := json.MarshalIndent(state, "", "    ")
			if err != nil {
				fmt.Printf("error: JSON encoding failed: %s", err)
				os.Exit(-1)
			}

			fmt.Println(string(data))
		case "summary":
			for _, challenge := range state {
				nInstances := 0
				for _, build := range challenge.Builds {
					nInstances += len(build.Instances)
				}

				if len(challenge.Builds) > 0 {
					fmt.Printf("%s: %d builds (%d instances)\n",
						challenge.Id,
						len(challenge.Builds),
						nInstances)
				}
			}
		default:
			fmt.Printf("error: unrecognized flag of %s\n", os.Args[2])
			printUsage()
			os.Exit(-1)
		}
	default:
		fmt.Println("error: unrecognized command")
		printUsage()
		os.Exit(-1)
	}
}

func printChallenges(challenges []*cmgr.ChallengeMetadata, verbose bool) {
	for _, challenge := range challenges {
		var line string
		if verbose {
			line = fmt.Sprintf(`%s: "%s"`, challenge.Id, challenge.Name)
		} else {
			line = string(challenge.Id)
		}
		fmt.Println(line)
	}
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

func printChanges(status *cmgr.ChallengeUpdates, verbose bool) {
	if verbose && len(status.Unmodified) != 0 {
		fmt.Println("Unmodified:")
		for _, md := range status.Unmodified {
			fmt.Printf("    %s\n", md.Id)
		}
	}

	if len(status.Added) != 0 {
		fmt.Println("Added:")
		for _, md := range status.Added {
			fmt.Printf("    %s\n", md.Id)
		}
	}

	if len(status.Refreshed) != 0 {
		fmt.Println("Refreshed:")
		for _, md := range status.Refreshed {
			fmt.Printf("    %s\n", md.Id)
		}
	}

	if len(status.Updated) != 0 {
		fmt.Println("Updated:")
		for _, md := range status.Updated {
			fmt.Printf("    %s\n", md.Id)
		}
	}

	if len(status.Removed) != 0 {
		fmt.Println("Removed:")
		for _, md := range status.Removed {
			fmt.Printf("    %s\n", md.Id)
		}
	}

	if len(status.Errors) != 0 {
		fmt.Println("Errors:")
		for idx, err := range status.Errors {
			fmt.Printf("    %d) %s\n", idx+1, err)
		}
	}
}

func getMetaByDir(m *cmgr.Manager, dir string) []*cmgr.ChallengeMetadata {
	cu := m.DetectChanges(dir)

	for i, meta := range cu.Unmodified {
		fullMeta, err := m.GetChallengeMetadata(meta.Id)
		if err != nil {
			cu.Errors = append(cu.Errors, err)
			continue
		}

		cu.Unmodified[i] = fullMeta
	}

	if len(cu.Errors) > 0 {
		fmt.Println("error: errors occurred during execution:")
		for i, err := range cu.Errors {
			fmt.Printf("    %d) %s\n", i+1, err)
		}
		os.Exit(-1)
	}

	if len(cu.Added)+len(cu.Updated)+len(cu.Refreshed)+len(cu.Removed) > 0 {
		fmt.Println("error: database out of sync with filesystem, run 'update'")
		printChanges(cu, false)
		os.Exit(-1)
	}

	return cu.Unmodified
}

func runTest(mgr *cmgr.Manager, cMeta *cmgr.ChallengeMetadata, solve, required bool) bool {

	// Build
	builds, err := mgr.Build(cMeta.Id, []int{42}, "flag{%s}")
	if err != nil {
		fmt.Printf("error (%s): could not build: %s\n", cMeta.Id, err)
		return false
	}
	build := builds[0]
	if solve {
		defer mgr.Destroy(build.Id)
	}

	// Start
	instance, err := mgr.Start(build.Id)
	if err != nil {
		fmt.Printf("error (%s): could not start instance: %s\n", cMeta.Id, err)
		return false
	}
	if solve {
		defer mgr.Stop(instance)
	}

	// Solve
	if solve && cMeta.SolveScript {
		err = mgr.CheckInstance(instance)
		if err != nil {
			fmt.Printf("error (%s): solver failed: %s\n", cMeta.Id, err)
			return false
		}
	} else if solve && required {
		fmt.Printf("error (%s): no solver found\n", cMeta.Id)
		return false
	} else if !solve {
		iMeta, err := mgr.GetInstanceMetadata(instance)
		if err != nil {
			fmt.Printf("error (%s): could not get instance metadata: %s\n", cMeta.Id, err)
			return false
		}

		// Interactive so print some useful information
		fmt.Printf("%s|%d|%d\n", cMeta.Id, build.Id, instance)
		fmt.Printf("    flag: %s\n", build.Flag)

		if len(build.LookupData) > 0 {
			fmt.Println("    lookup data:")
			for k, v := range build.LookupData {
				fmt.Printf("        %s: %s\b", k, v)
			}
		}

		if build.HasArtifacts {
			artDir, isSet := os.LookupEnv(cmgr.ARTIFACT_DIR_ENV)
			if !isSet {
				artDir = "."
			}
			fmt.Printf("    artifacts file: %s.tar.gz\n", filepath.Join(artDir, build.Images[0].DockerId))
		}

		if len(iMeta.Ports) > 0 {
			fmt.Println("    ports:")
			for name, port := range iMeta.Ports {
				fmt.Printf("        %s: %d\n", name, port)
			}
		}
	}

	return true
}
