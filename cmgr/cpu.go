package cmgr

import (
	"fmt"
	"math/big"
)

const nanoCPUsPerCPU int64 = 1_000_000_000

// parseNanoCPUs converts Docker's decimal --cpus representation into the
// NanoCPUs integer accepted by the Engine API. Values must resolve to a whole
// number of nanoseconds and fit in an int64.
func parseNanoCPUs(value string) (int64, error) {
	cpus, ok := new(big.Rat).SetString(value)
	if !ok {
		return 0, fmt.Errorf("failed to parse %q as a rational number", value)
	}

	nanoCPUs := new(big.Rat).Mul(cpus, big.NewRat(nanoCPUsPerCPU, 1))
	if !nanoCPUs.IsInt() {
		return 0, fmt.Errorf("CPU value %q is more precise than one NanoCPU", value)
	}
	if !nanoCPUs.Num().IsInt64() {
		return 0, fmt.Errorf("CPU value %q exceeds the supported range", value)
	}
	return nanoCPUs.Num().Int64(), nil
}
