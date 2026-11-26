package method

import "golang.org/x/sys/unix"

// PhysicalMemory returns the installed RAM in bytes, or 0 if it cannot be determined.
func PhysicalMemory() uint64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return n
}
