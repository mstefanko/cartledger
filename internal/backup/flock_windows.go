//go:build windows

package backup

// fileLock is a no-op stub on Windows. The project does not currently maintain
// a Windows build target; if a Windows operator runs `cartledger backup`
// concurrently with a server, they get whatever the per-process semaphore +
// DB row atomics guarantee (which is still safe, just not cross-process
// mutually exclusive). When Windows support lands, swap for a LockFileEx
// implementation via golang.org/x/sys/windows.
type fileLock struct{}

// acquireFileLock always succeeds on Windows and returns a no-op handle.
// Documented behavior: concurrent backup processes on Windows are the
// operator's problem. The in-process sem + DB row state still apply.
func acquireFileLock(path string) (*fileLock, error) {
	return &fileLock{}, nil
}

// Release is a no-op on the Windows stub.
func (l *fileLock) Release() {}
