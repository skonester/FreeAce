package rep

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/n0madic/go-unarc/internal/codec/frame"
	"github.com/n0madic/go-unarc/internal/method"
)

const (
	// Defaults of the method parameters (the REP_METHOD constructor).
	defaultBlockSize      = 64 * method.MB
	defaultMinCompression = 100
	defaultMinMatchLen    = 512
	defaultSmallestLen    = 512
	defaultAmplifier      = 1
)

func init() { method.Register(Parse) }

// codec is a parsed "rep" method. Of its parameters only the window size matters when decoding: it bounds
// the window announced by the stream. The others are kept to document what the string said.
type codec struct {
	blockSize      uint32 // b: window size
	minCompression int32  // N%
	minMatchLen    int32  // l, or a bare number
	hashSizeLog    int32  // h
	barrier        int32  // d
	smallestLen    int32  // s
	amplifier      int32  // a
	env            *method.Env
}

// Parse mirrors parse_REP.
func Parse(params []string, env *method.Env) (method.Codec, bool) {
	if params[0] != "rep" {
		return nil, false
	}
	c := codec{
		blockSize:      defaultBlockSize,
		minCompression: defaultMinCompression,
		minMatchLen:    defaultMinMatchLen,
		barrier:        math.MaxInt32,
		smallestLen:    defaultSmallestLen,
		amplifier:      defaultAmplifier,
		env:            env,
	}
	for _, param := range params[1:] {
		if !c.set(param) {
			return nil, false
		}
	}
	return c, true
}

// set applies one parameter, reporting false when it is invalid.
func (c *codec) set(param string) bool {
	if param != "" {
		value := param[1:]
		switch param[0] {
		case 'b':
			return setMem(&c.blockSize, value)
		case 'l':
			return setInt(&c.minMatchLen, value)
		case 'd':
			return setMemInt(&c.barrier, value)
		case 's':
			return setInt(&c.smallestLen, value)
		case 'h':
			return setInt(&c.hashSizeLog, value)
		case 'a':
			return setInt(&c.amplifier, value)
		}
	}
	// "N%" is the minimal compression percentage, a bare number is the minimal match length,
	// anything else must be the block size.
	if percent, ok := strings.CutSuffix(param, "%"); ok && setInt(&c.minCompression, percent) {
		return true
	}
	if setInt(&c.minMatchLen, param) {
		return true
	}
	return setMem(&c.blockSize, param)
}

// setInt, setMem and setMemInt store a parsed number only if the text was valid, so that a failed
// attempt (the parameter may be tried as something else) leaves the field alone.
func setInt(dst *int32, s string) bool {
	n, ok := method.ParseInt(s)
	if ok {
		*dst = int32(n)
	}
	return ok
}

func setMem(dst *uint32, s string) bool {
	n, ok := method.ParseMem(s)
	if ok {
		*dst = n
	}
	return ok
}

func setMemInt(dst *int32, s string) bool {
	n, ok := method.ParseMem(s)
	if ok {
		*dst = int32(n)
	}
	return ok
}

// DecompressionMem is the window size, as in the C method.
func (c codec) DecompressionMem() uint64 { return uint64(c.blockSize) }

// Dictionary mirrors REP_METHOD::GetDictionary: the block size.
func (c codec) Dictionary() uint64 { return uint64(c.blockSize) }

// Decode rebuilds the data block by block, keeping the last BlockSize bytes as the match window.
//
// The window size is read from the stream, as the C decoder does, but it must not exceed the size in the
// method parameters: that is what DecompressionMem promised the caller. The window is allocated as the data
// fills it, so a short stream cannot claim a big buffer without producing the data for it.
func (c codec) Decode(dst io.Writer, src io.Reader) error {
	v, err := frame.ReadInt32(src)
	if err != nil {
		return frame.Truncated(err)
	}
	size := uint32(v) // sizes are unsigned in rep streams
	if size > c.blockSize {
		return badData("window of %d bytes exceeds the %d bytes of the method", size, c.blockSize)
	}
	if err := c.env.CheckDecoderMemory(uint64(size)); err != nil {
		return err
	}
	if uint64(size) > math.MaxInt {
		return badData("window of %d bytes does not fit in memory", size)
	}
	d := &decoder{dst: dst, size: int(size)}
	for {
		v, err := frame.ReadInt32(src)
		if err != nil {
			return frame.Truncated(err)
		}
		comprSize := uint32(v)
		if comprSize == 0 {
			return nil
		}
		if comprSize > math.MaxInt32 {
			return badData("block of %d bytes", comprSize)
		}
		if err := c.env.CheckDecoderMemory(uint64(comprSize)); err != nil {
			return err
		}
		if d.block, err = frame.ReadN(d.block, src, int(comprSize)); err != nil {
			return err
		}
		if err := d.decodeBlock(d.block); err != nil {
			return err
		}
	}
}

