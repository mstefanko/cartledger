//go:build !windows

package backup

import "syscall"

// defaultDiskChecker uses syscall.Statfs for free-bytes on unix.
// Windows build tag provides a stub in diskspace_windows.go.
type defaultDiskChecker struct{}

// FreeBytes returns available bytes for an unprivileged user on the
// filesystem hosting `path`. Uses f_bavail (not f_bfree) because the former
// reports bytes we can actually write without root.
func (defaultDiskChecker) FreeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	// Bavail is uint64 on Linux, uint32 on some BSDs — Go's Statfs_t already
	// exposes the platform-correct type; multiplication stays in uint64.
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
