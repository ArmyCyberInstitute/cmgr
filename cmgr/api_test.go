package cmgr

import (
	"fmt"
)

// Iterates over all of the discovered challenges
func ExampleListChallenges() {
	mgr := NewManager(WARN)

	for _, c := range mgr.ListChallenges() {
		fmt.Printf("%s (%s)", c.Id, c.Name)
	}
}
