// Package lzma implements the lzma method: raw LZMA streams (no header; dictionary size and lc/lp/pb come from
// the method string) that must end with an end marker, as FreeArc's C_LZMA.cpp writes and reads them.
package lzma

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/n0madic/go-unarc/internal/method"
)

func init() { method.Register(Parse) }

const (
	// largeBufferSize is the input buffer and the output chunk of the C decoder (LARGE_BUFFER_SIZE).
	largeBufferSize = 256 * method.KB
	defaultDict     = 64 * method.MB
)

// Match finders, in the order of kMatchFinderIDs (only the parser cares about them).
var matchFinders = []string{"BT2", "BT3", "BT4", "HC4", "HT4"}

const (
	mfBT4   = 2
	mfHT4   = 4
	mfUnset = math.MaxInt32 // INT_MAX: no match finder given yet
)

func findMatchFinder(s string) int {
	for i, m := range matchFinders {
		if strings.EqualFold(m, s) {
			return i
		}
	}
	return -1
}

// codec holds the parameters of an lzma method; only dictSize and lc/lp/pb matter for decoding.
type codec struct {
	dictSize   uint32
	lc, lp, pb uint32
	env        *method.Env
}

// Parse mirrors parse_LZMA: named parameters d, h, a, fb, mc, lc, lp, pb, mf; the keywords
// fastest/fast/normal/max/ultra; unnamed match finder, fast bytes or dictionary size. A '*' prefix makes a
// parameter optional: its errors are ignored, but, as in C, the error flag stays set for the next parameter.
func Parse(params []string, env *method.Env) (method.Codec, bool) {
	if params[0] != "lzma" {
		return nil, false
	}
	c := &codec{dictSize: defaultDict, lc: 3, lp: 0, pb: 2, env: env}
	mf := mfUnset
	bad := false
	parseInt := func(s string) uint32 {
		n, ok := method.ParseInt(s)
		bad = bad || !ok
		return n
	}
	keyword := func(fastMF int) {
		if mf == mfUnset {
			mf = fastMF
		}
	}
	for _, param := range params[1:] {
		optional := strings.HasPrefix(param, "*")
		if optional {
			param = param[1:]
		}
		unnamed := false
		switch {
		case strings.HasPrefix(param, "d"):
			n, ok := method.ParseMem(param[1:])
			bad = bad || !ok
			c.dictSize = n
		case strings.HasPrefix(param, "h"):
			_, ok := method.ParseMem(param[1:])
			bad = bad || !ok
			unnamed = bad // "hc4", "ht4": a match finder
		case strings.HasPrefix(param, "a"):
			parseInt(param[1:])
		case strings.HasPrefix(param, "fb"), strings.HasPrefix(param, "mc"):
			parseInt(param[2:])
		case strings.HasPrefix(param, "lc"):
			c.lc = parseInt(param[2:])
		case strings.HasPrefix(param, "lp"):
			c.lp = parseInt(param[2:])
		case strings.HasPrefix(param, "pb"):
			c.pb = parseInt(param[2:])
		case strings.HasPrefix(param, "mf"):
			name := param[2:]
			if strings.HasPrefix(name, "=") {
				name = name[1:]
			}
			mf = findMatchFinder(name)
		case param == "fastest", param == "fast", param == "normal":
			keyword(mfHT4)
		case param == "max", param == "ultra":
			keyword(mfBT4)
		default:
			unnamed = true
		}
		if unnamed {
			bad = false
			if n := findMatchFinder(param); n >= 0 {
				mf = n
			} else if _, ok := method.ParseInt(param); !ok {
				n, ok := method.ParseMem(param)
				bad = !ok
				if ok {
					c.dictSize = n
				}
			}
		}
		if !optional && (bad || mf < 0) {
			return nil, false
		}
	}
	return c, true
}

// DecompressionMem mirrors GetDecompressionMem: the dictionary plus the input buffer, as a 32-bit MemSize.
func (c *codec) DecompressionMem() uint64 { return uint64(c.dictSize + largeBufferSize) }

// Dictionary mirrors LZMA_METHOD::GetDictionary.
func (c *codec) Dictionary() uint64 { return uint64(c.dictSize) }

// Decode mirrors lzma_decompress: it reads the input in 256 KiB blocks and writes the output in chunks of at
// most 256 KiB, until the end marker. A stream without one is an error.
func (c *codec) Decode(dst io.Writer, src io.Reader) error { return c.NewDecoder().Decode(dst, src) }

// NewDecoder returns a decoder for one goroutine that keeps the dictionary, the probabilities and the input
// buffer from one stream to the next.
func (c *codec) NewDecoder() method.Decoder { return &streamDecoder{c: c} }

// streamDecoder is the memory of a decoding, kept for the next stream.
type streamDecoder struct {
	c  *codec
	d  decoder
	in []byte
}

func (s *streamDecoder) Decode(dst io.Writer, src io.Reader) error {
	d := &s.d
	if err := d.reset(s.c.lc, s.c.lp, s.c.pb, s.c.dictSize, s.c.env); err != nil {
		return err
	}
	if s.in == nil {
		s.in = make([]byte, largeBufferSize)
	}
	in := s.in
	inPos, inSize := 0, 0
	for {
		if inPos == inSize {
			n, err := io.ReadFull(src, in)
			if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				return err
			}
			inPos, inSize = 0, n
		}
		oldDicPos := d.dicPos
		curSize := min(d.dicBufSize-oldDicPos, largeBufferSize)
		d.ensure(oldDicPos + curSize)
		processed, st, decodeErr := d.decodeToDic(oldDicPos+curSize, in[inPos:inSize])
		inPos += processed
		out := d.dic[oldDicPos:d.dicPos]
		if len(out) > 0 {
			if _, err := dst.Write(out); err != nil {
				return err
			}
		}
		if decodeErr != nil {
			return decodeErr
		}
		if processed == 0 && len(out) == 0 {
			if st != statusFinishedWithMark {
				return fmt.Errorf("%w: stream ends without an end marker", method.ErrBadData)
			}
			return nil
		}
		if d.dicPos == d.dicBufSize {
			d.dicPos = 0
		}
	}
}
