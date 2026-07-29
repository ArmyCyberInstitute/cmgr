package cmgr

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/docker/go-units"
)

const (
	defaultCPUsEnv          = "CMGR_DEFAULT_CPUS"
	defaultMemoryEnv        = "CMGR_DEFAULT_MEMORY"
	defaultPidsLimitEnv     = "CMGR_DEFAULT_PIDS_LIMIT"
	defaultNofileEnv        = "CMGR_DEFAULT_NOFILE"
	maxSeedsPerRequestEnv   = "CMGR_MAX_SEEDS_PER_REQUEST"
	maxConcurrentBuildsEnv  = "CMGR_MAX_CONCURRENT_BUILDS"
	maxBuildContextFilesEnv = "CMGR_MAX_BUILD_CONTEXT_FILES"
	maxBuildContextBytesEnv = "CMGR_MAX_BUILD_CONTEXT_BYTES"
	maxArtifactFilesEnv     = "CMGR_MAX_ARTIFACT_FILES"
	maxArtifactBytesEnv     = "CMGR_MAX_ARTIFACT_BYTES"
	maxArtifactFileBytesEnv = "CMGR_MAX_ARTIFACT_FILE_BYTES"
	maxRequestBytesEnv      = "CMGR_MAX_REQUEST_BYTES"
	solverTimeoutEnv        = "CMGR_SOLVER_TIMEOUT"
	maxSolverLogBytesEnv    = "CMGR_MAX_SOLVER_LOG_BYTES"
	maxSolverFlagBytesEnv   = "CMGR_MAX_SOLVER_FLAG_BYTES"
)

type managerPolicy struct {
	MaxSeedsPerRequest   int
	MaxConcurrentBuilds  int
	MaxBuildContextFiles int
	MaxBuildContextBytes int64
	MaxArtifactFiles     int
	MaxArtifactBytes     int64
	MaxArtifactFileBytes int64
	MaxRequestBytes      int64
	SolverTimeout        time.Duration
	MaxSolverLogBytes    int64
	MaxSolverFlagBytes   int64
}

func envString(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}

func positiveEnvInt(name string, fallback int) (int, error) {
	value := envString(name, strconv.Itoa(fallback))
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", name, value)
	}
	return parsed, nil
}

func positiveEnvBytes(name, fallback string) (int64, error) {
	value := envString(name, fallback)
	parsed, err := units.RAMInBytes(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive byte size, got %q", name, value)
	}
	return parsed, nil
}

func (m *Manager) initPolicy() error {
	cpus := envString(defaultCPUsEnv, "1")
	nanoCPUs, err := parseNanoCPUs(cpus)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", defaultCPUsEnv, err)
	}
	if nanoCPUs <= 0 {
		return fmt.Errorf("%s must be greater than zero, got %q", defaultCPUsEnv, cpus)
	}
	memory := envString(defaultMemoryEnv, "512m")
	if memoryBytes, err := units.RAMInBytes(memory); err != nil || memoryBytes <= 0 {
		return fmt.Errorf("%s must be a positive byte size, got %q", defaultMemoryEnv, memory)
	}
	pidsLimit, err := positiveEnvInt(defaultPidsLimitEnv, 256)
	if err != nil {
		return err
	}
	nofile, err := positiveEnvInt(defaultNofileEnv, 4096)
	if err != nil {
		return err
	}
	m.runtimeDefaults = ContainerOptions{
		Cpus:      cpus,
		Memory:    memory,
		PidsLimit: int64(pidsLimit),
		Ulimits:   []string{fmt.Sprintf("nofile=%d:%d", nofile, nofile)},
	}

	if m.policy.MaxSeedsPerRequest, err = positiveEnvInt(maxSeedsPerRequestEnv, 10_000); err != nil {
		return err
	}
	if m.policy.MaxConcurrentBuilds, err = positiveEnvInt(maxConcurrentBuildsEnv, 4); err != nil {
		return err
	}
	if m.policy.MaxBuildContextFiles, err = positiveEnvInt(maxBuildContextFilesEnv, 10_000); err != nil {
		return err
	}
	if m.policy.MaxBuildContextBytes, err = positiveEnvBytes(maxBuildContextBytesEnv, "2g"); err != nil {
		return err
	}
	if m.policy.MaxArtifactFiles, err = positiveEnvInt(maxArtifactFilesEnv, 10_000); err != nil {
		return err
	}
	if m.policy.MaxArtifactBytes, err = positiveEnvBytes(maxArtifactBytesEnv, "5g"); err != nil {
		return err
	}
	if m.policy.MaxArtifactFileBytes, err = positiveEnvBytes(maxArtifactFileBytesEnv, "1g"); err != nil {
		return err
	}
	if m.policy.MaxRequestBytes, err = positiveEnvBytes(maxRequestBytesEnv, "1m"); err != nil {
		return err
	}
	timeoutValue := envString(solverTimeoutEnv, "5m")
	m.policy.SolverTimeout, err = time.ParseDuration(timeoutValue)
	if err != nil || m.policy.SolverTimeout <= 0 {
		return fmt.Errorf("%s must be a positive duration, got %q", solverTimeoutEnv, timeoutValue)
	}
	if m.policy.MaxSolverLogBytes, err = positiveEnvBytes(maxSolverLogBytesEnv, "1m"); err != nil {
		return err
	}
	if m.policy.MaxSolverFlagBytes, err = positiveEnvBytes(maxSolverFlagBytesEnv, "4k"); err != nil {
		return err
	}
	m.buildSlots = make(chan struct{}, m.policy.MaxConcurrentBuilds)
	return nil
}

func mergeRuntimeDefaults(
	defaults ContainerOptions,
	challenge ContainerOptions,
) ContainerOptions {
	if challenge.Cpus == "" {
		challenge.Cpus = defaults.Cpus
	}
	if challenge.Memory == "" {
		challenge.Memory = defaults.Memory
	}
	if challenge.PidsLimit == 0 {
		challenge.PidsLimit = defaults.PidsLimit
	}
	if len(defaults.Ulimits) != 0 {
		present := make(map[string]struct{}, len(challenge.Ulimits))
		for _, limit := range challenge.Ulimits {
			name := limit
			if idx := strings.IndexByte(name, '='); idx >= 0 {
				name = name[:idx]
			}
			present[name] = struct{}{}
		}
		for _, limit := range defaults.Ulimits {
			name := limit
			if idx := strings.IndexByte(name, '='); idx >= 0 {
				name = name[:idx]
			}
			if _, overridden := present[name]; !overridden {
				challenge.Ulimits = append(challenge.Ulimits, limit)
			}
		}
	}
	return challenge
}

func (m *Manager) validateSeedLimit(count int) error {
	if m.policy.MaxSeedsPerRequest > 0 && count > m.policy.MaxSeedsPerRequest {
		return fmt.Errorf(
			"request contains %d seeds; maximum is %d (%s)",
			count,
			m.policy.MaxSeedsPerRequest,
			maxSeedsPerRequestEnv,
		)
	}
	return nil
}
