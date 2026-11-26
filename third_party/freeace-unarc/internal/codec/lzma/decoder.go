package lzma

import (
	"errors"
	"fmt"
	"math"

	"github.com/n0madic/go-unarc/internal/method"
)

// A port of LzmaDec.c (2008-11-06, Igor Pavlov, public domain) as FreeArc 0.67 uses it.

const (
	numTopBits           = 24
	topValue             = 1 << numTopBits
	numBitModelTotalBits = 11
	bitModelTotal        = 1 << numBitModelTotalBits
	numMoveBits          = 5
	rcInitSize           = 5
	requiredInputMax     = 20 // LZMA_REQUIRED_INPUT_MAX: the most input one symbol can take

	numPosBitsMax = 4

	lenNumLowBits     = 3
	lenNumLowSymbols  = 1 << lenNumLowBits
	lenNumMidBits     = 3
	lenNumMidSymbols  = 1 << lenNumMidBits
	lenNumHighBits    = 8
	lenNumHighSymbols = 1 << lenNumHighBits

	lenChoice   = 0
	lenChoice2  = lenChoice + 1
	lenLow      = lenChoice2 + 1
	lenMid      = lenLow + (1<<numPosBitsMax)<<lenNumLowBits
	lenHigh     = lenMid + (1<<numPosBitsMax)<<lenNumMidBits
	numLenProbs = lenHigh + lenNumHighSymbols

	numStates    = 12
	numLitStates = 7

	startPosModelIndex = 4
	endPosModelIndex   = 14
	numFullDistances   = 1 << (endPosModelIndex >> 1)

	numPosSlotBits    = 6
	numLenToPosStates = 4

	numAlignBits   = 4
	alignTableSize = 1 << numAlignBits

	matchMinLen       = 2
	matchSpecLenStart = matchMinLen + lenNumLowSymbols + lenNumMidSymbols + lenNumHighSymbols

	isMatch     = 0
	isRep       = isMatch + numStates<<numPosBitsMax
	isRepG0     = isRep + numStates
	isRepG1     = isRepG0 + numStates
	isRepG2     = isRepG1 + numStates
	isRep0Long  = isRepG2 + numStates
	posSlot     = isRep0Long + numStates<<numPosBitsMax
	specPos     = posSlot + numLenToPosStates<<numPosSlotBits
	align       = specPos + numFullDistances - endPosModelIndex
	lenCoder    = align + alignTableSize
	repLenCoder = lenCoder + numLenProbs
	literal     = repLenCoder + numLenProbs

	litSize = 0x300
)

var literalNextStates = [numStates * 2]uint32{0, 0, 0, 0, 1, 2, 3, 4, 5, 6, 4, 5, 7, 7, 7, 7, 7, 7, 7, 10, 10, 10, 10, 10}

// status mirrors ELzmaStatus.
type status int

const (
	statusNotSpecified status = iota
	statusFinishedWithMark
	statusNotFinished
	statusNeedsMoreInput
	statusMaybeFinishedWithoutMark
)

// tryDummy results (ELzmaDummy).
const (
	dummyError = iota // unexpected end of input
	dummyLit
	dummyMatch
	dummyRep
)

var errData = fmt.Errorf("%w: invalid data", method.ErrBadData)

// decoder is CLzmaDec. The dictionary is a ring of dicBufSize bytes; dic grows up to that size as data
// arrives, which is invisible to decoding because nothing wraps before the ring is full.
type decoder struct {
	lc, lp, pb uint32
	dictSize   uint32 // prop.dicSize
	probs      []uint16

	dic        []byte
	dicBufSize int
	dicPos     int

	rng, code    uint32
	processedPos uint32
	checkDicSize uint32
	state        uint32
	reps         [4]uint32
	remainLen    uint32

	needFlush     bool
	needInitState bool
	tempBufSize   int
	tempBuf       [requiredInputMax]byte
}

// errParams is returned for lc/lp/pb that the C decoder would index its tables out of range with.
var errParams = errors.New("unsupported lc/lp/pb")

