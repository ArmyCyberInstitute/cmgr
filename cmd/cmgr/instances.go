package main

import (
	"flag"
	"fmt"
	"strconv"

	"github.com/ArmyCyberInstitute/cmgr/cmgr"
)

func startInstance(mgr *cmgr.Manager, args []string) int {
	parser := flag.NewFlagSet("start", flag.ExitOnError)
	updateUsage(parser, "<build>")
	parser.Parse(args)

	if parser.NArg() != 1 {
		parser.Usage()
		return USAGE_ERROR
	}

	build, err := strconv.Atoi(parser.Arg(0))
	if err != nil {
		fmt.Fprintf(parser.Output(), "error: could not interpret '%s' as a build id: %s\n", parser.Arg(0), err)
		parser.Usage()
		return USAGE_ERROR
	}

	instance, err := mgr.Start(cmgr.BuildId(build))
	if err != nil {
		fmt.Printf("error: could not start instance: %s\n", err)
		return RUNTIME_ERROR
	}

	fmt.Printf("Instance ID: %d\n", instance)
	return NO_ERROR
}

func checkInstances(mgr *cmgr.Manager, args []string) int {
	parser := flag.NewFlagSet("check", flag.ExitOnError)
	updateUsage(parser, "<instance> [<instance> ...]")
	parser.Parse(args)

	if parser.NArg() < 1 {
		parser.Usage()
		return USAGE_ERROR
	}

	instances := []cmgr.InstanceId{}
	for _, instanceStr := range parser.Args() {
		instanceInt, err := strconv.Atoi(instanceStr)
		if err != nil {
			fmt.Fprintf(parser.Output(), "error: could not interpret '%s' as an instance id: %s\n", instanceStr, err)
			parser.Usage()
			return USAGE_ERROR
		}
		instances = append(instances, cmgr.InstanceId(instanceInt))
	}

	retCode := NO_ERROR
	for _, instance := range instances {
		err := mgr.CheckInstance(instance)
		if err != nil {
			retCode = RUNTIME_ERROR
			fmt.Printf("check of instance %d failed with: %s\n", instance, err)
		}
	}
	return retCode
}

func stopInstance(mgr *cmgr.Manager, args []string) int {
	parser := flag.NewFlagSet("stop", flag.ExitOnError)
	updateUsage(parser, "<instance>")
	parser.Parse(args)

	if parser.NArg() != 1 {
		return USAGE_ERROR
	}

	instance, err := strconv.Atoi(parser.Arg(0))
	if err != nil {
		fmt.Fprintf(parser.Output(), "error: could not interpret '%s' as an instance id: %s\n", parser.Arg(0), err)
		parser.Usage()
		return USAGE_ERROR
	}

	err = mgr.Stop(cmgr.InstanceId(instance))
	if err != nil {
		fmt.Printf("error: could not stop instance: %s\n", err)
		parser.Usage()
		return RUNTIME_ERROR
	}

	return NO_ERROR
}
