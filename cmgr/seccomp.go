package cmgr

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"

	"github.com/ArmyCyberInstitute/cmgr/internal/ociinterceptor"
)

const (
	seccompTweakAllowDisableASLR = ociinterceptor.TweakAllowDisableASLR
	maxSeccompProfileSize        = 1024 * 1024
)

// seccompPolicy is the profile historically applied to every Linux container.
// It is retained only for challenges that explicitly select legacy behavior.
//
//go:embed seccomp.json
var seccompPolicy string

type seccompArgument struct {
	Index    uint   `json:"index"`
	Value    uint64 `json:"value"`
	ValueTwo uint64 `json:"valueTwo,omitempty"`
	Op       string `json:"op"`
}

type seccompSyscall struct {
	Name     string            `json:"name,omitempty"`
	Names    []string          `json:"names"`
	Action   string            `json:"action"`
	ErrnoRet *uint             `json:"errnoRet,omitempty"`
	Args     []seccompArgument `json:"args,omitempty"`
}

type seccompProfile struct {
	DefaultAction   string           `json:"defaultAction"`
	DefaultErrnoRet *uint            `json:"defaultErrnoRet,omitempty"`
	Syscalls        []seccompSyscall `json:"syscalls"`
}

type persistedSeccompOptions struct {
	Options          *SeccompOptions `json:"options,omitempty"`
	EffectiveProfile string          `json:"effective_profile,omitempty"`
}

func (opts *SeccompOptions) resolve(challengeDir string) error {
	opts.ProfileHash = ""
	opts.effectiveProfile = ""

	hasTweaks := len(opts.Tweaks) != 0
	hasProfile := opts.Profile != ""
	selectedModes := 0
	if opts.Legacy {
		selectedModes++
	}
	if hasTweaks {
		selectedModes++
	}
	if hasProfile {
		selectedModes++
	}
	if selectedModes > 1 {
		return fmt.Errorf("seccomp legacy mode, tweaks, and a custom profile are mutually exclusive")
	}

	var profile string
	var err error
	switch {
	case opts.Legacy:
		profile = seccompPolicy
	case hasTweaks:
		opts.Tweaks, err = ociinterceptor.NormalizeTweaks(opts.Tweaks)
		return err
	case hasProfile:
		profile, err = readSeccompProfile(challengeDir, opts.Profile)
	default:
		return nil
	}
	if err != nil {
		return err
	}
	if err = validateSeccompProfile(profile); err != nil {
		return err
	}

	sum := sha256.Sum256([]byte(profile))
	opts.ProfileHash = fmt.Sprintf("%x", sum)
	opts.effectiveProfile = profile
	return nil
}