// reset prepares d for a new stream. It keeps the storage of the dictionary and the probabilities: their old
// contents are never read, the probabilities are initialised before use and a match may only reach back
// into the bytes of the current stream (as LzmaDec_Init with the dictionary C keeps in a 4x4 CodecState).
func (d *decoder) reset(lc, lp, pb, dictSize uint32, env *method.Env) error {
	if lc > 8 || lp > 4 || pb > 4 {
		return fmt.Errorf("%w: %w (lc=%d lp=%d pb=%d)", method.ErrBadData, errParams, lc, lp, pb)
	}
	if dictSize == 0 {
		return errData
	}
	if uint64(dictSize) > math.MaxInt {
		return fmt.Errorf("%w: LZMA dictionary doesn't fit in an int", method.ErrLimit)
	}
	n := literal + litSize<<(lc+lp)
	if err := env.CheckDecoderMemory(uint64(dictSize) + uint64(2*n) + largeBufferSize); err != nil {
		return err
	}
	probs, dic := d.probs, d.dic
	*d = decoder{lc: lc, lp: lp, pb: pb, dictSize: dictSize, dicBufSize: int(dictSize)}
	if cap(probs) >= n {
		d.probs = probs[:n]
	} else {
		d.probs = make([]uint16, n)
	}
	if first := min(d.dicBufSize, initialDic); cap(dic) >= first {
		d.dic = dic[:min(cap(dic), d.dicBufSize)]
	} else {
		d.dic = make([]byte, first)
	}
	d.initDicAndState()
	return nil
}

// initialDic is the first allocation of the dictionary; it doubles as needed up to the declared size.
const initialDic = 1 << 20

// ensure grows the dictionary so that positions below limit exist.
func (d *decoder) ensure(limit int) {
	if limit <= len(d.dic) {
		return
	}
	n := max(limit, min(2*len(d.dic), d.dicBufSize))
	grown := make([]byte, n)
	copy(grown, d.dic[:d.dicPos])
	d.dic = grown
}

func (d *decoder) initDicAndState() {
	d.needFlush = true
	d.remainLen = 0
	d.tempBufSize = 0
	d.processedPos = 0
	d.checkDicSize = 0
	d.needInitState = true
}

func (d *decoder) initStateReal() {
	for i := range d.probs {
		d.probs[i] = bitModelTotal >> 1
	}
	d.reps = [4]uint32{1, 1, 1, 1}
	d.state = 0
	d.needInitState = false
}

func (d *decoder) initRc(data []byte) {
	d.code = uint32(data[1])<<24 | uint32(data[2])<<16 | uint32(data[3])<<8 | uint32(data[4])
	d.rng = 0xFFFFFFFF
	d.needFlush = false
}

