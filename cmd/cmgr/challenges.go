package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ArmyCyberInstitute/cmgr/cmgr"
)

func listChallenges(mgr *cmgr.Manager, args []string) int {
	if len(args) > 1 {
		fmt.Println("error: too many arguments")
		return USAGE_ERROR
	}

	verbose := len(args) == 1 && args[0] == "--verbose"

	if len(args) == 1 && !verbose {
		fmt.Printf("error: unrecognized argument of '%s'\n", args[0])
		return USAGE_ERROR
	}

	challenges := mgr.ListChallenges()
	printChallenges(challenges, verbose)
	return NO_ERROR
}

func searchChallenges(mgr *cmgr.Manager, args []string) int {
	verbose := false
	tags := []string{}
	if len(args) > 0 {
		verbose = args[0] == "--verbose"
		idx := 0
		if verbose {
			idx += 1
		}
		tags = args[idx:]
	}

	challenges := mgr.SearchChallenges(tags)
	printChallenges(challenges, verbose)
	return NO_ERROR
}

func displayChallengeInfo(mgr *cmgr.Manager, args []string) int {
	if len(args) > 1 {
		fmt.Println("error: too many arguments")
		return USAGE_ERROR
	}

	path := "."
	if len(args) == 1 {
		path = args[0]
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
	return NO_ERROR
}

func updateChallengeInfo(mgr *cmgr.Manager, args []string) int {
	path := ""
	verbose := false
	dryRun := false
	switch len(args) {
	case 0:
		// Keep the defaults
	case 3:
		if args[2] == "--verbose" {
			verbose = true
		} else if args[2] == "--dry-run" {
			dryRun = true
		} else {
			path = args[2]
		}
		fallthrough
	case 2:
		if args[1] == "--verbose" {
			verbose = true
		} else if args[1] == "--dry-run" {
			dryRun = true
		} else {
			path = args[1]
		}
		fallthrough
	case 1:
		if args[0] == "--verbose" {
			verbose = true
		} else if args[0] == "--dry-run" {
			dryRun = true
		} else {
			path = args[0]
		}
	default:
		fmt.Println("error: too many arguments")
		return USAGE_ERROR
	}

	var updates *cmgr.ChallengeUpdates
	if dryRun {
		updates = mgr.DetectChanges(path)
	} else {
		updates = mgr.Update(path)
	}

	printChanges(updates, verbose)
	retCode := NO_ERROR
	if len(updates.Errors) > 0 {
		retCode = RUNTIME_ERROR
	}
	return retCode
}

func testChallenges(mgr *cmgr.Manager, args []string) int {
	path := "."
	solve := true
	required := false

	switch len(args) {
	case 0:
		// Just use defaults
	case 1:
		solve = args[0] != "--no-solve"
		required = args[0] == "--require-solve"

		if solve && !required { // Didn't match a flag, so treat as path
			path = args[0]
		}
	case 2:
		path = args[1]

		if args[0] != "--no-solve" && args[0] != "--require-solve" {
			fmt.Printf("error: unrecognized argument of '%s'\n", args[0])
			return USAGE_ERROR
		}

		solve = args[0] != "--no-solve"
		required = args[0] == "--require-solve"
	default:
		fmt.Println("error: too many arguments")
		return USAGE_ERROR
	}

	cu := mgr.Update(path)
	printChanges(cu, false)
	if len(cu.Errors) > 0 {
		return RUNTIME_ERROR
	}

	metalist := getMetaByDir(mgr, path)

	retCode := NO_ERROR
	for _, cMeta := range metalist {
		if !runTest(mgr, cMeta, solve, required) {
			retCode = RUNTIME_ERROR
		}
	}
	return retCode
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
		return nil
	}

	if len(cu.Added)+len(cu.Updated)+len(cu.Refreshed)+len(cu.Removed) > 0 {
		fmt.Println("error: database out of sync with filesystem, run 'update'")
		printChanges(cu, false)
		return nil
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
			fmt.Printf("    artifacts file: %s.tar.gz\n", filepath.Join(artDir, fmt.Sprint(build.Id)))
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
