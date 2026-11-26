package tornado

import (
	"fmt"

	"github.com/n0madic/go-unarc/internal/method"
)

// Parameters of the semi-adaptive arithmetic coder (TCounter).
const (
	rangeBits = 14             // symbol frequencies are scaled to sum up to 1<<rangeBits
	rangeSize = 1 << rangeBits // RANGE
	indexes   = 2048           // entries of the table that finds a symbol from its count
	// tabSize rounds the alphabet up to a power of two, which lets the compiler drop bounds checks.
	tabSize = 1024
)

var errRangeData = fmt.Errorf("%w: arithmetic decoder data error", method.ErrBadData)

// sym is the part of the cumulative frequency table that decoding looks at.
type sym struct{ cum, cnt uint32 }

// arithDecoder is LZ77_Decoder<ArithDecoder>: Schindler's range coder over a semi-adaptive model. Symbol
// counts are gathered in live and become the coding table every rangeSize symbols, each decayed by a sixth
// to keep the model responsive.
type arithDecoder struct {
	in    *inStream
	model lzModel

	rng, code uint32 // range and the window of the code value (C: range, buffer)
	bad       bool   // a getbits call hit a data error

	remainder uint32 // symbols left until the next rescale
	live      [tabSize]uint32
	tab       [tabSize]sym
	index     [indexes]uint16 // first symbol that can hold count c*(rangeSize/indexes)
}

func newArithDecoder(in *inStream, minLen uint32) *arithDecoder {
	d := &arithDecoder{in: in, model: lzModel{minLen: minLen}, rng: 0xffffffff}
	// Initially the range is split equally; the first symbols take the superfluous points.
	extra := rangeSize - rangeSize/codes*codes
	for s := range codes {
		d.live[s] = rangeSize / codes
		if s < extra {
			d.live[s]++
		}
	}
	d.rescale()
	// The first byte of the stream is always 0 (the encoder's cache byte) and falls out of the 32-bit window.
	for range 5 {
		d.code = d.code<<8 + in.get8()
	}
	return d
}

// rescale installs the gathered counts as the new coding table (TCounter::Rescale).
func (d *arithDecoder) rescale() {
	var total uint32
	remainder := uint32(rangeSize)
	ind := 0
	for s := range codes {
		c := d.live[s]
		d.tab[s] = sym{cum: total, cnt: c}
		last := total + c - 1
		total += c
		if c > 1 && c < 6 {
			c--
		} else {
			c -= c / 6
		}
		d.live[s] = c
		remainder -= c
		// index[i] gets the first symbol whose range reaches i*(rangeSize/indexes).
		for ind < indexes && last >= uint32(rangeSize/indexes*ind) {
			d.index[ind] = uint16(s)
			ind++
		}
	}
	d.remainder = remainder
}

// bits reads n (1..30) equiprobable bits with the range coder in the state (rng, code) and returns the bits
// and the new state (TRangeDecoder::GetCount and Update with a count of 1). The C decoder splits more than
// 24 bits into 15 and the rest. A count out of range, which only corrupt data produces, sets d.bad, after
// which nothing is read.
func (d *arithDecoder) bits(rng, code, n uint32) (x, newRng, newCode uint32) {
	if n > 24 {
		x1, rng, code := d.bits(rng, code, 15)
		x2, rng, code := d.bits(rng, code, n-15)
		return x2<<15 + x1, rng, code
	}
	if d.bad {
		return 0, rng, code
	}
	r := rng >> n
	x = code / r
	if x >= 1<<n {
		d.bad = true
		return 0, rng, code
	}
	code -= x * r
	for r < 1<<24 {
		r <<= 8
		code = code<<8 + d.in.get8()
	}
	return x, r, code
}

// decode fills ev with the next elements of the stream (see source).
func (d *arithDecoder) decode(ev []element) (int, error) {
	in, m := d.in, &d.model
	rng, code := d.rng, d.code
	n := 0
	for n < len(ev) {
		// Find the symbol whose slot holds the count of the code value.
		rng >>= rangeBits
		count := code / rng
		if count >= rangeSize {
			d.rng, d.code = rng, code
			return n, errRangeData
		}
		x := uint32(d.index[count/(rangeSize/indexes)&(indexes-1)])
		for {
			e := d.tab[x&(tabSize-1)]
			if e.cum+e.cnt-1 >= count {
				break
			}
			x++
		}
		e := d.tab[x&(tabSize-1)]
		code -= e.cum * rng
		rng *= e.cnt
		for rng < 1<<24 {
			rng <<= 8
			code = code<<8 + in.get8()
		}
		d.live[x&(tabSize-1)]++
		if d.remainder--; d.remainder == 0 {
			d.rescale()
		}

		if x < 256 {
			ev[n] = element{len: x}
			n++
			continue
		}

		// A match: the extra bits of the length, then those of the distance.
		var length, dist uint32
		if x == repChar {
			length, dist = 1, m.d0
		} else if x < eobCode {
			lcode := x & (lenCodes - 1)
			length = lenBase2[lcode]
			if lb := uint32(lenBits2[lcode]); lb != 0 {
				var v uint32
				v, rng, code = d.bits(rng, code, lb)
				length += v
			}
			length = m.matchLen(length)
			if dcode := int((x-256)/lenCodes) - repdistCodes; dcode < 0 {
				dist = m.repeat(dcode)
			} else {
				dcode &= 31
				dist = distBase[dcode] + 1
				if db := uint32(distBits[dcode]); db != 0 {
					var v uint32
					v, rng, code = d.bits(rng, code, db)
					dist += v
				}
				m.push(dist)
			}
			if d.bad {
				d.rng, d.code = rng, code
				return n, errRangeData
			}
		}
		if dist == 0 { // an unused symbol, or a repeat of a distance never seen
			d.rng, d.code = rng, code
			return n, errCorrupt
		}
		ev[n] = element{len: length, dist: dist}
		n++
		if length >= impossibleLen {
			break
		}
	}
	d.rng, d.code = rng, code
	return n, in.err
}