// decodeReal decodes symbols into the dictionary up to limit while pos < bufLimit; the first symbol is
// always decoded. buf must hold requiredInputMax bytes past bufLimit. It returns the new input position.
func (d *decoder) decodeReal(limit int, buf []byte, pos, bufLimit int) (int, error) {
	probs := d.probs
	state := d.state
	rep0, rep1, rep2, rep3 := d.reps[0], d.reps[1], d.reps[2], d.reps[3]
	pbMask := uint32(1)<<d.pb - 1
	lpMask := uint32(1)<<d.lp - 1
	lc := d.lc
	dic := d.dic
	dicBufSize := d.dicBufSize
	dicPos := d.dicPos
	processedPos := d.processedPos
	checkDicSize := d.checkDicSize
	var length uint32
	rng, code := d.rng, d.code

loop:
	for {
		posState := processedPos & pbMask
		prob := isMatch + state<<numPosBitsMax + posState
		ttt := uint32(probs[prob])
		if rng < topValue {
			rng <<= 8
			code = code<<8 | uint32(buf[pos])
			pos++
		}
		bound := (rng >> numBitModelTotalBits) * ttt
		if code < bound {
			rng = bound
			probs[prob] = uint16(ttt + (bitModelTotal-ttt)>>numMoveBits)
			prob = literal
			if checkDicSize != 0 || processedPos != 0 {
				prev := dicPos
				if prev == 0 {
					prev = dicBufSize
				}
				prob += litSize * ((processedPos&lpMask)<<lc + uint32(dic[prev-1])>>(8-lc))
			}
			symbol := uint32(1)
			if state < numLitStates {
				for symbol < 0x100 {
					p := prob + symbol
					ttt := uint32(probs[p])
					if rng < topValue {
						rng <<= 8
						code = code<<8 | uint32(buf[pos])
						pos++
					}
					bound := (rng >> numBitModelTotalBits) * ttt
					if code < bound {
						rng = bound
						probs[p] = uint16(ttt + (bitModelTotal-ttt)>>numMoveBits)
						symbol <<= 1
					} else {
						rng -= bound
						code -= bound
						probs[p] = uint16(ttt - ttt>>numMoveBits)
						symbol = symbol<<1 + 1
					}
				}
			} else {
				mp := dicPos - int(rep0)
				if dicPos < int(rep0) {
					mp += dicBufSize
				}
				matchByte := uint32(dic[mp])
				offs := uint32(0x100)
				for symbol < 0x100 {
					matchByte <<= 1
					bit := matchByte & offs
					p := prob + offs + bit + symbol
					ttt := uint32(probs[p])
					if rng < topValue {
						rng <<= 8
						code = code<<8 | uint32(buf[pos])
						pos++
					}
					bound := (rng >> numBitModelTotalBits) * ttt
					if code < bound {
						rng = bound
						probs[p] = uint16(ttt + (bitModelTotal-ttt)>>numMoveBits)
						symbol <<= 1
						offs &^= bit
					} else {
						rng -= bound
						code -= bound
						probs[p] = uint16(ttt - ttt>>numMoveBits)
						symbol = symbol<<1 + 1
						offs &= bit
					}
				}
			}
			dic[dicPos] = byte(symbol)
			dicPos++
			processedPos++
			state = literalNextStates[state]
			if dicPos < limit && pos < bufLimit {
				continue
			}
			break
		}
		rng -= bound
		code -= bound
		probs[prob] = uint16(ttt - ttt>>numMoveBits)

		var bit uint32
		bit, rng, code, pos = decodeBit(probs, isRep+state, rng, code, buf, pos)
		if bit == 0 {
			state += numStates
			prob = lenCoder
		} else {
			if checkDicSize == 0 && processedPos == 0 {
				return pos, errData
			}
			bit, rng, code, pos = decodeBit(probs, isRepG0+state, rng, code, buf, pos)
			if bit == 0 {
				bit, rng, code, pos = decodeBit(probs, isRep0Long+state<<numPosBitsMax+posState, rng, code, buf, pos)
				if bit == 0 {
					sp := dicPos - int(rep0)
					if dicPos < int(rep0) {
						sp += dicBufSize
					}
					dic[dicPos] = dic[sp]
					dicPos++
					processedPos++
					if state < numLitStates {
						state = 9
					} else {
						state = 11
					}
					if dicPos < limit && pos < bufLimit {
						continue
					}
					break
				}
			} else {
				var distance uint32
				bit, rng, code, pos = decodeBit(probs, isRepG1+state, rng, code, buf, pos)
				if bit == 0 {
					distance = rep1
				} else {
					bit, rng, code, pos = decodeBit(probs, isRepG2+state, rng, code, buf, pos)
					if bit == 0 {
						distance = rep2
					} else {
						distance = rep3
						rep3 = rep2
					}
					rep2 = rep1
				}
				rep1 = rep0
				rep0 = distance
			}
			if state < numLitStates {
				state = 8
			} else {
				state = 11
			}
			prob = repLenCoder
		}

		// Match length.
		var lim, offset uint32
		probLen := prob + lenChoice
		bit, rng, code, pos = decodeBit(probs, probLen, rng, code, buf, pos)
		if bit == 0 {
			probLen = prob + lenLow + posState<<lenNumLowBits
			offset, lim = 0, 1<<lenNumLowBits
		} else {
			bit, rng, code, pos = decodeBit(probs, prob+lenChoice2, rng, code, buf, pos)
			if bit == 0 {
				probLen = prob + lenMid + posState<<lenNumMidBits
				offset, lim = lenNumLowSymbols, 1<<lenNumMidBits
			} else {
				probLen = prob + lenHigh
				offset, lim = lenNumLowSymbols+lenNumMidSymbols, 1<<lenNumHighBits
			}
		}
		length = 1
		for length < lim {
			bit, rng, code, pos = decodeBit(probs, probLen+length, rng, code, buf, pos)
			length = length<<1 + bit
		}
		length = length - lim + offset

		if state >= numStates {
			lps := min(length, numLenToPosStates-1)
			p := posSlot + lps<<numPosSlotBits
			distance := uint32(1)
			for range 6 {
				bit, rng, code, pos = decodeBit(probs, p+distance, rng, code, buf, pos)
				distance = distance<<1 + bit
			}
			distance -= 0x40
			if distance >= startPosModelIndex {
				slot := distance
				numDirectBits := int(distance>>1) - 1
				distance = 2 | distance&1
				if slot < endPosModelIndex {
					distance <<= uint(numDirectBits)
					p := specPos + distance - slot - 1
					mask, i := uint32(1), uint32(1)
					for ; numDirectBits > 0; numDirectBits-- {
						bit, rng, code, pos = decodeBit(probs, p+i, rng, code, buf, pos)
						i = i<<1 + bit
						if bit != 0 {
							distance |= mask
						}
						mask <<= 1
					}
				} else {
					for numDirectBits -= numAlignBits; numDirectBits > 0; numDirectBits-- {
						if rng < topValue {
							rng <<= 8
							code = code<<8 | uint32(buf[pos])
							pos++
						}
						rng >>= 1
						code -= rng
						t := 0 - code>>31
						distance = distance<<1 + (t + 1)
						code += rng & t
					}
					distance <<= numAlignBits
					i := uint32(1)
					for k := range uint32(numAlignBits) {
						bit, rng, code, pos = decodeBit(probs, align+i, rng, code, buf, pos)
						i = i<<1 + bit
						if bit != 0 {
							distance |= 1 << k
						}
					}
					if distance == 0xFFFFFFFF {
						length += matchSpecLenStart
						state -= numStates
						break loop
					}
				}
			}
			rep3, rep2, rep1 = rep2, rep1, rep0
			rep0 = distance + 1
			if checkDicSize == 0 {
				if distance >= processedPos {
					return pos, errData
				}
			} else if distance >= checkDicSize {
				return pos, errData
			}
			if state < numStates+numLitStates {
				state = numLitStates
			} else {
				state = numLitStates + 3
			}
		}

		length += matchMinLen
		if limit == dicPos {
			return pos, errData
		}
		curLen := min(limit-dicPos, int(length))
		src := dicPos - int(rep0)
		if dicPos < int(rep0) {
			src += dicBufSize
		}
		processedPos += uint32(curLen)
		length -= uint32(curLen)
		if src+curLen <= dicBufSize {
			if int(rep0) >= curLen {
				copy(dic[dicPos:dicPos+curLen], dic[src:src+curLen])
			} else {
				for i := range curLen {
					dic[dicPos+i] = dic[src+i]
				}
			}
			dicPos += curLen
		} else {
			for ; curLen > 0; curLen-- {
				dic[dicPos] = dic[src]
				dicPos++
				if src++; src == dicBufSize {
					src = 0
				}
			}
		}
		if !(dicPos < limit && pos < bufLimit) {
			break
		}
	}
	if rng < topValue {
		rng <<= 8
		code = code<<8 | uint32(buf[pos])
		pos++
	}
	d.rng, d.code = rng, code
	d.remainLen = length
	d.dicPos = dicPos
	d.processedPos = processedPos
	d.reps = [4]uint32{rep0, rep1, rep2, rep3}
	d.state = state
	return pos, nil
}

