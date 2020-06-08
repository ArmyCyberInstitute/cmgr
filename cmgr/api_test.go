package cmgr

import (
	"fmt"
)

// Iterates over all of the discovered challenges
func ExampleListChallenges() {
	mgr := NewManager(WARN)

	for _, c := range mgr.ListChallenges() {
		cm, err := mgr.GetChallengeMetadata(c)
		if err == nil {
			fmt.Printf("%s (%s)", cm.Id, cm.Name)
		}
	}
}
