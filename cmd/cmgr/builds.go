package main

import (
	"flag"
	"fmt"
	"strconv"

	"github.com/ArmyCyberInstitute/cmgr/cmgr"
)

func doBuild(mgr *cmgr.Manager, args []string) int {
	parser := flag.NewFlagSet("build", flag.ExitOnError)
	updateUsage(parser, "<challenge> <seed> [<seed> ...]")
	flagFormat := parser.String("flag-format", "flag{%s}", "the `format-string` to use for the flag")
	parser.Parse(args)

	if parser.NArg() < 2 {
		parser.Usage()
		return USAGE_ERROR
	}

	challenge := cmgr.ChallengeId(parser.Arg(0))

	seeds := []int{}
	for _, seedStr := range parser.Args()[1:] {
		seed, err := strconv.ParseInt(seedStr, 0, 0)
		if err != nil {
			fmt.Fprintf(parser.Output(), "error: could not convert seed value of '%s' to an integer\n", seedStr)
			parser.Usage()
			return USAGE_ERROR
		}
		seeds = append(seeds, int(seed))
	}

	builds, err := mgr.Build(challenge, seeds, *flagFormat)
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
	parser := flag.NewFlagSet("destroy", flag.ExitOnError)
	updateUsage(parser, "<build> [<build> ...]")
	parser.Parse(args)

	if parser.NArg() == 0 {
		fmt.Println("error: expected at least one build id")
		parser.Usage()
		return USAGE_ERROR
	}

	retCode := NO_ERROR

	for _, arg := range parser.Args() {

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
