package main

import (
	"encoding/json"
	"flag"
	"fmt"

	"github.com/ArmyCyberInstitute/cmgr/cmgr"
)

func resetSystemState(mgr *cmgr.Manager, args []string) int {
	parser := flag.NewFlagSet("reset", flag.ExitOnError)
	updateUsage(parser, "")
	verbose := parser.Bool("verbose", false, "print more information")
	parser.Parse(args)

	if parser.NArg() != 0 {
		parser.Usage()
		return USAGE_ERROR
	}

	retCode := NO_ERROR
	state, err := mgr.DumpState(nil)
	if err != nil {
		fmt.Printf("error: %s\n", err)
		return RUNTIME_ERROR
	}

	for _, challenge := range state {
		for _, build := range challenge.Builds {
			for _, instance := range build.Instances {
				if *verbose {
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
			if *verbose {
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
	parser := flag.NewFlagSet("system-dump", flag.ExitOnError)
	updateUsage(parser, "[<challenge> ...]")
	summary := parser.Bool("summary", false, "print only summary information")
	jsonout := parser.Bool("json", false, "print information as json")
	parser.Parse(args)

	if *summary && *jsonout {
		fmt.Fprintf(parser.Output(), "error: json and summary options cannot be combined\n")
		parser.Usage()
		return USAGE_ERROR
	}

	challenges := []cmgr.ChallengeId{}
	for _, cid := range parser.Args() {
		challenges = append(challenges, cmgr.ChallengeId(cid))
	}

	state, err := mgr.DumpState(challenges)
	if err != nil {
		fmt.Printf("error: %s\n", err)
		return RUNTIME_ERROR
	}

	if *summary {
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
	} else if *jsonout {
		data, err := json.MarshalIndent(state, "", "    ")
		if err != nil {
			fmt.Printf("error: JSON encoding failed: %s", err)
			return RUNTIME_ERROR
		}

		fmt.Println(string(data))
	} else {
		for _, challenge := range state {
			fmt.Printf("%s:\n", challenge.Id)
			for _, build := range challenge.Builds {
				fmt.Printf("    Build ID: %d\n", build.Id)
				for _, instance := range build.Instances {
					fmt.Printf("        %d\n", instance.Id)
				}
			}
		}
	}

	return NO_ERROR
}
