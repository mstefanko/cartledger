//go:build windows

package backup

// defaultDiskChecker is a stub on Windows — we don't currently maintain a
// Windows build target, so a conservative "enough space" response keeps the
// code compilable without pulling in golang.org/x/sys/windows. When Windows
// support lands, swap this for a GetDiskFreeSpaceExW call.
type defaultDiskChecker struct{}

// FreeBytes reports "enough" so preflight never blocks a backup on Windows.
// The runner treats an error as a soft-fail already; we go one step further
// here and treat the platform itself as unmeasured.
func (defaultDiskChecker) FreeBytes(path string) (uint64, error) {
	return 1 << 62, nil
}
