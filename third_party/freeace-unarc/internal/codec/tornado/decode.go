package tornado

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/n0madic/go-unarc/internal/method"
)

// Coders, the first byte of a stream (STORING has no decoder: the C decoder rejects it too).
const (
	coderByte = 1 + iota
	coderBit
	coderHuffman
	coderArith
)

const (
	headerSize = 6 // coder (1 byte), minimum match length (1), buffer size (4, little endian)
	// hugeBuffer is the least size of the ring buffer and the most written to the output at a time
	// (HUGE_BUFFER_SIZE).
	hugeBuffer = 8 * method.MB
	// pad is the room kept before and after the ring buffer for data tables. The C decoder keeps 512 bytes,
	// 2 rows of the widest table; a table diffed across the end of the ring needs up to 3 rows before its start.
	pad = 4 * method.KB
	// firstAlloc is how much of the ring is allocated at first. The ring grows as output is produced, so a
	// stream that claims a huge buffer cannot make the decoder allocate more than it actually decodes.
	firstAlloc = 64 * method.KB
	// batch is the number of elements a coder decodes at a time.
	batch = 512
)

// element is one decoded literal or match. A literal has dist 0 and the byte in len.
type element struct{ len, dist uint32 }

// source is an entropy decoder: decode stores the next elements of the stream in ev and returns their
// number. It stops after a special element (len >= impossibleLen: end of stream or table), at the end of
// ev, or on error. It returns the elements decoded before an error along with it; they are not used.
type source interface {
	decode(ev []element) (int, error)
}

// Decode reads a Tornado stream from src and writes the data it encodes to dst.
//
// The stream starts with the coder, the minimum match length and the buffer size; the C decoder keeps the
// output in a circular buffer of the larger of that size and 8 MiB, and writes it out in chunks of at most
// 8 MiB. Chunk boundaries do not change the output, except through the corner cases of data tables, so the
// same boundaries are used here.
func (c *codec) Decode(dst io.Writer, src io.Reader) error { return c.NewDecoder().Decode(dst, src) }

// NewDecoder returns a decoder for one goroutine that keeps the input buffer and the ring from one stream to
// the next.
func (c *codec) NewDecoder() method.Decoder { return &streamDecoder{env: c.env} }

// streamDecoder is the memory of a decoding, kept for the next stream.
type streamDecoder struct {
	env *method.Env
	in  []byte // inStream.buf
	buf []byte // output.buf
}

func (d *streamDecoder) Decode(dst io.Writer, src io.Reader) error {
	in := newInStream(src, d.in)
	d.in = in.buf
	s, bufSize, err := openStream(in)
	if err != nil {
		return err
	}
	if err := d.env.CheckDecoderMemory(uint64(max(bufSize, hugeBuffer))); err != nil {
		return err
	}
	o, err := newOutput(dst, bufSize, d.buf)
	if err != nil {
		return err
	}
	err = o.run(s)
	d.buf = o.buf
	return err
}

// openStream reads the header of a stream from in, which has not read anything yet, and returns the
// entropy decoder of the rest and the buffer size the header records.
func openStream(in *inStream) (s source, bufSize uint32, err error) {
	var hdr [headerSize]byte
	if _, err := io.ReadFull(in.r, hdr[:]); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			err = fmt.Errorf("%w: truncated header", method.ErrBadData)
		}
		return nil, 0, err
	}
	coder, minLen := hdr[0], uint32(hdr[1])
	bufSize = binary.LittleEndian.Uint32(hdr[2:])

	switch coder {
	case coderByte:
		s = newByteDecoder(in, minLen)
	case coderBit:
		s = newBitDecoder(in, minLen)
	case coderHuffman:
		s = newHuffDecoder(in, minLen)
	case coderArith:
		s = newArithDecoder(in, minLen)
	default:
		return nil, 0, fmt.Errorf("%w: unknown coder %d", method.ErrBadData, coder)
	}
	return s, bufSize, nil
}

// output is the part of tor_decompress0 behind the entropy decoder: the circular buffer, the copying of
// matches, the data tables and the writing. Positions are indexes into buf, which holds the ring at
// [ring0, ringEnd) between pads.
type output struct {
	dst io.Writer
	buf []byte

	ring0, ringEnd int
	hdrSize        uint32 // buffer size from the stream header (_bufsize)
	bufSize        uint32 // size of the ring: hdrSize, at least hugeBuffer (bufsize)

	writeStart int    // data before this point was written
	writeEnd   int    // the buffer is written out when the output pointer reaches this point
	allocEnd   int    // the ring is allocated up to this point
	lim        int    // min(writeEnd, allocEnd): where the output loop has to stop and call advance
	offset     uint64 // output position of ring0
	tables     dataTables
}

// newOutput prepares the ring for a stream whose header gives hdrSize. It reuses the storage of old, the
// ring of an earlier stream: the ring holds no byte of this stream before the decoder writes it (a match may
// not reach before the start of the stream), and the pads are cleared as in a new buffer.
func newOutput(dst io.Writer, hdrSize uint32, old []byte) (*output, error) {
	bufSize := max(hdrSize, hugeBuffer)
	if uint64(bufSize)+3*pad > math.MaxInt {
		return nil, fmt.Errorf("%w: buffer of %d bytes does not fit in memory", method.ErrBadData, bufSize)
	}
	o := &output{dst: dst, hdrSize: hdrSize, bufSize: bufSize, ring0: pad}
	o.ringEnd = o.ring0 + int(bufSize)
	o.writeStart = o.ring0
	o.writeEnd = o.ring0 + int(min(bufSize, hugeBuffer))
	o.allocEnd = max(o.ring0+firstAlloc, min(cap(old)-pad, o.writeEnd))
	if cap(old) >= o.allocEnd+pad {
		o.buf = old[:o.allocEnd+pad]
		clear(o.buf[:o.ring0])
		clear(o.buf[o.allocEnd:])
	} else {
		o.buf = make([]byte, o.allocEnd+pad)
	}
	o.lim = o.allocEnd
	return o, nil
}

