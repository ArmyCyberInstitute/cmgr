package main

import (
	"fmt"
	"log"

	"github.com/ArmyCyberInstitute/cmgr/cmgr"
)

func main() {
	log.SetFlags(0)
	mgr := cmgr.NewManager(cmgr.DEBUG)
	printChanges(mgr.DetectChanges(""))

}

func printChanges(status *cmgr.ChallengeUpdates) {
	changes := false
	if len(status.Unmodified) != 0 {
		changes = true
		fmt.Println("Unmodified:")
		for _, md := range status.Unmodified {
			fmt.Printf("    %s\n", md.Id)
		}
	}

	if len(status.Added) != 0 {
		changes = true
		fmt.Println("Added:")
		for _, md := range status.Added {
			fmt.Printf("    %s\n", md.Id)
		}
	}

	if len(status.Updated) != 0 {
		changes = true
		fmt.Println("Updated:")
		for _, md := range status.Updated {
			fmt.Printf("    %s\n", md.Id)
		}
	}

	if len(status.Removed) != 0 {
		changes = true
		fmt.Println("Removed:")
		for _, md := range status.Removed {
			fmt.Printf("    %s\n", md.Id)
		}
	}

	if !changes {
		fmt.Println("No changes")
	}

	if len(status.Errors) != 0 {
		fmt.Println("Errors:")
		for idx, err := range status.Errors {
			fmt.Printf("    %d) %s\n", idx, err)
		}
	}
}
