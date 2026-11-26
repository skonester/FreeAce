package method

import "golang.org/x/sys/unix"

// PhysicalMemory returns the installed RAM in bytes, or 0 if it cannot be determined.
func PhysicalMemory() uint64 {
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err != nil {
		return 0
	}
	return uint64(si.Totalram) * uint64(si.Unit)
}
