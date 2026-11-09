package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// workerLock is a process-scoped advisory lock that ensures only one process
// consumes the durable job queue. SQLite still guarantees atomic claims, but
// the lock also prevents two workers from running independent jobs in parallel.
type workerLock struct {
	file *os.File
	mu   sync.Mutex
}

func acquireWorkerLock(path string) (*workerLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errWorkerLocked
		}
		return nil, fmt.Errorf("job worker lock: %w", err)
	}
	return &workerLock{file: f}, nil
}

func (l *workerLock) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}

var errWorkerLocked = errors.New("another pbmanager process is already running the job worker")

// TryAcquireWorker acquires the single-worker lock. When false is returned,
// another process (normally the systemd service) owns the job worker.
func (a *App) TryAcquireWorker() (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.worker != nil {
		return true, nil
	}
	lock, err := acquireWorkerLock(filepath.Join(a.Config.DataDir, "worker.lock"))
	if errors.Is(err, errWorkerLocked) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	a.worker = lock
	return true, nil
}

// ReleaseWorker releases the worker lock.
func (a *App) ReleaseWorker() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.worker != nil {
		a.worker.release()
		a.worker = nil
	}
}
