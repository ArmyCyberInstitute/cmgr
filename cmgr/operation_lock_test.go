package cmgr

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type operationLockResult struct {
	release func()
	err     error
}

const operationLockHelperEnvironment = "CMGR_TEST_LOCK_HELPER"

type operationLockHelperProcess struct {
	command  *exec.Cmd
	input    io.WriteCloser
	acquired chan error
	stderr   bytes.Buffer
}

func startOperationLockHelperProcess(
	t *testing.T,
	lockPath string,
	kind string,
	exclusive bool,
) *operationLockHelperProcess {
	t.Helper()
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestOperationLockHelperProcess$",
	)
	command.Env = append(
		os.Environ(),
		operationLockHelperEnvironment+"="+kind,
		"CMGR_TEST_LOCK_PATH="+lockPath,
		fmt.Sprintf("CMGR_TEST_LOCK_EXCLUSIVE=%t", exclusive),
	)
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	process := &operationLockHelperProcess{
		command:  command,
		input:    input,
		acquired: make(chan error, 1),
	}
	command.Stderr = &process.stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		line, err := bufio.NewReader(output).ReadString('\n')
		if err == nil && strings.TrimSpace(line) != "acquired" {
			err = fmt.Errorf("unexpected helper output %q", line)
		}
		process.acquired <- err
	}()
	t.Cleanup(func() {
		_ = command.Process.Kill()
	})
	return process
}

func (process *operationLockHelperProcess) waitForAcquisition(
	t *testing.T,
) {
	t.Helper()
	select {
	case err := <-process.acquired:
		if err != nil {
			t.Fatalf(
				"lock helper did not acquire its lock: %v\n%s",
				err,
				process.stderr.String(),
			)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf(
			"lock helper timed out\n%s",
			process.stderr.String(),
		)
	}
}

func (process *operationLockHelperProcess) release(t *testing.T) {
	t.Helper()
	if _, err := process.input.Write([]byte{'\n'}); err != nil {
		t.Fatal(err)
	}
	if err := process.input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := process.command.Wait(); err != nil {
		t.Fatalf("lock helper failed: %v\n%s", err, process.stderr.String())
	}
}

func TestOperationLockHelperProcess(t *testing.T) {
	kind := os.Getenv(operationLockHelperEnvironment)
	if kind == "" {
		return
	}
	manager := &Manager{operationLockPath: os.Getenv("CMGR_TEST_LOCK_PATH")}
	exclusive := os.Getenv("CMGR_TEST_LOCK_EXCLUSIVE") == "true"

	var release func()
	var err error
	switch kind {
	case "operation":
		release, err = manager.acquireOperationLock(exclusive)
	case "port":
		release, err = manager.acquirePortAllocationLock()
	default:
		t.Fatalf("unknown lock helper kind %q", kind)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(os.Stdout, "acquired"); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(os.Stdin).ReadByte(); err != nil {
		t.Fatal(err)
	}
	release()
}

func TestOperationLockExclusiveBlocksOtherManagers(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "cmgr.db"+operationLockSuffix)
	first := &Manager{operationLockPath: lockPath}
	second := &Manager{operationLockPath: lockPath}

	releaseFirst, err := first.acquireOperationLock(true)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()

	acquired := make(chan operationLockResult, 1)
	go func() {
		release, err := second.acquireOperationLock(false)
		acquired <- operationLockResult{release: release, err: err}
	}()

	select {
	case result := <-acquired:
		if result.release != nil {
			result.release()
		}
		t.Fatalf("shared operation passed an exclusive update lock: %v", result.err)
	case <-time.After(100 * time.Millisecond):
	}

	releaseFirst()
	select {
	case result := <-acquired:
		if result.err != nil {
			t.Fatal(result.err)
		}
		result.release()
	case <-time.After(2 * time.Second):
		t.Fatal("shared operation did not proceed after update lock release")
	}
}

func TestOperationLockAllowsConcurrentOrdinaryOperations(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "cmgr.db"+operationLockSuffix)
	first := &Manager{operationLockPath: lockPath}
	second := &Manager{operationLockPath: lockPath}

	releaseFirst, err := first.acquireOperationLock(false)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()

	acquired := make(chan operationLockResult, 1)
	go func() {
		release, err := second.acquireOperationLock(false)
		acquired <- operationLockResult{release: release, err: err}
	}()

	select {
	case result := <-acquired:
		if result.err != nil {
			t.Fatal(result.err)
		}
		result.release()
	case <-time.After(2 * time.Second):
		t.Fatal("two ordinary operations unexpectedly blocked each other")
	}
}

