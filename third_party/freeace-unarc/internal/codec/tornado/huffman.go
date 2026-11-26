package tornado

import (
	"fmt"
	"math"
	"slices"

	"github.com/n0madic/go-unarc/internal/method"
)

const (
	maxHuf   = 2048 // maximum number of symbols (MAXHUF); the alphabet has 842
	fastBits = 11   // codes up to this length are decoded with one lookup in huffTree.fast
	fastSize = 1 << fastBits
	// chunks is the counter value below which symbols are ordered by counting sort; the rest is sorted.
	chunks = 250
	// maxCodeBits bounds the length of a code. A tree over the counters of a valid stream (their total never
	// exceeds about 41000) is at most 22 levels deep, so longer codes mean a corrupt stream; the bound also
	// keeps the lookup table for long codes (huffTree.index) below 32 MiB.
	maxCodeBits = 24
	// maxCounter bounds a symbol counter so that counter<<11|symbol fits 32 bits, the sort key.
	maxCounter = 1 << 21
)

var (
	errCounters = fmt.Errorf("%w: Huffman statistics overflow", method.ErrBadData)
	errDepth    = fmt.Errorf("%w: Huffman code too long", method.ErrBadData)
)

// rescaleFactor maps the 3-bit rescale mode that follows an end-of-block code to the divisor by which the
// symbol counters are decayed.
var rescaleFactor = [8]uint32{2, 3, 4, 6, 8, 10, 12, 16}

// huffTree is the decoding side of HuffmanTree. Counters start at 1 and grow with every decoded symbol;
// build derives the code of each symbol from them and then decays them for the next block.
type huffTree struct {
	n       int
	counter [maxHuf]uint32

	// Decoding tables. fast maps the low fastBits input bits to bits<<16|symbol; an entry of 0 marks codes
	// longer than fastBits, which are found in index by the low maxbits input bits.
	fast  [fastSize]uint32
	index []uint16
	mask  uint32 // 1<<maxbits - 1
	bits  [maxHuf]uint8

	// Scratch space of build: nodes 0..n-1 are the symbols sorted by counter, the combined nodes follow.
	cnt   [2*maxHuf + 4]uint32
	left  [2*maxHuf + 4]uint16
	right [2*maxHuf + 4]uint16
	nbits [2*maxHuf + 4]uint8
	ncode [2*maxHuf + 4]uint32
	rest  []uint32
}

func newHuffTree(n int) *huffTree {
	t := &huffTree{n: n}
	for s := range n {
		t.counter[s] = 1
	}
	if err := t.build(0); err != nil {
		panic(err) // cannot happen: all counters are 1
	}
	return t
}

// build ports HuffmanTree::build_tree for the decoder, tie-breaking included: symbols are ordered by
// (counter, symbol), the two lowest nodes of the original list or of the list of combined nodes are joined
// repeatedly, a tie between the lists goes to the original one. rescaleMode selects how the counters decay.
func (t *huffTree) build(rescaleMode uint32) error {
	n := t.n

	// Order the symbols: counting sort for small counters, sorting for the others.
	var places [chunks + 1]int32
	for i := range n {
		c := t.counter[i]
		if c < chunks {
			places[c+1]++
		} else if c >= maxCounter {
			return errCounters
		}
	}
	for i := range chunks {
		places[i+1] += places[i]
	}
	rest := t.rest[:0]
	for i := range n {
		c := t.counter[i]
		if c < chunks {
			p := places[c]
			places[c]++
			t.cnt[p] = c
			t.left[p] = uint16(i)
		} else {
			rest = append(rest, c<<11|uint32(i))
		}
	}
	slices.Sort(rest)
	t.rest = rest
	small := int(places[chunks])
	for i, key := range rest {
		t.cnt[small+i] = key >> 11
		t.left[small+i] = uint16(key & (1<<11 - 1))
	}

	// Join nodes. The original nodes are 0..n-1, followed by INT_MAX fences; the combined ones are created
	// from n+2 on, over the fences, so each list ends with a fence of its own.
	b := n
	for i := range b + 4 {
		t.cnt[b+i] = math.MaxInt32
	}
	p1, p2, p3 := 0, b+2, b+2
	for p1 != b || p3-p2 != 1 {
		switch {
		case t.cnt[p1+1] < t.cnt[p2]: // the two lowest are original nodes
			t.cnt[p3] = t.cnt[p1] + t.cnt[p1+1]
			t.left[p3], t.right[p3] = uint16(p1), uint16(p1+1)
			p1 += 2
		case t.cnt[p1] > t.cnt[p2+1]: // the two lowest are combined nodes
			t.cnt[p3] = t.cnt[p2] + t.cnt[p2+1]
			t.left[p3], t.right[p3] = uint16(p2), uint16(p2+1)
			p2 += 2
		default: // one of each
			t.cnt[p3] = t.cnt[p1] + t.cnt[p2]
			t.left[p3], t.right[p3] = uint16(p1), uint16(p2)
			p1++
			p2++
		}
		p3++
	}

	// The root gets the empty code; a child gets the code of its parent plus a 0 (left) or 1 (right) bit
	// at the position of the parent's length. Children have lower indexes than their parents.
	t.nbits[p2], t.ncode[p2] = 0, 0
	for i := p2; i >= b+2; i-- {
		nb := t.nbits[i]
		if nb >= maxCodeBits {
			return errDepth
		}
		l, r := t.left[i], t.right[i]
		t.nbits[l], t.ncode[l] = nb+1, t.ncode[i]
		t.nbits[r], t.ncode[r] = nb+1, t.ncode[i]+1<<nb
	}

	// Decoding tables. maxbits is the length of the longest code, which the C decoder takes from the least
	// frequent symbol (the first node); the two always agree.
	maxbits := uint32(0)
	for i := range b {
		maxbits = max(maxbits, uint32(t.nbits[i]))
	}
	if need := 1 << maxbits; need > len(t.index) {
		t.index = make([]uint16, need)
	}
	t.mask = 1<<maxbits - 1
	for i := range b {
		s, sbits, scode := t.left[i], uint32(t.nbits[i]), t.ncode[i]
		t.bits[s&(maxHuf-1)] = uint8(sbits)
		if sbits <= fastBits {
			e := sbits<<16 | uint32(s)
			for j := scode; j < fastSize; j += 1 << sbits {
				t.fast[j] = e
			}
		} else {
			t.fast[scode&(fastSize-1)] = 0
			for j := scode; j <= t.mask; j += 1 << sbits {
				t.index[j] = s
			}
		}
	}

	t.decay(rescaleMode)
	return nil
}

