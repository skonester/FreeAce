// Package frame reads the framing of block codec streams: little-endian sizes and blocks whose lengths come
// from untrusted data.
package frame

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/n0madic/go-unarc/internal/method"
)

// MaxPrealloc bounds what is allocated up front for a size read from a stream; larger blocks grow as their
// data arrives.
const MaxPrealloc = 1 * method.MB

// ErrTruncated is the error for a stream that ends inside a value or a block.
var ErrTruncated = fmt.Errorf("%w: unexpected end of data", method.ErrBadData)

// Truncated turns the end of input into ErrTruncated; other errors pass unchanged.
func Truncated(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrTruncated
	}
	return err
}

// ReadInt32 reads a little-endian int32. It returns io.EOF if the stream ends cleanly before the value, and
// ErrTruncated if it ends inside it.
func ReadInt32(src io.Reader) (int32, error) {
	var b [4]byte
	if _, err := io.ReadFull(src, b[:]); err != nil {
		if err == io.EOF {
			return 0, io.EOF
		}
		return 0, Truncated(err)
	}
	return int32(binary.LittleEndian.Uint32(b[:])), nil
}

// ReadN reads exactly n bytes from src, reusing buf's storage. A big n is not trusted: the buffer grows only
// as data arrives, so a forged size cannot make the decoder allocate more than the stream really holds.
// A stream that ends early gives ErrTruncated, with the bytes read so far.
func ReadN(buf []byte, src io.Reader, n int) ([]byte, error) {
	buf = buf[:0]
	for len(buf) < n {
		step := n - len(buf)
		if n > MaxPrealloc && cap(buf) < n {
			step = min(step, max(len(buf), MaxPrealloc))
		}
		buf = slices.Grow(buf, step)
		m, err := io.ReadFull(src, buf[len(buf):len(buf)+step])
		buf = buf[:len(buf)+m]
		if err != nil {
			return buf, Truncated(err)
		}
	}
	return buf, nil
}