// decodeBit decodes one bit with the adaptive probability probs[i].
func decodeBit(probs []uint16, i, rng, code uint32, buf []byte, pos int) (bit, rng2, code2 uint32, pos2 int) {
	ttt := uint32(probs[i])
	if rng < topValue {
		rng <<= 8
		code = code<<8 | uint32(buf[pos])
		pos++
	}
	bound := (rng >> numBitModelTotalBits) * ttt
	if code < bound {
		probs[i] = uint16(ttt + (bitModelTotal-ttt)>>numMoveBits)
		return 0, bound, code, pos
	}
	probs[i] = uint16(ttt - ttt>>numMoveBits)
	return 1, rng - bound, code - bound, pos
}

// writeRem finishes a match that did not fit before the previous limit.
func (d *decoder) writeRem(limit int) {
	if d.remainLen == 0 || d.remainLen >= matchSpecLenStart {
		return
	}
	dic := d.dic
	dicPos := d.dicPos
	length := d.remainLen
	rep0 := int(d.reps[0])
	if uint64(limit-dicPos) < uint64(length) {
		length = uint32(limit - dicPos)
	}
	if d.checkDicSize == 0 && d.dictSize-d.processedPos <= length {
		d.checkDicSize = d.dictSize
	}
	d.processedPos += length
	d.remainLen -= length
	for ; length > 0; length-- {
		src := dicPos - rep0
		if dicPos < rep0 {
			src += d.dicBufSize
		}
		dic[dicPos] = dic[src]
		dicPos++
	}
	d.dicPos = dicPos
}