// decoder is the state of one Decode call: the ring buffer holding the window.
type decoder struct {
	dst     io.Writer
	size    int    // window size
	win     []byte // the window; grows to size as data arrives, so len(win) < size until the first wrap
	pos     int    // next write position in win
	flushed int    // win[flushed:pos] is not written to dst yet
	block   []byte // input buffer, reused between blocks
}

// decodeBlock decodes one block (see the package comment) and writes its output.
func (d *decoder) decodeBlock(b []byte) error {
	if len(b) < 4 {
		return badData("block of %d bytes", len(b))
	}
	num := int(int32(binary.LittleEndian.Uint32(b)))
	if num < 0 || 12*int64(num)+8 > int64(len(b)) {
		return badData("%d matches do not fit into a block of %d bytes", num, len(b))
	}
	lens := b[4 : 4+4*num]
	offsets := b[4+4*num : 4+8*num]
	datalens := b[4+8*num : 8+12*num]
	literals := b[8+12*num:]

	for i := 0; ; i++ {
		n := int(int32(binary.LittleEndian.Uint32(datalens[4*i:])))
		if n < 0 || n > len(literals) {
			return badData("literal run of %d bytes, %d left in the block", n, len(literals))
		}
		if err := d.writeLiterals(literals[:n]); err != nil {
			return err
		}
		literals = literals[n:]
		if i == num {
			break
		}
		offset := int64(binary.LittleEndian.Uint32(offsets[4*i:]))
		length := int(int32(binary.LittleEndian.Uint32(lens[4*i:])))
		if length < 0 {
			return badData("match of negative length")
		}
		if err := d.copyMatch(offset, length); err != nil {
			return err
		}
	}
	return d.flush()
}

// flush writes the bytes decoded since the last flush.
func (d *decoder) flush() error {
	if d.pos > d.flushed {
		if _, err := d.dst.Write(d.win[d.flushed:d.pos]); err != nil {
			return err
		}
	}
	d.flushed = d.pos
	return nil
}

// wrap starts over at the beginning of the window once its end is reached.
func (d *decoder) wrap() error {
	if err := d.flush(); err != nil {
		return err
	}
	d.pos, d.flushed = 0, 0
	return nil
}

// grow makes win at least end bytes long (end <= size), reallocating geometrically.
func (d *decoder) grow(end int) {
	if end <= len(d.win) {
		return
	}
	if end > cap(d.win) {
		w := make([]byte, len(d.win), min(d.size, max(end, 2*cap(d.win), frame.MaxPrealloc)))
		copy(w, d.win)
		d.win = w
	}
	d.win = d.win[:end]
}

func (d *decoder) writeLiterals(p []byte) error {
	if len(p) > 0 && d.size == 0 {
		return badData("data for an empty window")
	}
	for len(p) > 0 {
		if d.pos == d.size {
			if err := d.wrap(); err != nil {
				return err
			}
		}
		n := min(len(p), d.size-d.pos)
		d.grow(d.pos + n)
		copy(d.win[d.pos:], p[:n])
		d.pos += n
		p = p[n:]
	}
	return nil
}

// copyMatch appends length bytes copied from offset bytes back in the ring, byte by byte, so that a match
// longer than its offset repeats itself.
func (d *decoder) copyMatch(offset int64, length int) error {
	if length == 0 {
		return nil
	}
	if d.size == 0 || offset > int64(d.size) {
		return badData("match offset %d, window of %d bytes", offset, d.size)
	}
	if len(d.win) < d.size && (offset == 0 || offset > int64(d.pos)) {
		// The window has not wrapped yet, so there is nothing before the start of the data.
		return badData("match offset %d, %d bytes decoded", offset, d.pos)
	}
	off := int(offset)
	for length > 0 {
		if d.pos == d.size {
			if err := d.wrap(); err != nil {
				return err
			}
		}
		src := d.pos - off
		if src < 0 {
			src += d.size
		}
		// A step ends where the destination or the source reaches the end of the ring.
		n := min(length, d.size-d.pos, d.size-src)
		d.grow(d.pos + n)
		lzCopy(d.win, d.pos, src, n)
		d.pos += n
		length -= n
	}
	return nil
}

// lzCopy copies n bytes from buf[src:] to buf[dst:] as a forward byte loop would: when the source lies
// before the destination and the ranges overlap, the bytes just written are copied again.
func lzCopy(buf []byte, dst, src, n int) {
	dist := dst - src
	if dist <= 0 || dist >= n {
		copy(buf[dst:dst+n], buf[src:src+n]) // disjoint, or the source is ahead (memmove semantics are right)
		return
	}
	copy(buf[dst:dst+dist], buf[src:src+dist])
	for done := dist; done < n; {
		done += copy(buf[dst+done:dst+n], buf[dst:dst+done])
	}
}

func badData(format string, args ...any) error {
	return fmt.Errorf("%w: %s", method.ErrBadData, fmt.Sprintf(format, args...))
}