func readSeccompProfile(challengeDir, profilePath string) (string, error) {
	if err := validateSeccompProfileFilename(profilePath); err != nil {
		return "", err
	}

	root, err := filepath.Abs(challengeDir)
	if err != nil {
		return "", fmt.Errorf("could not resolve challenge directory for seccomp profile: %v", err)
	}
	candidate := filepath.Join(root, profilePath)
	info, err := os.Lstat(candidate)
	if err != nil {
		return "", fmt.Errorf("could not inspect seccomp profile '%s': %v", profilePath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("seccomp profile '%s' must not be a symbolic link", profilePath)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("seccomp profile '%s' is not a regular file", profilePath)
	}
	if info.Size() > maxSeccompProfileSize {
		return "", fmt.Errorf("seccomp profile '%s' exceeds the %d byte size limit", profilePath, maxSeccompProfileSize)
	}

	file, err := os.Open(candidate)
	if err != nil {
		return "", fmt.Errorf("could not open seccomp profile '%s': %v", profilePath, err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("could not inspect open seccomp profile '%s': %v", profilePath, err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return "", fmt.Errorf("seccomp profile '%s' changed while it was being opened", profilePath)
	}
	data, err := ioutil.ReadAll(io.LimitReader(file, maxSeccompProfileSize+1))
	if err != nil {
		return "", fmt.Errorf("could not read seccomp profile '%s': %v", profilePath, err)
	}
	if len(data) > maxSeccompProfileSize {
		return "", fmt.Errorf("seccomp profile '%s' exceeds the %d byte size limit", profilePath, maxSeccompProfileSize)
	}
	return string(data), nil
}

func validateSeccompProfileFilename(profilePath string) error {
	if profilePath == "" {
		return fmt.Errorf("seccomp profile filename cannot be empty")
	}
	if filepath.IsAbs(profilePath) || filepath.Base(profilePath) != profilePath {
		return fmt.Errorf("seccomp profile must be a JSON file in the challenge directory")
	}
	if profilePath[0] == '.' {
		return fmt.Errorf("seccomp profile filename must not start with '.'")
	}
	if filepath.Ext(profilePath) != ".json" {
		return fmt.Errorf("seccomp profile filename must end with '.json'")
	}
	if profilePath == "problem.json" {
		return fmt.Errorf("problem.json cannot also be used as a seccomp profile")
	}
	for _, character := range profilePath {
		isLetter := character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z'
		isNumber := character >= '0' && character <= '9'
		if !isLetter && !isNumber &&
			character != '_' && character != '-' && character != '.' {
			return fmt.Errorf(
				"seccomp profile filename %q contains unsupported characters",
				profilePath,
			)
		}
	}
	return nil
}

func validateSeccompProfile(profile string) error {
	var document seccompProfile
	if err := json.Unmarshal([]byte(profile), &document); err != nil {
		return fmt.Errorf("invalid seccomp profile JSON: %v", err)
	}
	if document.DefaultAction == "" {
		return fmt.Errorf("invalid seccomp profile: defaultAction must be a non-empty string")
	}
	for i, syscall := range document.Syscalls {
		if syscall.Name != "" && len(syscall.Names) != 0 {
			return fmt.Errorf("invalid seccomp profile: syscall rule %d specifies both name and names", i)
		}
		if syscall.Name == "" && len(syscall.Names) == 0 {
			return fmt.Errorf("invalid seccomp profile: syscall rule %d does not specify any names", i)
		}
		if syscall.Action == "" {
			return fmt.Errorf("invalid seccomp profile: syscall rule %d does not specify an action", i)
		}
		for j, argument := range syscall.Args {
			if argument.Op == "" {
				return fmt.Errorf("invalid seccomp profile: argument %d in syscall rule %d does not specify an operator", j, i)
			}
		}
	}
	return nil
}

func marshalSeccompOptions(options *SeccompOptions) (string, error) {
	if options == nil {
		return "", nil
	}
	data, err := json.Marshal(persistedSeccompOptions{
		Options:          options,
		EffectiveProfile: options.effectiveProfile,
	})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func unmarshalSeccompOptions(data string) (*SeccompOptions, error) {
	if data == "" {
		return nil, nil
	}
	var persisted persistedSeccompOptions
	if err := json.Unmarshal([]byte(data), &persisted); err != nil {
		return nil, err
	}
	if persisted.Options == nil {
		if persisted.EffectiveProfile != "" {
			return nil, fmt.Errorf("persisted seccomp profile has no options")
		}
		return nil, nil
	}
	persisted.Options.effectiveProfile = persisted.EffectiveProfile
	if err := validatePersistedSeccompOptions(persisted.Options); err != nil {
		return nil, err
	}
	return persisted.Options, nil
}

func validatePersistedSeccompOptions(options *SeccompOptions) error {
	hasTweaks := len(options.Tweaks) != 0
	hasProfile := options.Profile != ""
	selectedModes := 0
	if options.Legacy {
		selectedModes++
	}
	if hasTweaks {
		selectedModes++
	}
	if hasProfile {
		selectedModes++
	}
	if selectedModes > 1 {
		return fmt.Errorf("persisted seccomp options select more than one mode")
	}

	switch {
	case hasTweaks:
		normalized, err := ociinterceptor.NormalizeTweaks(options.Tweaks)
		if err != nil {
			return fmt.Errorf("invalid persisted seccomp tweaks: %v", err)
		}
		options.Tweaks = normalized
		if options.effectiveProfile != "" || options.ProfileHash != "" {
			return fmt.Errorf("persisted seccomp tweaks unexpectedly contain a profile snapshot")
		}
	case options.Legacy || hasProfile:
		if hasProfile {
			if err := validateSeccompProfileFilename(options.Profile); err != nil {
				return fmt.Errorf("invalid persisted seccomp profile filename: %v", err)
			}
		}
		if options.effectiveProfile == "" {
			return fmt.Errorf("persisted seccomp profile snapshot is missing")
		}
		if err := validateSeccompProfile(options.effectiveProfile); err != nil {
			return fmt.Errorf("invalid persisted seccomp profile snapshot: %v", err)
		}
		sum := sha256.Sum256([]byte(options.effectiveProfile))
		expectedHash := fmt.Sprintf("%x", sum)
		if options.ProfileHash != expectedHash {
			return fmt.Errorf("persisted seccomp profile hash does not match its snapshot")
		}
	default:
		if options.effectiveProfile != "" || options.ProfileHash != "" {
			return fmt.Errorf("default seccomp options unexpectedly contain a profile snapshot")
		}
	}
	return nil
}

type seccompPolicyFingerprint struct {
	Legacy      bool
	Tweaks      []string
	Profile     string
	ProfileHash string
}

func challengeSeccompPolicies(options ChallengeOptions) map[string]seccompPolicyFingerprint {
	policies := make(map[string]seccompPolicyFingerprint)
	for host, containerOptions := range options.Overrides {
		if containerOptions.Seccomp == nil {
			continue
		}
		policy := seccompPolicyFingerprint{
			Legacy:      containerOptions.Seccomp.Legacy,
			Tweaks:      append([]string(nil), containerOptions.Seccomp.Tweaks...),
			Profile:     containerOptions.Seccomp.Profile,
			ProfileHash: containerOptions.Seccomp.ProfileHash,
		}
		policies[host] = policy
	}
	return policies
}

func seccompPoliciesEqual(left, right ChallengeOptions) bool {
	return reflect.DeepEqual(
		challengeSeccompPolicies(left),
		challengeSeccompPolicies(right),
	)
}
