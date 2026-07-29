package cmgr

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

const operationLockSuffix = ".cmgr.lock"
const operationGateSuffix = ".gate"
const portLockSuffix = ".ports"

type localPortLock struct {
	mu   sync.Mutex
	refs int
}

var localPortLocks = struct {
	sync.Mutex
	locks map[string]*localPortLock
}{
	locks: make(map[string]*localPortLock),
}

func canonicalDatabasePath(databasePath string) (string, error) {
	if databasePath == "" || databasePath == ":memory:" {
		return "", nil
	}
	absolute, err := filepath.Abs(databasePath)
	if err != nil {
		return "", fmt.Errorf("could not resolve database path: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = resolved
	} else if !os.IsNotExist(resolveErr) {
		return "", fmt.Errorf("could not resolve database path: %w", resolveErr)
	} else {
		parent, parentErr := filepath.EvalSymlinks(filepath.Dir(absolute))
		if parentErr != nil {
			return "", fmt.Errorf(
				"could not resolve database directory: %w",
				parentErr,
			)
		}
		absolute = filepath.Join(parent, filepath.Base(absolute))
	}
	return absolute, nil
}

// databaseOperationLockPath returns a process-shared lock path next to the
// configured database. Keeping the lock beside the database makes managers
// using the same database converge on the same barrier even when their current
// working directories differ.
func databaseOperationLockPath(databasePath string) (string, error) {
	absolute, err := canonicalDatabasePath(databasePath)
	if err != nil || absolute == "" {
		return "", err
	}
	return absolute + operationLockSuffix, nil
}

func (m *Manager) initOperationLock() error {
	path, err := databaseOperationLockPath(configuredDatabasePath())
	if err != nil {
		return err
	}
	m.operationLockPath = path
	if path != "" {
		m.operationGatePath = path + operationGateSuffix
		m.portLockPath = path + portLockSuffix
	}
	return nil
}

func operationGatePath(m *Manager) string {
	if m.operationGatePath != "" {
		return m.operationGatePath
	}
	if m.operationLockPath == "" {
		return ""
	}
	return m.operationLockPath + operationGateSuffix
}

func portAllocationLockPath(m *Manager) string {
	if m.portLockPath != "" {
		return m.portLockPath
	}
	if m.operationLockPath == "" {
		return ""
	}
	return m.operationLockPath + portLockSuffix
}

func acquireLocalPortLock(path string) func() {
	if path == "" {
		path = ":memory:"
	}
	localPortLocks.Lock()
	lock := localPortLocks.locks[path]
	if lock == nil {
		lock = new(localPortLock)
		localPortLocks.locks[path] = lock
	}
	lock.refs++
	localPortLocks.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		localPortLocks.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(localPortLocks.locks, path)
		}
		localPortLocks.Unlock()
	}
}

func acquireFileLock(
	path string,
	description string,
	mode int,
	nonblocking bool,
) (file *os.File, acquired bool, err error) {
	if info, inspectErr := os.Lstat(path); inspectErr == nil {
		if !info.Mode().IsRegular() {
			return nil, false, fmt.Errorf(
				"%s %s is not a regular file",
				description,
				path,
			)
		}
	} else if !os.IsNotExist(inspectErr) {
		return nil, false, fmt.Errorf(
			"could not inspect %s: %w",
			description,
			inspectErr,
		)
	}

	file, err = os.OpenFile(
		path,
		os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW,
		0600,
	)
	if err != nil {
		return nil, false, fmt.Errorf(
			"could not open %s: %w",
			description,
			err,
		)
	}
	if info, statErr := file.Stat(); statErr != nil {
		_ = file.Close()
		return nil, false, fmt.Errorf(
			"could not inspect %s: %w",
			description,
			statErr,
		)
	} else if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, false, fmt.Errorf(
			"%s %s is not a regular file",
			description,
			path,
		)
	}

	if nonblocking {
		mode |= syscall.LOCK_NB
	}
	for {
		err = syscall.Flock(int(file.Fd()), mode)
		if err != syscall.EINTR {
			break
		}
	}
	if err == nil {
		return file, true, nil
	}
	_ = file.Close()
	if nonblocking &&
		(errors.Is(err, syscall.EWOULDBLOCK) ||
			errors.Is(err, syscall.EAGAIN)) {
		return nil, false, nil
	}
	return nil, false, fmt.Errorf(
		"could not acquire %s: %w",
		description,
		err,
	)
}

