package method

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
)

// ErrLimit distinguishes a local resource policy from damaged archive data.
var ErrLimit = errors.New("resource limit exceeded")

const (
	defaultControlBytes = 64 * MB
	defaultDecoderBytes = 1 * GB
	defaultTempBytes    = 64 * GB
	defaultKDFRounds    = 1_000_000
)

// Limits bounds work requested by archive data. A zero field uses the default.
type Limits struct {
	ControlBytes uint64 // packed and unpacked size of each control block
	DecoderBytes uint64 // large buffers and models within a native decoder, not process RSS
	TempBytes    uint64 // live decoder scratch files (not extracted files)
	KDFRounds    uint64 // PBKDF2 iterations per key
}

func limitsFromEnv() Limits {
	read := func(name string, unit uint64) uint64 {
		n, err := strconv.ParseUint(os.Getenv(name), 10, 64)
		if err != nil || n > math.MaxInt64/unit {
			return 0
		}
		return n * unit
	}
	return Limits{
		ControlBytes: read("UNARC_MAX_CONTROL_MB", MB),
		DecoderBytes: read("UNARC_MAX_DECODER_MB", MB),
		TempBytes:    read("UNARC_MAX_TEMP_MB", MB),
		KDFRounds:    read("UNARC_MAX_KDF_ITERATIONS", 1),
	}
}

func (e *Env) ControlLimit() uint64 {
	if e != nil && e.Limits.ControlBytes != 0 {
		return e.Limits.ControlBytes
	}
	return defaultControlBytes
}

func (e *Env) DecoderLimit() uint64 {
	if e != nil && e.Limits.DecoderBytes != 0 {
		return e.Limits.DecoderBytes
	}
	return defaultDecoderBytes
}

func (e *Env) CheckDecoderMemory(n uint64) error {
	if n > e.DecoderLimit() {
		return fmt.Errorf("%w: decoder needs %d bytes (limit %d)", ErrLimit, n, e.DecoderLimit())
	}
	return nil
}

func (e *Env) CheckKDFRounds(n uint64) error {
	limit := uint64(defaultKDFRounds)
	if e != nil && e.Limits.KDFRounds != 0 {
		limit = e.Limits.KDFRounds
	}
	if n > limit {
		return fmt.Errorf("%w: PBKDF2 needs %d iterations (limit %d)", ErrLimit, n, limit)
	}
	return nil
}

// TempSpace accounts for one scratch file in the shared budget of an Env. Its owner calls Release
// when the file is removed or no longer managed by the decoder. Each file has one writer.
type TempSpace struct {
	env  *Env
	size uint64
}

func (e *Env) TempSpace() *TempSpace {
	if e == nil {
		return nil
	}
	return &TempSpace{env: e}
}

// Grow reserves up to size bytes, including holes in a sparse file.
func (t *TempSpace) Grow(size uint64) error {
	if t == nil || size <= t.size {
		return nil
	}
	limit := uint64(defaultTempBytes)
	if t.env.Limits.TempBytes != 0 {
		limit = t.env.Limits.TempBytes
	}
	more := size - t.size
	for {
		used := t.env.tempUsed.Load()
		if used > limit || more > limit-used {
			return fmt.Errorf("%w: temporary storage exceeds %d bytes", ErrLimit, limit)
		}
		if t.env.tempUsed.CompareAndSwap(used, used+more) {
			t.size = size
			return nil
		}
	}
}

func (t *TempSpace) Release() {
	if t != nil && t.size != 0 {
		t.env.tempUsed.Add(-t.size)
		t.size = 0
	}
}

// Writer reserves space before appending. Do not use it for overwrites or random access.
func (t *TempSpace) Writer(w io.Writer) io.Writer {
	if t == nil {
		return w
	}
	return tempWriter{space: t, dst: w}
}

type tempWriter struct {
	space *TempSpace
	dst   io.Writer
}

func (w tempWriter) Write(p []byte) (int, error) {
	if uint64(len(p)) > math.MaxUint64-w.space.size {
		return 0, ErrLimit
	}
	if err := w.space.Grow(w.space.size + uint64(len(p))); err != nil {
		return 0, err
	}
	return w.dst.Write(p)
}