func TestOperationLockQueuedWriterBlocksLaterReader(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "cmgr.db"+operationLockSuffix)
	activeReader := &Manager{operationLockPath: lockPath}
	writer := &Manager{operationLockPath: lockPath}
	lateReader := &Manager{operationLockPath: lockPath}

	releaseActive, err := activeReader.acquireOperationLock(false)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseActive()

	writerResult := make(chan operationLockResult, 1)
	go func() {
		release, err := writer.acquireOperationLock(true)
		writerResult <- operationLockResult{release: release, err: err}
	}()

	// Wait until the writer owns the turnstile and is waiting for the active
	// shared lock. This handshake avoids relying on goroutine scheduling.
	gatePath := operationGatePath(writer)
	deadline := time.Now().Add(2 * time.Second)
	for {
		gate, acquired, gateErr := acquireFileLock(
			gatePath,
			"test operation lock gate",
			syscall.LOCK_EX,
			true,
		)
		if gateErr != nil {
			t.Fatal(gateErr)
		}
		if !acquired {
			break
		}
		releaseFileLock(gate)
		if time.Now().After(deadline) {
			t.Fatal("writer did not acquire the operation lock gate")
		}
		time.Sleep(time.Millisecond)
	}

	readerResult := make(chan operationLockResult, 1)
	go func() {
		release, err := lateReader.acquireOperationLock(false)
		readerResult <- operationLockResult{release: release, err: err}
	}()

	releaseActive()
	var releaseWriter func()
	select {
	case result := <-writerResult:
		if result.err != nil {
			t.Fatal(result.err)
		}
		releaseWriter = result.release
	case <-time.After(2 * time.Second):
		t.Fatal("queued writer did not acquire the operation lock")
	}

	select {
	case result := <-readerResult:
		if result.release != nil {
			result.release()
		}
		t.Fatalf("late reader bypassed queued writer: %v", result.err)
	case <-time.After(100 * time.Millisecond):
	}

	releaseWriter()
	select {
	case result := <-readerResult:
		if result.err != nil {
			t.Fatal(result.err)
		}
		result.release()
	case <-time.After(2 * time.Second):
		t.Fatal("late reader did not proceed after writer release")
	}
}

func TestOperationLockWriterPreferenceAcrossProcesses(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "cmgr.db"+operationLockSuffix)
	activeReader := startOperationLockHelperProcess(
		t,
		lockPath,
		"operation",
		false,
	)
	activeReader.waitForAcquisition(t)

	writer := startOperationLockHelperProcess(
		t,
		lockPath,
		"operation",
		true,
	)

	gatePath := lockPath + operationGateSuffix
	deadline := time.Now().Add(2 * time.Second)
	for {
		gate, acquired, err := acquireFileLock(
			gatePath,
			"test operation lock gate",
			syscall.LOCK_EX,
			true,
		)
		if err != nil {
			t.Fatal(err)
		}
		if !acquired {
			break
		}
		releaseFileLock(gate)
		if time.Now().After(deadline) {
			t.Fatal("writer process did not acquire the operation lock gate")
		}
		time.Sleep(time.Millisecond)
	}

	lateReader := startOperationLockHelperProcess(
		t,
		lockPath,
		"operation",
		false,
	)
	activeReader.release(t)
	writer.waitForAcquisition(t)

	select {
	case err := <-lateReader.acquired:
		t.Fatalf("late reader process bypassed queued writer: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	writer.release(t)
	lateReader.waitForAcquisition(t)
	lateReader.release(t)
}

func TestPortAllocationLockBlocksOtherManagers(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "cmgr.db"+operationLockSuffix)
	first := &Manager{operationLockPath: lockPath}
	second := &Manager{operationLockPath: lockPath}

	releaseFirst, err := first.acquirePortAllocationLock()
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()

	acquired := make(chan operationLockResult, 1)
	go func() {
		release, err := second.acquirePortAllocationLock()
		acquired <- operationLockResult{release: release, err: err}
	}()

	select {
	case result := <-acquired:
		if result.release != nil {
			result.release()
		}
		t.Fatalf("second manager passed the port allocation lock: %v", result.err)
	case <-time.After(100 * time.Millisecond):
	}

	releaseFirst()
	select {
	case result := <-acquired:
		if result.err != nil {
			t.Fatal(result.err)
		}
		result.release()
	case <-time.After(2 * time.Second):
		t.Fatal("second manager did not acquire the released port allocation lock")
	}
}