func releaseFileLock(file *os.File) {
	if file == nil {
		return
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

// acquirePortAllocationLock serializes the interval from selecting a host port
// through persisting Docker's assignment. The mutex is the inexpensive local
// layer (and the fallback for in-memory databases); flock coordinates separate
// cmgr processes using the same database.
func (m *Manager) acquirePortAllocationLock() (func(), error) {
	path := portAllocationLockPath(m)
	releaseLocal := acquireLocalPortLock(path)
	if path == "" {
		var once sync.Once
		return func() {
			once.Do(releaseLocal)
		}, nil
	}

	file, acquired, err := acquireFileLock(
		path,
		"port allocation lock",
		syscall.LOCK_EX,
		false,
	)
	if err != nil {
		releaseLocal()
		return nil, err
	}
	if !acquired {
		releaseLocal()
		return nil, errors.New("could not acquire blocking port allocation lock")
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			releaseFileLock(file)
			releaseLocal()
		})
	}, nil
}

// acquireOperationLock coordinates long-running Docker/database mutations
// across Manager values and separate cmgr/cmgrd processes. Ordinary
// operations take a shared lock so they retain their existing concurrency;
// challenge updates take the exclusive side of the barrier because they keep
// old containers and image tags as rollback reserves until every build has
// validated.
func (m *Manager) acquireOperationLock(exclusive bool) (func(), error) {
	release, _, err := m.acquireOperationLockMode(exclusive, false)
	return release, err
}

// tryAcquireOperationLock attempts to acquire a lock without waiting. A false
// acquired result is ordinary contention, not an error.
func (m *Manager) tryAcquireOperationLock(
	exclusive bool,
) (release func(), acquired bool, err error) {
	return m.acquireOperationLockMode(exclusive, true)
}

// acquireStartupOperationLock reserves exclusive startup access when it is
// immediately available. If another ordinary operation is already active, a
// current database can instead be opened under a shared lock without waiting
// for that potentially long-running operation to finish.
func (m *Manager) acquireStartupOperationLock() (
	release func(),
	exclusive bool,
	err error,
) {
	release, exclusive, err = m.tryAcquireOperationLock(true)
	if err != nil || exclusive {
		return release, exclusive, err
	}
	release, err = m.acquireOperationLock(false)
	return release, false, err
}

func (m *Manager) acquireOperationLockMode(
	exclusive bool,
	nonblocking bool,
) (release func(), acquired bool, err error) {
	unlockLocal := m.operationMu.RUnlock
	if exclusive {
		if nonblocking {
			if !m.operationMu.TryLock() {
				return nil, false, nil
			}
		} else {
			m.operationMu.Lock()
		}
		unlockLocal = m.operationMu.Unlock
	} else {
		if nonblocking {
			if !m.operationMu.TryRLock() {
				return nil, false, nil
			}
		} else {
			m.operationMu.RLock()
		}
	}

	if m.operationLockPath == "" {
		var once sync.Once
		return func() {
			once.Do(unlockLocal)
		}, true, nil
	}

	// Every operation passes through an exclusive turnstile before acquiring
	// the shared/exclusive main lock. An exclusive waiter holds the turnstile
	// while existing readers drain, preventing later readers from bypassing it.
	gate, acquired, err := acquireFileLock(
		operationGatePath(m),
		"operation lock gate",
		syscall.LOCK_EX,
		nonblocking,
	)
	if err != nil {
		unlockLocal()
		return nil, false, err
	}
	if !acquired {
		unlockLocal()
		return nil, false, nil
	}

	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	lock, acquired, err := acquireFileLock(
		m.operationLockPath,
		"operation lock",
		mode,
		nonblocking,
	)
	if err != nil {
		releaseFileLock(gate)
		unlockLocal()
		return nil, false, err
	}
	if !acquired {
		releaseFileLock(gate)
		unlockLocal()
		return nil, false, nil
	}
	releaseFileLock(gate)

	var once sync.Once
	return func() {
		once.Do(func() {
			releaseFileLock(lock)
			unlockLocal()
		})
	}, true, nil
}
