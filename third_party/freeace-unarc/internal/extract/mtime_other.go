//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package extract

import "os"

// Do not reopen an untrusted path just to set a best-effort timestamp.
func setMtime(*os.File, uint32) {}
