//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package extract

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Set the time through the open file: a pathname-based Chtimes can follow a replaced symlink.
func setMtime(f *os.File, seconds uint32) {
	fd := int(f.Fd())
	var st unix.Stat_t
	if unix.Fstat(fd, &st) == nil {
		_ = unix.Futimes(fd, []unix.Timeval{
			unix.NsecToTimeval(st.Atim.Nano()),
			unix.NsecToTimeval(time.Unix(int64(seconds), 0).UnixNano()),
		})
	}
}
