package tornado

import "encoding/binary"

// byteDecoder is LZ77_ByteDecoder, the coder of tor:1. Elements are byte-aligned; a 32-bit flags word
// ahead of every 16 elements tells the kind of each: 0 literal (1 byte), 1 short match (2 bytes: 4 bits of
// length, 12 of distance), 2 medium match (3 bytes: 6 and 18 bits), 3 long match (see decode).
type byteDecoder struct {
	in      *inStream
	minLen  uint32
	flags   uint32
	flagpos uint32 // elements left in flags, counting the current one
}

func newByteDecoder(in *inStream, minLen uint32) *byteDecoder {
	return &byteDecoder{in: in, minLen: minLen, flagpos: 1}
}

// decode fills ev with the next elements of the stream (see source).
func (d *byteDecoder) decode(ev []element) (int, error) {
	in := d.in
	buf, pos, end := in.buf, in.pos, in.end
	flags, flagpos := d.flags, d.flagpos
	n := 0
	for n < len(ev) {
		if pos+maxElem > end {
			in.pos = pos
			in.refill()
			pos, end = in.pos, in.end
		}
		if flagpos--; flagpos != 0 {
			flags >>= 2
		} else {
			flagpos = 16
			flags = binary.LittleEndian.Uint32(buf[pos:])
			pos += 4
		}
		var e element
		switch flags & 3 {
		case 0:
			e = element{len: uint32(buf[pos])}
			pos++
		case 1:
			x := uint32(binary.LittleEndian.Uint16(buf[pos:]))
			pos += 2
			e = element{len: d.minLen + x>>12, dist: x & (1<<12 - 1)}
		case 2:
			x := binary.LittleEndian.Uint32(buf[pos:]) & 0xffffff
			pos += 3
			e = element{len: d.minLen + x>>18, dist: x & (1<<18 - 1)}
		default:
			// 1 byte of length-minLen, or 255 and the top byte of the distance followed by that byte; a
			// length of 254 escapes to a 32-bit one (24 bits and 8 more). The low 24 bits of the distance follow.
			length := uint32(buf[pos])
			pos++
			var dist uint32
			if length == 255 {
				dist = uint32(buf[pos]) << 24
				length = uint32(buf[pos+1])
				pos += 2
			}
			if length == 254 {
				length = binary.LittleEndian.Uint32(buf[pos:])&0xffffff<<8 + uint32(buf[pos+3])
				pos += 4
			}
			dist += binary.LittleEndian.Uint32(buf[pos:]) & 0xffffff
			pos += 3
			e = element{len: d.minLen + length, dist: dist}
		}
		if e.dist == 0 && flags&3 != 0 {
			in.pos = pos
			return n, errCorrupt
		}
		ev[n] = e
		n++
		if e.len >= impossibleLen {
			break
		}
	}
	in.pos = pos
	d.flags, d.flagpos = flags, flagpos
	return n, in.err
}