// decay lowers the counters for the next block: by 1 if they are below the factor, else by a part 1/factor
// of their value. The rescale mode of the C encoder is always 3 (factor 6), which gets a constant divisor.
func (t *huffTree) decay(mode uint32) {
	counters := t.counter[:t.n]
	if mode&7 == 3 {
		for s, c := range counters {
			if c > 1 && c < 6 {
				counters[s] = c - 1
			} else {
				counters[s] = c - c/6
			}
		}
		return
	}
	f := rescaleFactor[mode&7]
	for s, c := range counters {
		if c > 1 && c < f {
			counters[s] = c - 1
		} else {
			counters[s] = c - c/f
		}
	}
}

// huffDecoder is LZ77_Decoder<HuffmanDecoder>: Huffman-coded symbols and extra bits in one bit stream.
// The tree is rebuilt from the symbol statistics whenever the end-of-block symbol comes.
type huffDecoder struct {
	bitReader
	model lzModel
	tree  *huffTree
}

func newHuffDecoder(in *inStream, minLen uint32) *huffDecoder {
	return &huffDecoder{bitReader: bitReader{in: in}, model: lzModel{minLen: minLen}, tree: newHuffTree(codes)}
}

// decode fills ev with the next elements of the stream (see source).
func (d *huffDecoder) decode(ev []element) (int, error) {
	in, t, m := d.in, d.tree, &d.model
	bitbuf, bitcnt := d.buf, d.cnt
	n := 0
	for n < len(ev) {
		if bitcnt <= 32 {
			bitbuf |= uint64(in.get32()) << bitcnt
			bitcnt += 32
		}
		code := uint32(bitbuf)
		var x, nb uint32
		if e := t.fast[code&(fastSize-1)]; e != 0 {
			x, nb = e&0xffff, e>>16
		} else {
			x = uint32(t.index[code&t.mask])
			nb = uint32(t.bits[x&(maxHuf-1)])
		}
		bitbuf >>= nb
		bitcnt -= uint(nb)
		if x == eobCode {
			d.buf, d.cnt = bitbuf, bitcnt
			if err := t.build(d.getbits(3)); err != nil {
				return n, err
			}
			bitbuf, bitcnt = d.buf, d.cnt
			continue
		}
		t.counter[x&(maxHuf-1)]++
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
			if lb := uint(lenBits2[lcode]); lb != 0 {
				if bitcnt <= 32 {
					bitbuf |= uint64(in.get32()) << bitcnt
					bitcnt += 32
				}
				length += uint32(bitbuf) & (1<<lb - 1)
				bitbuf >>= lb
				bitcnt -= lb
			}
			length = m.matchLen(length)
			if dcode := int((x-256)/lenCodes) - repdistCodes; dcode < 0 {
				dist = m.repeat(dcode)
			} else {
				dcode &= 31
				dist = distBase[dcode] + 1
				if db := uint(distBits[dcode]); db != 0 {
					if bitcnt <= 32 {
						bitbuf |= uint64(in.get32()) << bitcnt
						bitcnt += 32
					}
					dist += uint32(bitbuf) & (1<<db - 1)
					bitbuf >>= db
					bitcnt -= db
				}
				m.push(dist)
			}
		}
		if dist == 0 { // an unused symbol, or a repeat of a distance never seen
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
