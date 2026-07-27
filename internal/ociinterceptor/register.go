package ociinterceptor

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

const (
	// RegisterSubcommand adds cmgr-oci-interceptor to Docker's daemon
	// configuration.
	RegisterSubcommand = "register"

	defaultDockerConfigPath = "/etc/docker/daemon.json"
	maxDockerConfigSize     = 16 * 1024 * 1024

	// RegistrationCommand is the command shown when the Docker runtime is not
	// available.
	RegistrationCommand = "sudo " + RuntimeName + " " + RegisterSubcommand
)

type dockerRuntimeRegistration struct {
	Path        string   `json:"path"`
	RuntimeArgs []string `json:"runtimeArgs,omitempty"`
}

// RunRegisterCommand registers cmgr-oci-interceptor as a named Docker runtime
// and reloads Docker so the change takes effect.
func RunRegisterCommand(
	arguments []string,
	invokedExecutable string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	flags := flag.NewFlagSet(RegisterSubcommand, flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String(
		"config",
		defaultDockerConfigPath,
		"path to Docker's daemon.json",
	)
	runtimePath := flags.String(
		"runtime-path",
		"",
		"absolute path to cmgr-oci-interceptor (defaults to the invoked executable)",
	)
	runcPath := flags.String(
		"runc-path",
		"runc",
		"path to the real OCI runtime (defaults to resolving runc on PATH)",
	)
	force := flags.Bool(
		"force",
		false,
		"replace a conflicting cmgr runtime registration",
	)
	noReload := flags.Bool(
		"no-reload",
		false,
		"write the configuration without reloading Docker",
	)
	flags.Usage = func() {
		fmt.Fprintf(
			flags.Output(),
			"Usage: %s %s [<options>]\n",
			invokedExecutable,
			RegisterSubcommand,
		)
		flags.PrintDefaults()
	}

	if err := flags.Parse(arguments); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "%s does not accept positional arguments\n", RegisterSubcommand)
		flags.Usage()
		return 2
	}
	if runtime.GOOS != "linux" {
		fmt.Fprintln(stderr, "Docker OCI runtime registration is only supported on Linux")
		return 1
	}
	privilegedRegistration := filepath.Clean(*configPath) == defaultDockerConfigPath
	if privilegedRegistration && os.Geteuid() != 0 {
		fmt.Fprintf(stderr, "%s must be run as root\n", RegistrationCommand)
		return 1
	}

	resolvedRuntimePath, err := resolveInterceptorExecutable(*runtimePath)
	if err != nil {
		fmt.Fprintf(stderr, "could not resolve cmgr-oci-interceptor executable: %s\n", err)
		return 1
	}
	resolvedRuncPath, err := resolveRuntimeExecutable(*runcPath)
	if err != nil {
		fmt.Fprintf(stderr, "could not resolve real OCI runtime executable: %s\n", err)
		return 1
	}
	if privilegedRegistration {
		if err = validateTrustedExecutable(resolvedRuntimePath); err != nil {
			fmt.Fprintf(stderr, "refusing unsafe interceptor executable: %s\n", err)
			return 1
		}
		if err = validateTrustedExecutable(resolvedRuncPath); err != nil {
			fmt.Fprintf(stderr, "refusing unsafe real OCI runtime executable: %s\n", err)
			return 1
		}
	}
	if err = os.MkdirAll(filepath.Dir(*configPath), 0755); err != nil {
		fmt.Fprintf(stderr, "could not create Docker configuration directory: %s\n", err)
		return 1
	}
	if privilegedRegistration {
		if err = validateRootOwnedPath(filepath.Dir(*configPath), true); err != nil {
			fmt.Fprintf(stderr, "refusing unsafe Docker configuration directory: %s\n", err)
			return 1
		}
		if _, statErr := os.Lstat(*configPath); statErr == nil {
			if err = validateRootOwnedPath(*configPath, false); err != nil {
				fmt.Fprintf(stderr, "refusing unsafe Docker configuration: %s\n", err)
				return 1
			}
		} else if !os.IsNotExist(statErr) {
			fmt.Fprintf(stderr, "could not inspect Docker configuration: %s\n", statErr)
			return 1
		}
	}
	lock, err := acquireRegistrationLock(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "could not lock Docker runtime configuration: %s\n", err)
		return 1
	}
	defer releaseRegistrationLock(lock)
	original, err := snapshotFile(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "could not snapshot Docker runtime configuration: %s\n", err)
		return 1
	}

	changed, err := RegisterRuntime(
		*configPath,
		resolvedRuntimePath,
		resolvedRuncPath,
		*force,
	)
	if err != nil {
		fmt.Fprintf(stderr, "could not register Docker runtime: %s\n", err)
		return 1
	}
	if changed {
		output, supported, validationErr := validateDockerConfiguration(
			*configPath,
			privilegedRegistration,
		)
		if supported && validationErr != nil {
			restoreErr := restoreFile(*configPath, original)
			detail := strings.TrimSpace(string(output))
			if detail != "" {
				detail = ": " + detail
			}
			fmt.Fprintf(
				stderr,
				"Docker rejected the updated daemon configuration: %s%s\n",
				validationErr,
				detail,
			)
			if restoreErr != nil {
				fmt.Fprintf(
					stderr,
					"Automatic rollback failed: %s. Restore %s before reloading Docker.\n",
					restoreErr,
					*configPath,
				)
			} else {
				fmt.Fprintln(stderr, "The previous Docker configuration was restored.")
			}
			return 1
		}
	}

	if *noReload {
		if changed {
			fmt.Fprintf(stdout, "registered Docker runtime %q in %s\n", RuntimeName, *configPath)
		} else {
			fmt.Fprintf(stdout, "Docker runtime %q is already configured in %s\n", RuntimeName, *configPath)
		}
		fmt.Fprintln(stdout, "Reload Docker to make it available:")
		fmt.Fprintln(stdout, "  sudo systemctl reload docker")
		return 0
	}

	output, err := reloadDocker(privilegedRegistration)
	if err != nil {
		rollbackErr := rollbackRegistration(
			*configPath,
			original,
			changed,
			privilegedRegistration,
		)
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			detail = ": " + detail
		}
		fmt.Fprintf(
			stderr,
			"Docker runtime configuration was written, but Docker could not be reloaded: %s%s\n",
			err,
			detail,
		)
		reportRollback(stderr, rollbackErr)
		return 1
	}
	if err = waitForRuntimeRegistration(
		resolvedRuntimePath,
		runtimeArguments(resolvedRuncPath),
		5*time.Second,
	); err != nil {
		rollbackErr := rollbackRegistration(
			*configPath,
			original,
			changed,
			privilegedRegistration,
		)
		fmt.Fprintf(
			stderr,
			"Docker reloaded but did not adopt the expected runtime configuration: %s\n",
			err,
		)
		reportRollback(stderr, rollbackErr)
		return 1
	}

	if changed {
		fmt.Fprintf(stdout, "registered Docker runtime %q and reloaded Docker\n", RuntimeName)
	} else {
		fmt.Fprintf(stdout, "Docker runtime %q was already configured; reloaded Docker\n", RuntimeName)
	}
	return 0
}

