package tornado

import "math"

// Special length/distance values: the pair (impossibleLen, impossibleDist) ends the stream, and
// impossibleLen+type (type 1..) with a row count as distance describes a diffed data table.
const (
	impossibleLen  = math.MaxInt32 / 2
	impossibleDist = math.MaxInt32 / 2
)

// Symbol alphabet of the Huffman and arithmetic coders: 256 literals, then one symbol per (distance code,
// length code) pair, then the special symbols.
const (
	repdistCodes = 4                        // distance codes 0..3 repeat one of the last four distances
	lenCodes     = 16                       // number of length codes
	distCodes    = 32 + repdistCodes        // real distance codes plus the repeat codes
	eobCode      = 256 + lenCodes*distCodes // end of block: the Huffman tree is rebuilt
	repChar      = eobCode + 1              // literal equal to the byte one last-distance back
	codes        = eobCode + 10             // alphabet size, including spare symbols
)

// Number of extra bits following each length code, for the bit coder (lenBits1) and for the Huffman and
// arithmetic coders (lenBits2), and following each distance code (distBits).
var (
	lenBits1 = [8]uint8{0, 0, 0, 1, 2, 4, 8, 30}
	lenBits2 = [lenCodes]uint8{0, 0, 0, 0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 8, 30}
	distBits = [32]uint8{4, 4, 5, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13, 14, 14, 15, 15, 16, 17, 18, 19, 21, 23, 30}
)

// The first value of each code is the sum of 2^bits over the codes before it.
var (
	lenBase1 = func() (b [8]uint32) { fillBases(lenBits1[:], b[:]); return }()
	lenBase2 = func() (b [lenCodes]uint32) { fillBases(lenBits2[:], b[:]); return }()
	distBase = func() (b [32]uint32) { fillBases(distBits[:], b[:]); return }()
)

func fillBases(bits []uint8, base []uint32) {
	var v uint32
	for i, n := range bits {
		base[i] = v
		v += 1 << n
	}
}

// lzModel is the part of the LZ77_Decoder shared by the Huffman and arithmetic coders: it keeps the four
// most recent distances and maps what the coders decode to match lengths and distances. A symbol
// x >= 256 other than repChar stands for a match whose length code is x%16 and distance code x/16-20 (the
// first four are repeats of recent distances); the coders read the extra bits of both after it.
type lzModel struct {
	minLen uint32
	// Last distances, d0 the most recent. The C decoder starts them at zero; a repeat of such a distance
	// has no meaning and is reported as invalid.
	d0, d1, d2, d3 uint32
}

// matchLen maps the value decoded from a length code and its extra bits to a match length.
func (m *lzModel) matchLen(v uint32) uint32 {
	switch {
	case v <= 100:
		return v + m.minLen
	case v <= 104: // codes 101..104 stand for a data table of 1..4 byte rows
		return v + impossibleLen - 100
	default:
		return v + m.minLen - 4
	}
}

// repeat returns the distance of a repeat code (dcode -4..-1) and moves it to the front.
func (m *lzModel) repeat(dcode int) (dist uint32) {
	switch dcode {
	case -4:
		return m.d0
	case -3:
		dist = m.d1
		m.d1, m.d0 = m.d0, dist
	case -2:
		dist = m.d2
		m.d2, m.d1, m.d0 = m.d1, m.d0, dist
	default:
		dist = m.d3
		m.d3, m.d2, m.d1, m.d0 = m.d2, m.d1, m.d0, dist
	}
	return dist
}

// push makes dist the most recent distance.
func (m *lzModel) push(dist uint32) {
	m.d3, m.d2, m.d1, m.d0 = m.d2, m.d1, m.d0, dist
}
