package main

import (
	"fmt"
	"strconv"

	"github.com/ArmyCyberInstitute/cmgr/cmgr"
)

func startInstance(mgr *cmgr.Manager, args []string) int {
	if len(args) != 1 {
		fmt.Println("error: expected exactly one additional argument")
		return USAGE_ERROR
	}

	build, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Printf("error: could not interpret build id: %s\n", err)
		return RUNTIME_ERROR
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
	if len(args) == 0 {
		fmt.Println("error: expected at least one instance ID")
		return USAGE_ERROR
	}

	instances := []cmgr.InstanceId{}
	for _, instanceStr := range args {
		instanceInt, err := strconv.Atoi(instanceStr)
		if err != nil {
			fmt.Printf("error: invalid instance ID of '%s'\n", instanceStr)
			return RUNTIME_ERROR
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
	if len(args) != 1 {
		fmt.Println("error: expected exactly one additional argument")
		return USAGE_ERROR
	}

	instance, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Printf("error: could not interpret instance id: %s\n", err)
		return RUNTIME_ERROR
	}

	err = mgr.Stop(cmgr.InstanceId(instance))
	if err != nil {
		fmt.Printf("error: could not stop instance: %s\n", err)
		return RUNTIME_ERROR
	}

	return NO_ERROR
}