// advance is called when the output pointer out reaches o.lim: it writes the buffer out if the write point
// is reached, and allocates more of the ring if that is needed to go on. It returns the new output pointer.
func (o *output) advance(out int) (int, error) {
	if out >= o.writeEnd {
		var err error
		if out, err = o.flush(out); err != nil {
			return out, err
		}
	}
	o.grow()
	return out, nil
}

// grow extends the allocated part of the ring up to the write point (by doubling) and sets the limit.
func (o *output) grow() {
	if o.allocEnd < o.writeEnd && o.lim >= o.allocEnd {
		newEnd := min(o.writeEnd, o.ring0+max(2*(o.allocEnd-o.ring0), firstAlloc))
		buf := make([]byte, newEnd+pad)
		copy(buf, o.buf)
		o.buf, o.allocEnd = buf, newEnd
	}
	o.lim = min(o.writeEnd, o.allocEnd)
}

// flush is WRITE_DATA_IF: it undiffs the tables, writes buf[writeStart:out], diffs the tables again and moves
// the pointers: to the buffer start if the ring is full, and the write point to its next position.
func (o *output) flush(out int) (int, error) {
	if err := o.tables.undiff(o.buf, o.writeStart, out); err != nil {
		return out, err
	}
	if out > o.writeStart {
		if _, err := o.dst.Write(o.buf[o.writeStart:out]); err != nil {
			return out, err
		}
	}
	if err := o.tables.diff(o.buf, o.writeStart, out); err != nil {
		return out, err
	}
	o.writeStart = out
	if out >= o.ringEnd {
		shift := out - o.ring0
		o.offset += uint64(shift)
		o.writeStart -= shift
		o.writeEnd -= shift
		if err := o.tables.shift(o.buf, out, o.ring0); err != nil {
			return out, err
		}
		out = o.ring0
	}
	if o.writeStart >= o.writeEnd {
		o.writeEnd = o.writeStart + min(o.ringEnd-o.writeStart, hugeBuffer)
	}
	return out, nil
}

// run decodes the stream until its end marker.
func (o *output) run(src source) error {
	var ev [batch]element
	buf := o.buf
	out, lim := o.ring0, o.lim
	for {
		n, err := src.decode(ev[:])
		if err != nil {
			return err
		}
		for _, e := range ev[:n] {
			if e.dist == 0 { // literal
				buf[out] = byte(e.len)
				out++
				if out >= lim {
					if out, err = o.advance(out); err != nil {
						return err
					}
					buf, lim = o.buf, o.lim
				}
				continue
			}

			length, dist := e.len, e.dist
			if length < impossibleLen {
				if length == 0 {
					return errCorrupt
				}
				// The simple match (almost all of them): the source is in the buffer and the copy stays
				// clear of the write point.
				if int(dist) <= out-o.ring0 && lim-out > int(length) {
					copyMatch(buf, out, out-int(dist), int(length))
					out += int(length)
					continue
				}
				// Check that compressed data are not broken.
				if dist > o.bufSize || length > 2*o.hdrSize || o.offset+uint64(out-o.ring0) < uint64(dist) {
					return errCorrupt
				}
				if out, err = o.slowMatch(out, length, dist); err != nil {
					return err
				}
				buf, lim = o.buf, o.lim
			} else if length == impossibleLen && dist == impossibleDist {
				// End of the stream.
				_, err := o.flush(out)
				return err
			} else {
				// A data table: length-impossibleLen is its row size and dist the number of rows.
				rowSize := length - impossibleLen
				if rowSize == 0 || rowSize > maxTableRow || dist*rowSize > 2*o.hdrSize {
					return errCorrupt
				}
				o.tables.add(int(rowSize), out, int(dist))
				if o.tables.filled() {
					if out, err = o.flush(out); err != nil {
						return err
					}
					o.grow()
					buf, lim = o.buf, o.lim
				}
			}
		}
	}
}

// slowMatch copies a match whose source wraps around the end of the ring, or that runs into the write point
// (or the end of the allocated part of the ring), flushing the buffer when needed.
func (o *output) slowMatch(out int, length, dist uint32) (int, error) {
	// The source may be before the buffer start: then it is at the end of the ring.
	p := out - int(dist)
	if out-o.ring0 < int(dist) {
		p += int(o.bufSize)
	}
	for n := int(length); n > 0; {
		k := min(n, o.lim-out, o.ringEnd-p)
		copyMatch(o.buf, out, p, k)
		out += k
		p += k
		n -= k
		if p == o.ringEnd {
			p = o.ring0
		}
		if out >= o.lim {
			var err error
			if out, err = o.advance(out); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

// copyMatch copies n bytes from buf[src:] to buf[dst:] as the byte loop *dst++ = *src++ does: when the
// source is before the destination and they overlap, the copied bytes repeat with the period dst-src.
func copyMatch(buf []byte, dst, src, n int) {
	if src >= dst || src+n <= dst {
		copy(buf[dst:dst+n], buf[src:src+n])
		return
	}
	for done := 0; done < n; {
		done += copy(buf[dst+done:dst+n], buf[src:dst+done])
	}
}
