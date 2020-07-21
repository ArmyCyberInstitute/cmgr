package main

import (
	"fmt"
	"strconv"

	"github.com/ArmyCyberInstitute/cmgr/cmgr"
)

func doBuild(mgr *cmgr.Manager, args []string) int {
	if len(args) == 0 {
		fmt.Println("error: build command requires arguments")
		return USAGE_ERROR
	}

	idx := 0
	format := "flag{%s}"
	flagLen := len("--flag-format=")
	if len(args[idx]) > flagLen && args[idx][:flagLen] == "--flag-format=" {
		format = args[idx][len("--flag-format="):]
		idx++
	}

	if len(args) < idx+2 {
		fmt.Println("error: challenge id and at least one seed required")
		return USAGE_ERROR
	}

	challenge := cmgr.ChallengeId(args[idx])
	idx++

	seeds := []int{}
	for idx < len(args) {
		seed, err := strconv.ParseInt(args[idx], 0, 0)
		if err != nil {
			fmt.Printf("error: could not convert seed value of '%s' to an integer\n", args[idx])
			return USAGE_ERROR
		}
		idx++
		seeds = append(seeds, int(seed))
	}

	builds, err := mgr.Build(challenge, seeds, format)
	if err != nil {
		fmt.Printf("error: %s\n", err)
		return RUNTIME_ERROR
	}

	fmt.Println("Build IDs:")
	for _, build := range builds {
		fmt.Printf("    %d\n", build.Id)
	}
	return NO_ERROR
}

func destroyBuilds(mgr *cmgr.Manager, args []string) int {
	if len(args) == 0 {
		fmt.Println("error: expected at least one build id")
		return USAGE_ERROR
	}

	retCode := NO_ERROR

	for _, arg := range args {

		build, err := strconv.Atoi(arg)
		if err != nil {
			fmt.Printf("error: could not interpret build id: %s\n", err)
			retCode = RUNTIME_ERROR
		}

		err = mgr.Destroy(cmgr.BuildId(build))
		if err != nil {
			fmt.Printf("error: could not destroy build: %s\n", err)
			retCode = RUNTIME_ERROR
		}
	}

	return retCode
}
