// Package cleanup keeps the list of files to remove if the program is interrupted
// (temp files of chain stages, the output file being written).
package cleanup

import (
	"os"
	"sync"
)

var (
	mu       sync.Mutex
	next     int
	removers = map[int]func(){}
)

// Register remembers path for removal on interrupt and returns the function that forgets it again.
func Register(path string) (forget func()) {
	return RegisterFunc(func() { _ = os.RemoveAll(path) })
}

// RegisterFunc registers cleanup that must use something other than a process-relative path.
func RegisterFunc(remove func()) (forget func()) {
	mu.Lock()
	defer mu.Unlock()
	id := next
	next++
	removers[id] = remove
	return func() {
		mu.Lock()
		defer mu.Unlock()
		delete(removers, id)
	}
}

// RemoveAll removes every registered path, newest first so that files go before their folders.
func RemoveAll() {
	mu.Lock()
	defer mu.Unlock()
	for id := next - 1; id >= 0; id-- {
		if remove, ok := removers[id]; ok {
			remove() // best effort: the program is exiting
			delete(removers, id)
		}
	}
}