// RegisterRuntime safely merges cmgr's named runtime into a Docker daemon
// configuration. Existing unrelated daemon settings and runtimes are retained.
func RegisterRuntime(
	configPath string,
	runtimePath string,
	runcPath string,
	force bool,
) (bool, error) {
	if configPath == "" {
		return false, fmt.Errorf("Docker configuration path cannot be empty")
	}
	if !filepath.IsAbs(runtimePath) {
		return false, fmt.Errorf("interceptor runtime path must be absolute: %q", runtimePath)
	}
	if !shellSafePath(runtimePath) {
		return false, fmt.Errorf(
			"interceptor runtime path contains characters unsafe for Docker: %q",
			runtimePath,
		)
	}
	if runcPath == "" {
		return false, fmt.Errorf("runc path cannot be empty")
	}
	if !filepath.IsAbs(runcPath) {
		return false, fmt.Errorf("runc path must be absolute: %q", runcPath)
	}
	if !shellSafePath(runcPath) {
		return false, fmt.Errorf("runc path contains characters unsafe for Docker runtimeArgs: %q", runcPath)
	}
	if err := validateExecutable(runtimePath); err != nil {
		return false, err
	}

	document := make(map[string]json.RawMessage)
	mode := os.FileMode(0644)
	info, err := os.Lstat(configPath)
	switch {
	case err == nil:
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("%s is not a regular file", configPath)
		}
		if info.Size() > maxDockerConfigSize {
			return false, fmt.Errorf(
				"%s exceeds the %d byte size limit",
				configPath,
				maxDockerConfigSize,
			)
		}
		mode = info.Mode().Perm()
		contents, readErr := ioutil.ReadFile(configPath)
		if readErr != nil {
			return false, fmt.Errorf("could not read %s: %v", configPath, readErr)
		}
		if err = json.Unmarshal(contents, &document); err != nil {
			return false, fmt.Errorf("could not parse %s: %v", configPath, err)
		}
		if document == nil {
			return false, fmt.Errorf("%s does not contain a JSON object", configPath)
		}
	case os.IsNotExist(err):
	default:
		return false, fmt.Errorf("could not inspect %s: %v", configPath, err)
	}

	runtimes := make(map[string]json.RawMessage)
	if rawRuntimes, ok := document["runtimes"]; ok {
		if err = json.Unmarshal(rawRuntimes, &runtimes); err != nil {
			return false, fmt.Errorf("Docker configuration field %q is not an object", "runtimes")
		}
		if runtimes == nil {
			return false, fmt.Errorf("Docker configuration field %q is not an object", "runtimes")
		}
	}

	registration := dockerRuntimeRegistration{
		Path:        runtimePath,
		RuntimeArgs: runtimeArguments(runcPath),
	}
	rawRegistration, err := json.Marshal(registration)
	if err != nil {
		return false, fmt.Errorf("could not encode runtime registration: %v", err)
	}

	if existing, ok := runtimes[RuntimeName]; ok {
		equal, compareErr := equalJSON(existing, rawRegistration)
		if compareErr != nil {
			return false, fmt.Errorf(
				"could not parse existing Docker runtime %q: %v",
				RuntimeName,
				compareErr,
			)
		}
		if equal {
			return false, nil
		}
		if !force {
			return false, fmt.Errorf(
				"Docker runtime %q already has a different configuration; inspect it or rerun with --force",
				RuntimeName,
			)
		}
	}
	runtimes[RuntimeName] = rawRegistration

	rawRuntimes, err := json.Marshal(runtimes)
	if err != nil {
		return false, fmt.Errorf("could not encode Docker runtimes: %v", err)
	}
	document["runtimes"] = rawRuntimes
	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return false, fmt.Errorf("could not encode Docker configuration: %v", err)
	}
	updated = append(updated, '\n')

	if err = os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return false, fmt.Errorf("could not create Docker configuration directory: %v", err)
	}
	if err = replaceFile(configPath, updated, mode); err != nil {
		return false, fmt.Errorf("could not write %s: %v", configPath, err)
	}
	if err = syncDirectory(filepath.Dir(configPath)); err != nil {
		return false, fmt.Errorf("could not sync Docker configuration directory: %v", err)
	}
	return true, nil
}

