package arc

import (
	"bytes"
	"encoding/binary"
)

// buffer decodes the binary layout of control blocks (MEMORY_BUFFER in ArcStructure.h).
// Every read is bounds-checked; running past the end fails with the same messages as C.
type buffer struct {
	data []byte
	p    int
}

func (b *buffer) eof() bool      { return b.p >= len(b.data) }
func (b *buffer) remaining() int { return len(b.data) - b.p }
func (b *buffer) rest() []byte   { return b.data[b.p:] }
func (b *buffer) skip(n int) error {
	if n < 0 || n > b.remaining() {
		b.p = len(b.data)
		return errBadData
	}
	b.p += n
	return nil
}

// peek returns the next 9 bytes, zero-padded past the end (C reads the slack it allocated after the buffer).
func (b *buffer) peek() (out [9]byte) {
	copy(out[:], b.rest())
	return out
}

// readInteger decodes FreeArc's variable-length integer: the number of trailing 1 bits of the first byte
// gives the length; 0xFF is followed by 8 raw bytes.
func (b *buffer) readInteger() (uint64, error) {
	raw := b.peek()
	x := binary.LittleEndian.Uint32(raw[:4])
	y := binary.LittleEndian.Uint64(raw[:8])
	var n int
	var v uint64
	switch {
	case x&1 == 0:
		n, v = 1, uint64(x&0xFF)>>1
	case x&3 == 1:
		n, v = 2, uint64(x&0xFFFF)>>2
	case x&7 == 3:
		n, v = 3, uint64(x&0xFFFFFF)>>3
	case x&15 == 7:
		n, v = 4, uint64(x)>>4
	case x&31 == 15:
		n, v = 5, (y&(1<<40-1))>>5
	case x&63 == 31:
		n, v = 6, (y&(1<<48-1))>>6
	case x&127 == 63:
		n, v = 7, (y&(1<<56-1))>>7
	case x&255 == 127:
		n, v = 8, y>>8
	default:
		n, v = 9, binary.LittleEndian.Uint64(raw[1:9])
	}
	if err := b.skip(n); err != nil {
		return 0, err
	}
	return v, nil
}

// readCount reads an array length. A length that cannot fit in the rest of the buffer
// (every element takes at least minSize bytes) fails with tooLong instead of allocating it.
func (b *buffer) readCount(minSize int, tooLong error) (int, error) {
	n, err := b.readInteger()
	if err != nil {
		return 0, err
	}
	if n > uint64(b.remaining()/minSize) {
		return 0, tooLong
	}
	return int(n), nil
}

func (b *buffer) read1() (byte, error) {
	if b.remaining() < 1 {
		return 0, b.skip(1)
	}
	v := b.data[b.p]
	b.p++
	return v, nil
}

func (b *buffer) read4() (uint32, error) {
	if b.remaining() < 4 {
		return 0, b.skip(4)
	}
	v := binary.LittleEndian.Uint32(b.data[b.p:])
	b.p += 4
	return v, nil
}

// readString reads a NUL-terminated string.
func (b *buffer) readString() (string, error) {
	i := bytes.IndexByte(b.rest(), 0)
	if i < 0 {
		return "", errBadString
	}
	s := string(b.data[b.p : b.p+i])
	b.p += i + 1
	return s, nil
}

func (b *buffer) readIntegers(n int) ([]uint64, error) {
	out := make([]uint64, n)
	for i := range out {
		v, err := b.readInteger()
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func (b *buffer) readStrings(n int) ([]string, error) {
	out := make([]string, n)
	for i := range out {
		s, err := b.readString()
		if err != nil {
			return nil, err
		}
		out[i] = s
	}
	return out, nil
}

// readDescriptor reads a block descriptor as stored in the footer block.
func (b *buffer) readDescriptor() (Block, error) {
	var d Block
	t, err := b.readInteger()
	if err != nil {
		return d, err
	}
	d.Type = BlockType(t)
	if d.Compressor, err = b.readString(); err != nil {
		return d, err
	}
	if d.Pos, err = b.readInteger(); err != nil {
		return d, err
	}
	if d.OrigSize, err = b.readInteger(); err != nil {
		return d, err
	}
	if d.CompSize, err = b.readInteger(); err != nil {
		return d, err
	}
	d.CRC, err = b.read4()
	return d, err
}