func TestPortAllocationLocksAreScopedToDatabase(t *testing.T) {
	root := t.TempDir()
	first := &Manager{
		operationLockPath: filepath.Join(root, "first.db"+operationLockSuffix),
	}
	second := &Manager{
		operationLockPath: filepath.Join(root, "second.db"+operationLockSuffix),
	}

	releaseFirst, err := first.acquirePortAllocationLock()
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()

	releaseSecond, err := second.acquirePortAllocationLock()
	if err != nil {
		t.Fatal(err)
	}
	releaseSecond()
}

func TestPortAllocationLockBlocksOtherProcesses(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "cmgr.db"+operationLockSuffix)
	first := startOperationLockHelperProcess(t, lockPath, "port", false)
	first.waitForAcquisition(t)
	second := startOperationLockHelperProcess(t, lockPath, "port", false)

	select {
	case err := <-second.acquired:
		t.Fatalf("second process passed the port allocation lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	first.release(t)
	second.waitForAcquisition(t)
	second.release(t)
}

func TestStartupLockFallsBackToSharedDuringOrdinaryOperation(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "cmgr.db"+operationLockSuffix)
	active := &Manager{operationLockPath: lockPath}
	starting := &Manager{operationLockPath: lockPath}

	releaseActive, err := active.acquireOperationLock(false)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseActive()

	releaseStartup, exclusive, err := starting.acquireStartupOperationLock()
	if err != nil {
		t.Fatal(err)
	}
	defer releaseStartup()
	if exclusive {
		t.Fatal("startup unexpectedly acquired exclusive access during an ordinary operation")
	}
}

func TestStartupLockWaitsForExclusiveStartup(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "cmgr.db"+operationLockSuffix)
	first := &Manager{operationLockPath: lockPath}
	second := &Manager{operationLockPath: lockPath}

	releaseFirst, err := first.acquireOperationLock(true)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()

	acquired := make(chan operationLockResult, 1)
	go func() {
		release, _, err := second.acquireStartupOperationLock()
		acquired <- operationLockResult{release: release, err: err}
	}()

	select {
	case result := <-acquired:
		if result.release != nil {
			result.release()
		}
		t.Fatalf("startup passed another exclusive startup lock: %v", result.err)
	case <-time.After(100 * time.Millisecond):
	}

	releaseFirst()
	select {
	case result := <-acquired:
		if result.err != nil {
			t.Fatal(result.err)
		}
		result.release()
	case <-time.After(2 * time.Second):
		t.Fatal("startup did not fall back to shared access after migration lock release")
	}
}

func TestStartupLockUsesExclusiveAccessWhenAvailable(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "cmgr.db"+operationLockSuffix)
	manager := &Manager{operationLockPath: lockPath}

	release, exclusive, err := manager.acquireStartupOperationLock()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if !exclusive {
		t.Fatal("uncontended startup did not acquire exclusive access")
	}
}

func TestInMemoryDatabaseUsesOnlyTheManagerLock(t *testing.T) {
	path, err := databaseOperationLockPath(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Fatalf("in-memory database unexpectedly has lock path %q", path)
	}
}