func resolveInterceptorExecutable(explicitPath string) (string, error) {
	path := explicitPath
	if path != "" {
		if !filepath.IsAbs(explicitPath) {
			return "", fmt.Errorf("runtime path must be absolute: %q", explicitPath)
		}
	} else {
		var err error
		path, err = os.Executable()
		if err != nil {
			return "", fmt.Errorf("could not resolve invoked executable: %v", err)
		}
	}
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err = validateExecutable(absolute); err != nil {
		return "", err
	}
	if !shellSafePath(absolute) {
		return "", fmt.Errorf(
			"runtime path contains unsupported characters: %q",
			absolute,
		)
	}
	return absolute, nil
}

func resolveRuntimeExecutable(path string) (string, error) {
	resolved := path
	var err error
	if !filepath.IsAbs(resolved) {
		resolved, err = exec.LookPath(resolved)
		if err != nil {
			return "", err
		}
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	if err = validateExecutable(resolved); err != nil {
		return "", err
	}
	if !shellSafePath(resolved) {
		return "", fmt.Errorf("runtime path contains unsupported characters: %q", resolved)
	}
	return resolved, nil
}

func validateExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("could not inspect runtime executable %s: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("runtime executable %s is not a regular file", path)
	}
	if info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("runtime executable %s is not executable", path)
	}
	return nil
}

