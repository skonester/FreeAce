package extract

import (
	"os"
	"syscall"
	"time"
)

func setMtime(f *os.File, seconds uint32) {
	mtime := syscall.NsecToFiletime(time.Unix(int64(seconds), 0).UnixNano())
	_ = syscall.SetFileTime(syscall.Handle(f.Fd()), nil, nil, &mtime)
}
