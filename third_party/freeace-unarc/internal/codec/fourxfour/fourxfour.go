// Package fourxfour decodes the "4x4" method of FreeArc 0.67 (Compression/4x4/C_4x4.cpp), which FreeArc
// uses by default on multi-core machines (-m1..-m5): the data is cut into blocks, each compressed on its own
// by an inner method, so the blocks decode in parallel. The C unarc does not know this method.
//
// The stream is a little-endian int32 format version (0), then blocks until the end of the stream:
//
//	int32 OutSize   decoded size; -1 marks a stored block, whose payload is the data itself
//	int32 InSize    payload size
//	payload         InSize bytes: the inner method's stream for this block
package fourxfour

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"runtime"
	"strings"

	"github.com/n0madic/go-unarc/internal/codec/frame"
	"github.com/n0madic/go-unarc/internal/codec/parallel"
	"github.com/n0madic/go-unarc/internal/method"
)

const (
	formatVersion = 0           // _4x4_VERSION
	headerSize    = 8           // _4x4_HEADER_SIZE: OutSize and InSize
	storedBlock   = -1          // OutSize of a block kept as is
	defaultMethod = "tor:3:1mb" // inner method of a "4x4" string that names none
	defaultExtra  = 2           // GetNumExtraBuffers when i is not given
)

func init() { method.Register(Parse) }

// codec is a parsed "4x4" method.
type codec struct {
	inner     method.Codec
	innerName string // names the inner method in its errors, as the chain names its stages
	blockSize uint32 // b; 0 = the inner method's dictionary
	extra     uint32 // i: buffers read ahead, which only count in the memory estimate
	workers   int    // decoding goroutines
	env       *method.Env
}

// Parse mirrors parse_4x4: "4x4", its own parameters, then the inner method, whose parameters are the rest of
// the string (e.g. "4x4:b7mb:ppmd:8:96mb"). A parameter starts the inner method when neither of its first two
// characters is a digit. The own parameters are
//
//	bSIZE tN iN rX   block size, threads, extra I/O buffers, minimal order-0 ratio (only steers the encoder)
//	N                a bare number is the thread count
//	SIZE             a bare size ("8mb", "64kb", "24" = 2^24) is the block size
//
// The decoding goroutines are one per processor, no more than the threads the encoder chose, and as few as
// the memory limit requires (see limit).
func Parse(params []string, env *method.Env) (method.Codec, bool) {
	if params[0] != "4x4" {
		return nil, false
	}
	c := &codec{extra: defaultExtra, env: env}
	var threads uint32
	inner := defaultMethod
	for i, p := range params[1:] {
		if !isDigit(at(p, 0)) && !isDigit(at(p, 1)) {
			inner = strings.Join(params[1+i:], ":")
			break
		}
		if !c.param(p, &threads) {
			return nil, false
		}
	}
	var ok bool
	if c.inner, ok = method.Lookup(inner, env); !ok {
		return nil, false
	}
	c.innerName = method.Name(inner)
	c.workers = runtime.NumCPU()
	if threads > 0 {
		c.workers = int(min(threads, uint32(c.workers)))
	}
	c.limit(method.MaxMem())
	return c, true
}

// param parses one of the method's own parameters.
func (c *codec) param(p string, threads *uint32) bool {
	var ok bool
	if len(p) > 1 {
		switch p[0] {
		case 'b':
			c.blockSize, ok = method.ParseMem(p[1:])
			return ok
		case 't':
			*threads, ok = method.ParseInt(p[1:])
			return ok
		case 'i':
			c.extra, ok = method.ParseInt(p[1:])
			if int32(c.extra) < 0 {
				c.extra = defaultExtra // C keeps it in an int: a negative count means the default
			}
			return ok
		case 'r':
			return true // parseDouble is atof: it accepts anything
		}
	}
	// A parameter without a name: digits only is the thread count, otherwise a block size.
	if n, ok := method.ParseInt(p); ok {
		*threads = n
		return true
	}
	c.blockSize, ok = method.ParseMem(p)
	return ok
}

// at returns s[i], or 0 past the end of s (the C code reads the terminating NUL).
func at(s string, i int) byte {
	if i < len(s) {
		return s[i]
	}
	return 0
}

func isDigit(b byte) bool { return '0' <= b && b <= '9' }

// maxCompressedSize is COMPRESSION_METHOD::GetMaxCompressedSize, the largest payload of a block of n bytes
// for any inner method (the C methods that override it give smaller bounds).
func maxCompressedSize(n uint64) uint64 { return n + n/4 + 16*1024 }

// dictionarer is a codec that reports its dictionary size, as COMPRESSION_METHOD::GetDictionary.
type dictionarer interface{ Dictionary() uint64 }

// memory returns what a worker needs (the inner method's memory and a pair of block buffers) and what a pair
// of block buffers takes, as _4x4_METHOD::GetSetDeCompressionMem computes them for decompression.
func (c *codec) memory() (worker, buffers uint64) {
	block := uint64(c.blockSize)
	if d, ok := c.inner.(dictionarer); ok && block == 0 {
		block = d.Dictionary()
	}
	buffers = 2 * (maxCompressedSize(block) + headerSize)
	return uint64(uint32(c.inner.DecompressionMem())) + buffers, buffers
}

