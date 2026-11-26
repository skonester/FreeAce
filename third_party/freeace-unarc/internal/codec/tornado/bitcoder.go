package tornado

// bitDecoder is LZ77_BitDecoder, the coder of tor:2. Every element starts with 9 bits: a literal below 256,
// otherwise 256 + length code*32 + distance code, followed by the extra bits of the length and of the distance.
type bitDecoder struct {
	bitReader
	minLen uint32
}

func newBitDecoder(in *inStream, minLen uint32) *bitDecoder {
	return &bitDecoder{bitReader: bitReader{in: in}, minLen: minLen}
}

// decode fills ev with the next elements of the stream (see source).
func (d *bitDecoder) decode(ev []element) (int, error) {
	in := d.in
	bitbuf, bitcnt := d.buf, d.cnt
	n := 0
	for n < len(ev) {
		if bitcnt <= 32 {
			bitbuf |= uint64(in.get32()) << bitcnt
			bitcnt += 32
		}
		x := uint32(bitbuf) & (1<<9 - 1)
		bitbuf >>= 9
		bitcnt -= 9
		if x < 256 {
			ev[n] = element{len: x}
			n++
			continue
		}

		lcode := (x>>5 - 8) & 7
		length := d.minLen + lenBase1[lcode]
		if lb := uint(lenBits1[lcode]); lb != 0 {
			if bitcnt <= 32 {
				bitbuf |= uint64(in.get32()) << bitcnt
				bitcnt += 32
			}
			length += uint32(bitbuf) & (1<<lb - 1)
			bitbuf >>= lb
			bitcnt -= lb
		}
		dcode := x & 31
		dist := distBase[dcode]
		if db := uint(distBits[dcode]); db != 0 {
			if bitcnt <= 32 {
				bitbuf |= uint64(in.get32()) << bitcnt
				bitcnt += 32
			}
			dist += uint32(bitbuf) & (1<<db - 1)
			bitbuf >>= db
			bitcnt -= db
		}
		if dist == 0 {
			d.buf, d.cnt = bitbuf, bitcnt
			return n, errCorrupt
		}
		ev[n] = element{len: length, dist: dist}
		n++
		if length >= impossibleLen {
			break
		}
	}
	d.buf, d.cnt = bitbuf, bitcnt
	return n, in.err
}
