package main

import (
	"log"

	"github.com/ArmyCyberInstitute/cmgr/cmgr"
)

func main() {
	log.SetFlags(0)
	cmgr.NewManager(cmgr.DEBUG)
}