func runtimeArguments(runcPath string) []string {
	return []string{
		RuntimeProtocolArgument,
		runtimePathOption + "=" + runcPath,
	}
}

func shellSafePath(path string) bool {
	if path == "" {
		return false
	}
	for _, character := range path {
		isLetter := character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z'
		isNumber := character >= '0' && character <= '9'
		if !isLetter && !isNumber &&
			character != '/' && character != '.' &&
			character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func validateTrustedExecutable(path string) error {
	if err := validateExecutable(path); err != nil {
		return err
	}
	if err := validateRootOwnedPath(path, false); err != nil {
		return err
	}
	for directory := filepath.Dir(path); ; directory = filepath.Dir(directory) {
		if err := validateRootOwnedPath(directory, true); err != nil {
			return err
		}
		if directory == string(os.PathSeparator) {
			break
		}
	}
	return nil
}

func validateRootOwnedPath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symbolic link", path)
	}
	if directory && !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("could not determine ownership of %s", path)
	}
	if stat.Uid != 0 {
		return fmt.Errorf("%s is not owned by root", path)
	}
	if info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("%s is group- or world-writable", path)
	}
	return nil
}

func acquireRegistrationLock(configPath string) (*os.File, error) {
	lock, err := os.OpenFile(
		configPath+".cmgr.lock",
		os.O_CREATE|os.O_RDWR,
		0600,
	)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		lock.Close()
		return nil, err
	}
	return lock, nil
}

func releaseRegistrationLock(lock *os.File) {
	if lock == nil {
		return
	}
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	_ = lock.Close()
}

type registrationFileSnapshot struct {
	Exists   bool
	Contents []byte
	Mode     os.FileMode
}

func snapshotFile(path string) (registrationFileSnapshot, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return registrationFileSnapshot{}, nil
	}
	if err != nil {
		return registrationFileSnapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return registrationFileSnapshot{}, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > maxDockerConfigSize {
		return registrationFileSnapshot{}, fmt.Errorf(
			"%s exceeds the %d byte size limit",
			path,
			maxDockerConfigSize,
		)
	}
	contents, err := ioutil.ReadFile(path)
	if err != nil {
		return registrationFileSnapshot{}, err
	}
	return registrationFileSnapshot{
		Exists:   true,
		Contents: contents,
		Mode:     info.Mode().Perm(),
	}, nil
}

