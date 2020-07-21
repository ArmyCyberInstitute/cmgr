package main

import (
	"encoding/json"
	"fmt"

	"github.com/ArmyCyberInstitute/cmgr/cmgr"
)

func resetSystemState(mgr *cmgr.Manager, args []string) int {
	if len(args) > 1 {
		fmt.Println("error: too many arguments")
		return USAGE_ERROR
	}

	verbose := false
	retCode := NO_ERROR

	if len(args) == 1 && args[0] != "--verbose" {
		fmt.Printf("error: unrecognized argument '%s'\n", args[0])
		return USAGE_ERROR
	} else if len(args) == 1 {
		verbose = true
	}
	state, err := mgr.DumpState(nil)
	if err != nil {
		fmt.Printf("error: %s\n", err)
		return RUNTIME_ERROR
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
					retCode = RUNTIME_ERROR
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
				retCode = RUNTIME_ERROR
			}
		}
	}

	return retCode
}

func dumpSystemState(mgr *cmgr.Manager, args []string) int {
	challenges := []cmgr.ChallengeId{}
	displayFormat := "normal"
	if len(args) > 0 {
		idx := 0
		if len(args[idx]) >= 0 && args[idx][:2] == "--" {
			displayFormat = args[idx][2:]
			idx++
		}

		for idx < len(args) {
			challenges = append(challenges, cmgr.ChallengeId(args[idx]))
			idx++
		}
	}

	state, err := mgr.DumpState(challenges)
	if err != nil {
		fmt.Printf("error: %s\n", err)
		return RUNTIME_ERROR
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
			return RUNTIME_ERROR
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
		fmt.Printf("error: unrecognized flag of %s\n", args[0])
		return USAGE_ERROR
	}

	return NO_ERROR
}
