//go:build !darwin && !linux

package method

import "math"

// PhysicalMemory is unknown on this platform; report 4 GiB so that AddTempfile behaves as on a large machine.
// TODO: query the RAM size on Windows and the BSDs.
func PhysicalMemory() uint64 { return math.MaxUint32 }