func restoreFile(path string, snapshot registrationFileSnapshot) error {
	if snapshot.Exists {
		if err := replaceFile(path, snapshot.Contents, snapshot.Mode); err != nil {
			return err
		}
		return syncDirectory(filepath.Dir(path))
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func reloadDocker(requireTrustedPath bool) ([]byte, error) {
	var systemctlPath string
	for _, candidate := range []string{"/usr/bin/systemctl", "/bin/systemctl"} {
		if validateExecutable(candidate) == nil {
			systemctlPath = candidate
			break
		}
	}
	if systemctlPath == "" {
		return nil, fmt.Errorf("could not find systemctl at a trusted absolute path")
	}
	if requireTrustedPath {
		if err := validateTrustedExecutable(systemctlPath); err != nil {
			return nil, err
		}
	}
	command := exec.Command(systemctlPath, "reload", "docker")
	command.Env = []string{
		"LANG=C",
		"PATH=/usr/sbin:/usr/bin:/sbin:/bin",
	}
	return command.CombinedOutput()
}

func validateDockerConfiguration(
	configPath string,
	requireTrustedPath bool,
) ([]byte, bool, error) {
	var dockerdPath string
	for _, candidate := range []string{
		"/usr/bin/dockerd",
		"/usr/sbin/dockerd",
		"/bin/dockerd",
		"/sbin/dockerd",
	} {
		if validateExecutable(candidate) == nil {
			dockerdPath = candidate
			break
		}
	}
	if dockerdPath == "" {
		return nil, false, nil
	}
	if requireTrustedPath {
		if err := validateTrustedExecutable(dockerdPath); err != nil {
			return nil, true, err
		}
	}

	environment := []string{
		"LANG=C",
		"PATH=/usr/sbin:/usr/bin:/sbin:/bin",
	}
	helpCommand := exec.Command(dockerdPath, "--help")
	helpCommand.Env = environment
	help, err := helpCommand.CombinedOutput()
	if err != nil {
		return help, true, fmt.Errorf("could not inspect dockerd validation support: %v", err)
	}
	if !strings.Contains(string(help), "--validate") {
		return nil, false, nil
	}

	command := exec.Command(
		dockerdPath,
		"--validate",
		"--config-file="+configPath,
	)
	command.Env = environment
	output, err := command.CombinedOutput()
	return output, true, err
}

func waitForRuntimeRegistration(
	expectedPath string,
	expectedArguments []string,
	timeout time.Duration,
) error {
	cli, err := client.NewClientWithOpts(
		client.WithHost("unix:///var/run/docker.sock"),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return err
	}
	defer cli.Close()

	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		info, infoErr := cli.Info(ctx)
		cancel()
		if infoErr == nil {
			if runtimeRegistrationMatches(info, expectedPath, expectedArguments) {
				return nil
			}
			lastErr = fmt.Errorf("Docker reports a different or incomplete runtime entry")
		} else {
			lastErr = infoErr
		}
		if time.Now().After(deadline) {
			return lastErr
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func runtimeRegistrationMatches(
	info types.Info,
	expectedPath string,
	expectedArguments []string,
) bool {
	registration, ok := info.Runtimes[RuntimeName]
	return ok &&
		registration.Path == expectedPath &&
		reflect.DeepEqual(registration.Args, expectedArguments)
}

func rollbackRegistration(
	configPath string,
	original registrationFileSnapshot,
	changed bool,
	requireTrustedPath bool,
) error {
	if changed {
		if err := restoreFile(configPath, original); err != nil {
			return fmt.Errorf("could not restore Docker configuration: %v", err)
		}
	}
	if _, err := reloadDocker(requireTrustedPath); err != nil {
		return fmt.Errorf("could not reload Docker after restoring its configuration: %v", err)
	}
	return nil
}

func reportRollback(stderr io.Writer, err error) {
	if err != nil {
		fmt.Fprintf(
			stderr,
			"Automatic rollback failed: %s. Inspect the Docker configuration before restarting Docker.\n",
			err,
		)
		return
	}
	fmt.Fprintln(stderr, "The previous Docker configuration was restored and reloaded.")
}

func equalJSON(left []byte, right []byte) (bool, error) {
	var leftValue interface{}
	if err := json.Unmarshal(left, &leftValue); err != nil {
		return false, err
	}
	var rightValue interface{}
	if err := json.Unmarshal(right, &rightValue); err != nil {
		return false, err
	}
	return reflect.DeepEqual(leftValue, rightValue), nil
}