func (d *decoder) decodeReal2(limit int, buf []byte, pos, bufLimit int) (int, error) {
	for {
		limit2 := limit
		if d.checkDicSize == 0 {
			rem := d.dictSize - d.processedPos
			if uint64(limit-d.dicPos) > uint64(rem) {
				limit2 = d.dicPos + int(rem)
			}
		}
		var err error
		if pos, err = d.decodeReal(limit2, buf, pos, bufLimit); err != nil {
			return pos, err
		}
		if d.processedPos >= d.dictSize {
			d.checkDicSize = d.dictSize
		}
		d.writeRem(limit)
		if !(d.dicPos < limit && pos < bufLimit && d.remainLen < matchSpecLenStart) {
			break
		}
	}
	if d.remainLen > matchSpecLenStart {
		d.remainLen = matchSpecLenStart
	}
	return pos, nil
}

// dummy is the range decoder state tryDummy works on.
type dummy struct {
	rng, code uint32
	buf       []byte
	pos       int
}

// normalize reports false when the input ends before the next byte.
func (r *dummy) normalize() bool {
	if r.rng < topValue {
		if r.pos >= len(r.buf) {
			return false
		}
		r.rng <<= 8
		r.code = r.code<<8 | uint32(r.buf[r.pos])
		r.pos++
	}
	return true
}

// bit decodes a bit without updating the probability.
func (r *dummy) bit(prob uint16) (uint32, bool) {
	if !r.normalize() {
		return 0, false
	}
	bound := (r.rng >> numBitModelTotalBits) * uint32(prob)
	if r.code < bound {
		r.rng = bound
		return 0, true
	}
	r.rng -= bound
	r.code -= bound
	return 1, true
}

// tree decodes a bit tree of limit leaves.
func (r *dummy) tree(probs []uint16, base, limit uint32) (uint32, bool) {
	i := uint32(1)
	for i < limit {
		b, ok := r.bit(probs[base+i])
		if !ok {
			return 0, false
		}
		i = i<<1 + b
	}
	return i - limit, true
}

// tryDummy checks whether buf holds a whole symbol, and of which kind (LzmaDec_TryDummy).
func (d *decoder) tryDummy(buf []byte) int {
	r := dummy{rng: d.rng, code: d.code, buf: buf}
	probs := d.probs
	state := d.state
	posState := d.processedPos & (1<<d.pb - 1)
	var res int

	b, ok := r.bit(probs[isMatch+state<<numPosBitsMax+posState])
	if !ok {
		return dummyError
	}
	if b == 0 {
		prob := uint32(literal)
		if d.checkDicSize != 0 || d.processedPos != 0 {
			prev := d.dicPos
			if prev == 0 {
				prev = d.dicBufSize
			}
			prob += litSize * ((d.processedPos&(1<<d.lp-1))<<d.lc + uint32(d.dic[prev-1])>>(8-d.lc))
		}
		symbol := uint32(1)
		if state < numLitStates {
			for symbol < 0x100 {
				b, ok := r.bit(probs[prob+symbol])
				if !ok {
					return dummyError
				}
				symbol = symbol<<1 + b
			}
		} else {
			mp := d.dicPos - int(d.reps[0])
			if d.dicPos < int(d.reps[0]) {
				mp += d.dicBufSize
			}
			matchByte := uint32(d.dic[mp])
			offs := uint32(0x100)
			for symbol < 0x100 {
				matchByte <<= 1
				bit := matchByte & offs
				b, ok := r.bit(probs[prob+offs+bit+symbol])
				if !ok {
					return dummyError
				}
				symbol = symbol<<1 + b
				if b == 0 {
					offs &^= bit
				} else {
					offs &= bit
				}
			}
		}
		res = dummyLit
	} else {
		var prob uint32
		b, ok := r.bit(probs[isRep+state])
		if !ok {
			return dummyError
		}
		if b == 0 {
			state = 0
			prob = lenCoder
			res = dummyMatch
		} else {
			res = dummyRep
			b, ok := r.bit(probs[isRepG0+state])
			if !ok {
				return dummyError
			}
			if b == 0 {
				b, ok := r.bit(probs[isRep0Long+state<<numPosBitsMax+posState])
				if !ok {
					return dummyError
				}
				if b == 0 {
					if !r.normalize() {
						return dummyError
					}
					return dummyRep
				}
			} else {
				b, ok := r.bit(probs[isRepG1+state])
				if !ok {
					return dummyError
				}
				if b != 0 {
					if _, ok := r.bit(probs[isRepG2+state]); !ok {
						return dummyError
					}
				}
			}
			state = numStates
			prob = repLenCoder
		}
		var lim, offset, base uint32
		b, ok = r.bit(probs[prob+lenChoice])
		if !ok {
			return dummyError
		}
		if b == 0 {
			base, offset, lim = prob+lenLow+posState<<lenNumLowBits, 0, 1<<lenNumLowBits
		} else {
			b, ok := r.bit(probs[prob+lenChoice2])
			if !ok {
				return dummyError
			}
			if b == 0 {
				base, offset, lim = prob+lenMid+posState<<lenNumMidBits, lenNumLowSymbols, 1<<lenNumMidBits
			} else {
				base, offset, lim = prob+lenHigh, lenNumLowSymbols+lenNumMidSymbols, 1<<lenNumHighBits
			}
		}
		length, ok := r.tree(probs, base, lim)
		if !ok {
			return dummyError
		}
		length += offset
		if state < 4 {
			slot, ok := r.tree(probs, posSlot+min(length, numLenToPosStates-1)<<numPosSlotBits, 1<<numPosSlotBits)
			if !ok {
				return dummyError
			}
			if slot >= startPosModelIndex {
				numDirectBits := int(slot>>1) - 1
				var p uint32
				if slot < endPosModelIndex {
					p = specPos + (2|slot&1)<<uint(numDirectBits) - slot - 1
				} else {
					for numDirectBits -= numAlignBits; numDirectBits > 0; numDirectBits-- {
						if !r.normalize() {
							return dummyError
						}
						r.rng >>= 1
						r.code -= r.rng & ((r.code-r.rng)>>31 - 1)
					}
					p = align
					numDirectBits = numAlignBits
				}
				i := uint32(1)
				for ; numDirectBits > 0; numDirectBits-- {
					b, ok := r.bit(probs[p+i])
					if !ok {
						return dummyError
					}
					i = i<<1 + b
				}
			}
		}
	}
	if !r.normalize() {
		return dummyError
	}
	return res
}

