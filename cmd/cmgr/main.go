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

	log.SetFlags(0)
	mgr := cmgr.NewManager(cmgr.ERROR)

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
		os.Exit(listCmd(mgr, verbose))
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

		ids, err := mgr.Build(challenge, seeds, format)
		if err != nil {
			fmt.Printf("error: %s\n", err)
			os.Exit(-1)
		}

		fmt.Println("Build IDs:")
		for _, id := range ids {
			fmt.Printf("    %d\n", id)
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
			// Build
			buildIds, err := mgr.Build(cMeta.Id, []int{42}, "flag{%s}")
			if err != nil {
				exitCode = -1
				fmt.Printf("error (%s): could not build: %s\n", cMeta.Id, err)
				continue
			}
			build := buildIds[0]
			if solve {
				defer mgr.Destroy(build)
			}

			// Start
			instance, err := mgr.Start(build)
			if err != nil {
				exitCode = -1
				fmt.Printf("error (%s): could not start instance: %s\n", cMeta.Id, err)
				continue
			}
			if solve {
				defer mgr.Stop(instance)
			}

			// Solve
			if solve && cMeta.SolveScript {
				err = mgr.CheckInstance(instance)
				if err != nil {
					exitCode = -1
					fmt.Printf("error (%s): solver failed: %s\n", cMeta.Id, err)
					continue
				}
			} else if solve && required {
				exitCode = -1
				fmt.Printf("error (%s): no solver found\n", cMeta.Id)
				continue
			} else if !solve {
				bMeta, err := mgr.GetBuildMetadata(build)
				if err != nil {
					exitCode = -1
					fmt.Printf("error (%s): could not get build metadata: %s\n", cMeta.Id, err)
					continue
				}

				iMeta, err := mgr.GetInstanceMetadata(instance)
				if err != nil {
					exitCode = -1
					fmt.Printf("error (%s): could not get instance metadata: %s\n", cMeta.Id, err)
					continue
				}

				// Interactive so print some useful information
				fmt.Printf("%s|%d|%d\n", cMeta.Id, build, instance)
				fmt.Printf("    flag: %s\n", bMeta.Flag)

				if len(bMeta.LookupData) > 0 {
					fmt.Println("    lookup data:")
					for k, v := range bMeta.LookupData {
						fmt.Printf("        %s: %s\b", k, v)
					}
				}

				if bMeta.HasArtifacts {
					artDir, isSet := os.LookupEnv(cmgr.ARTIFACT_DIR_ENV)
					if !isSet {
						artDir = "."
					}
					fmt.Printf("    artifacts file: %s.tar.gz\n", filepath.Join(artDir, bMeta.Images[0].DockerId))
				}

				if len(iMeta.Ports) > 0 {
					fmt.Println("    ports:")
					for name, port := range iMeta.Ports {
						fmt.Printf("        %s: %d\n", name, port)
					}
				}
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

func listCmd(mgr *cmgr.Manager, verbose bool) int {
	challenges := mgr.ListChallenges()
	exitCode := 0
	for _, challenge := range challenges {
		var line string
		if verbose {
			md, err := mgr.GetChallengeMetadata(challenge)
			if err == nil {
				line = fmt.Sprintf(`%s: "%s"`, challenge, md.Name)
			} else {
				line = fmt.Sprintf(`%s: (ERROR: %s)`, challenge, err)
				exitCode = -1
			}
		} else {
			line = string(challenge)
		}
		fmt.Println(line)
	}
	return exitCode
}

func printUsage() {
	fmt.Printf(`
Usage: %s <command> [<args>]

Available commands:
  list [--verbose]
  info [<path>]
  update [--verbose] [--dry-run] [<path>]
  build [--flag-format=<format_string>] <challenge> <seed> [<seed>...]
  start <build identfier>
  stop <instance identifier>
  destroy <build identifier>
  reset [--verbose]
  test [--no-solve|--require-solve] [<path>]
  system-dump [--summary|--json] [<challenge> ...]
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