// DecompressionMem mirrors _4x4_METHOD::GetDecompressionMem: every worker needs its memory, and i more pairs
// of block buffers are read ahead.
func (c *codec) DecompressionMem() uint64 {
	worker, buffers := c.memory()
	return min(uint64(c.workers)*worker+uint64(c.extra)*buffers, math.MaxUint32)
}

// limit fits the workers into mem bytes as FreeArc does before decompressing (LimitDecompressionMem, the
// SetDecompressionMem branch of GetSetDeCompressionMem): first fewer buffers read ahead, then fewer workers,
// at least one. The block size is the stream's, so the memory of one worker cannot shrink.
func (c *codec) limit(mem uint64) {
	worker, buffers := c.memory()
	t, i := uint64(c.workers), uint64(c.extra)
	switch {
	case mem >= t*worker+i*buffers:
	case mem >= t*worker:
		c.extra = uint32((mem - t*worker) / buffers)
	case mem >= worker:
		t = mem / worker
		c.workers = int(t)
		c.extra = uint32(min(i, (mem-t*worker)/buffers))
	default:
		c.workers, c.extra = 1, 0
	}
}

// Decode reads the format version and decodes the blocks on c.workers goroutines, writing them in order.
func (c *codec) Decode(dst io.Writer, src io.Reader) error {
	version, err := frame.ReadInt32(src)
	switch {
	case err == io.EOF:
		return nil // C: an empty stream ends at READ4_OR_EOF
	case err != nil:
		return err
	case version != formatVersion:
		return fmt.Errorf("%w: format version %d", method.ErrBadData, version)
	}
	worker, _ := c.memory()
	budget := c.env.DecoderLimit()
	workers := int(min(uint64(c.workers), max(1, budget/worker)))
	// The queue retains more items than the number of decoding workers.
	r := &reader{src: src, budget: budget / uint64(workers+workers/2+1)}
	return parallel.Decode(dst, workers, r.next, c.decodeBlock)
}

// reader reads the blocks of one stream.
type reader struct {
	src    io.Reader
	limit  int64  // the largest OutSize or InSize allowed; 0 until the first block sets it
	budget uint64 // input + output bytes allowed per queued block
}

// next reads a block header and its payload. Like the C decoder, it refuses a block larger than the
// largest payload of the first one: C sizes its buffers from the first block.
func (r *reader) next(j *parallel.Job[int32]) (bool, error) {
	outSize, err := frame.ReadInt32(r.src)
	if err == io.EOF {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	inSize, err := frame.ReadInt32(r.src)
	if err != nil {
		return false, frame.Truncated(err)
	}
	if inSize < 0 || outSize < storedBlock {
		return false, fmt.Errorf("%w: block of %d bytes from %d", method.ErrBadData, outSize, inSize)
	}
	if r.limit == 0 {
		first := outSize
		if first <= 0 {
			first = inSize
		}
		r.limit = int64(maxCompressedSize(uint64(first)))
	}
	if int64(inSize) > r.limit || int64(outSize) > r.limit {
		return false, fmt.Errorf("%w: block of %d bytes from %d, larger than the first block allows (%d)",
			method.ErrBadData, outSize, inSize, r.limit)
	}
	if size := uint64(inSize) + uint64(max(0, outSize)); size > r.budget {
		return false, fmt.Errorf("%w: 4x4 block needs %d bytes (limit %d)", method.ErrLimit, size, r.budget)
	}
	j.Header = outSize
	if j.In, err = frame.ReadN(j.In, r.src, int(inSize)); err != nil {
		return false, err
	}
	return true, nil
}

// errOverflow is what blockWriter returns when the inner method writes more than the block's size.
var errOverflow = errors.New("block overflow")

// blockWriter collects a decoded block of at most max bytes, as C's MemBuf does.
type blockWriter struct {
	buf []byte
	max int
}

func (w *blockWriter) Write(p []byte) (int, error) {
	if len(p) > w.max-len(w.buf) {
		return 0, errOverflow
	}
	w.buf = append(w.buf, p...)
	return len(p), nil
}

// reusable is an inner method that can keep its memory (dictionary, model, buffers) from one block to the
// next, as C keeps the CodecState of each 4x4 thread; see method.Codec.
type reusable interface {
	NewDecoder() method.Decoder
}

// worker is the state of one decoding goroutine.
type worker struct {
	dec method.Decoder // the inner method's decoder, made for the first block
	src bytes.Reader
}

// decodeBlock decodes the payload of j with the inner method into j.Out. The block must decode to exactly
// OutSize bytes: C writes OutSize bytes whatever the inner method produced.
func (c *codec) decodeBlock(wk *worker, j *parallel.Job[int32]) {
	if j.Header == storedBlock {
		j.In, j.Out = j.Out, j.In
		return
	}
	if wk.dec == nil {
		wk.dec = c.inner
		if r, ok := c.inner.(reusable); ok {
			wk.dec = r.NewDecoder()
		}
	}
	wk.src.Reset(j.In)
	w := blockWriter{buf: j.Out[:0], max: int(j.Header)}
	err := wk.dec.Decode(&w, &wk.src)
	j.Out = w.buf
	switch {
	case errors.Is(err, errOverflow):
		j.Err = fmt.Errorf("%w: block decodes to more than %d bytes", method.ErrBadData, j.Header)
	case err != nil:
		j.Err = method.Named(c.innerName, err)
	case len(w.buf) != int(j.Header):
		j.Err = fmt.Errorf("%w: block decodes to %d bytes instead of %d", method.ErrBadData, len(w.buf), j.Header)
	}
}