// decodeToDic decodes src into the dictionary up to dicLimit (LzmaDec_DecodeToDic with LZMA_FINISH_ANY).
// It returns how much of src it consumed.
func (d *decoder) decodeToDic(dicLimit int, src []byte) (int, status, error) {
	srcLen := 0
	d.writeRem(dicLimit)
	for d.remainLen != matchSpecLenStart {
		if d.needFlush {
			for len(src) > 0 && d.tempBufSize < rcInitSize {
				d.tempBuf[d.tempBufSize] = src[0]
				d.tempBufSize++
				src = src[1:]
				srcLen++
			}
			if d.tempBufSize < rcInitSize {
				return srcLen, statusNeedsMoreInput, nil
			}
			if d.tempBuf[0] != 0 {
				return srcLen, statusNotSpecified, errData
			}
			d.initRc(d.tempBuf[:])
			d.tempBufSize = 0
		}
		if d.dicPos >= dicLimit {
			if d.remainLen == 0 && d.code == 0 {
				return srcLen, statusMaybeFinishedWithoutMark, nil
			}
			return srcLen, statusNotFinished, nil
		}
		if d.needInitState {
			d.initStateReal()
		}
		if d.tempBufSize == 0 {
			bufLimit := len(src) - requiredInputMax
			if len(src) < requiredInputMax {
				if d.tryDummy(src) == dummyError {
					copy(d.tempBuf[:], src)
					d.tempBufSize = len(src)
					srcLen += len(src)
					return srcLen, statusNeedsMoreInput, nil
				}
				bufLimit = 0
			}
			// decodeReal reads at most requiredInputMax bytes past bufLimit; with less input than that,
			// tryDummy has proven that the one symbol decoded fits.
			pos, err := d.decodeReal2(dicLimit, src, 0, bufLimit)
			if err != nil {
				return srcLen, statusNotSpecified, err
			}
			srcLen += pos
			src = src[pos:]
		} else {
			rem, lookAhead := d.tempBufSize, 0
			for rem < requiredInputMax && lookAhead < len(src) {
				d.tempBuf[rem] = src[lookAhead]
				rem++
				lookAhead++
			}
			d.tempBufSize = rem
			if rem < requiredInputMax {
				if d.tryDummy(d.tempBuf[:rem]) == dummyError {
					srcLen += lookAhead
					return srcLen, statusNeedsMoreInput, nil
				}
			}
			pos, err := d.decodeReal2(dicLimit, d.tempBuf[:], 0, 0)
			if err != nil {
				return srcLen, statusNotSpecified, err
			}
			lookAhead -= rem - pos
			srcLen += lookAhead
			src = src[lookAhead:]
			d.tempBufSize = 0
		}
	}
	if d.code != 0 {
		return srcLen, statusNotSpecified, errData
	}
	return srcLen, statusFinishedWithMark, nil
}
