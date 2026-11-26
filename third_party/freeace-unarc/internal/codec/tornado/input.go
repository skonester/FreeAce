package tornado

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/n0madic/go-unarc/internal/method"
)

const (
	// inChunk is the amount requested from the source at a time (LARGE_BUFFER_SIZE of the C decoder).
	inChunk = 256 * method.KB
	// maxElem is the most bytes a decoder consumes between two checks of the buffer: the widest element of
	// the byte coder (4 bytes of flags and 10 of a long match, rounded up). After a refill that many bytes are
	// readable ahead, zeros if the stream has ended.
	maxElem = 16
	// maxOverrun is how many bytes past the end of the stream the decoders may consume before the stream
	// counts as truncated. A valid stream is over-read by at most 8 bytes: the bit readers prefetch 32 bits
	// ahead and keep at most 64 bits in their buffer.
	maxOverrun = 16
)

var (
	errTruncated = fmt.Errorf("%w: unexpected end of data", method.ErrBadData)
	errCorrupt   = fmt.Errorf("%w: invalid code", method.ErrBadData)
)

// inStream is the byte-oriented input of every decoder, the counterpart of the C InputByteStream. The C
// decoder reads past the end of a valid stream (lookahead of the bit readers) and gets stale buffer bytes;
// here those bytes are zeros, and a stream that is consumed further than maxOverrun bytes past its end is an
// error.
type inStream struct {
	r    io.Reader
	buf  []byte
	pos  int // next byte to read; may run past end by up to maxElem bytes (zero padding after EOF)
	end  int // end of the data read from r
	over int // bytes handed out beyond end so far
	eof  bool
	err  error // first read error or truncation; returned by the decoders at the end of each batch
}

// newInStream returns the input of a stream read from r, reusing the storage of buf when it is large enough.
func newInStream(r io.Reader, buf []byte) *inStream {
	if n := inChunk + 3*maxElem; cap(buf) >= n {
		buf = buf[:n]
	} else {
		buf = make([]byte, n)
	}
	return &inStream{r: r, buf: buf}
}

// refill moves the unread bytes to the buffer start and reads the next chunk. After EOF it appends zero padding.
func (s *inStream) refill() {
	if s.pos > s.end {
		s.over += s.pos - s.end
		s.pos = s.end
	}
	n := copy(s.buf, s.buf[s.pos:s.end])
	s.pos, s.end = 0, n
	if !s.eof {
		m, err := io.ReadFull(s.r, s.buf[n:n+inChunk])
		s.end += m
		if err != nil {
			s.eof = true
			if err != io.EOF && err != io.ErrUnexpectedEOF && s.err == nil {
				s.err = err
			}
		}
	}
	if s.eof {
		clear(s.buf[s.end : s.end+maxElem])
		if s.over > maxOverrun && s.err == nil {
			s.err = errTruncated
		}
	}
}

func (s *inStream) get8() uint32 {
	if s.pos >= s.end {
		s.refill()
	}
	v := s.buf[s.pos]
	s.pos++
	return uint32(v)
}

func (s *inStream) get16() uint32 {
	if s.pos+2 > s.end {
		s.refill()
	}
	v := binary.LittleEndian.Uint16(s.buf[s.pos:])
	s.pos += 2
	return uint32(v)
}

func (s *inStream) get24() uint32 {
	if s.pos+3 > s.end {
		s.refill()
	}
	v := binary.LittleEndian.Uint32(s.buf[s.pos:]) & 0xffffff
	s.pos += 3
	return v
}

func (s *inStream) get32() uint32 {
	if s.pos+4 > s.end {
		s.refill()
	}
	v := binary.LittleEndian.Uint32(s.buf[s.pos:])
	s.pos += 4
	return v
}

// bitReader is the C InputBitStream: bits are taken from the low end of a 64-bit buffer that is topped up
// with 32 bits whenever 32 or fewer remain.
type bitReader struct {
	in  *inStream
	buf uint64
	cnt uint // valid bits in buf
}

// getbits returns the next n (at most 30) bits.
func (b *bitReader) getbits(n uint32) uint32 {
	if b.cnt <= 32 {
		b.buf |= uint64(b.in.get32()) << b.cnt
		b.cnt += 32
	}
	x := uint32(b.buf) & (1<<n - 1)
	b.buf >>= n
	b.cnt -= uint(n)
	return x
}
