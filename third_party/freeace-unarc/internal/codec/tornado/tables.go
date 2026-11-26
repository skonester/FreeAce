package tornado

import (
	"encoding/binary"
	"fmt"

	"github.com/n0madic/go-unarc/internal/method"
)

const (
	// maxTableRow is the longest row of a data table the decoder accepts (MAX_TABLE_ROW_AT_DECOMPRESSION).
	maxTableRow = 256
	// maxTables is the length of the list of tables waiting to be undiffed; a full list forces a write.
	maxTables = 10000
)

var errTable = fmt.Errorf("%w: invalid data table", method.ErrBadData)

// The encoder replaces data tables (arrays of 2- or 4-byte integers, found by a heuristic) by the
// difference of every row to the previous one, which compresses better. Row differences are taken
// "bytewise with carries from the lowest address", which for 2 and 4 byte rows equals little-endian
// integer subtraction. The decoder keeps tables diffed inside the window, so that LZ matches see the same
// data as the encoder did, and undiffs them in place while a chunk is written, diffing them back afterwards.

// diffTable subtracts from each of the rows rows of N bytes at start the contents of the previous one.
func diffTable(buf []byte, n, start, rows int) {
	end := start + n*rows
	switch n {
	case 2:
		prev := binary.LittleEndian.Uint16(buf[start:])
		for r := start + 2; r < end; r += 2 {
			v := binary.LittleEndian.Uint16(buf[r:])
			binary.LittleEndian.PutUint16(buf[r:], v-prev)
			prev = v
		}
	case 4:
		prev := binary.LittleEndian.Uint32(buf[start:])
		for r := start + 4; r < end; r += 4 {
			v := binary.LittleEndian.Uint32(buf[r:])
			binary.LittleEndian.PutUint32(buf[r:], v-prev)
			prev = v
		}
	default:
		for r := end - n; r > start; r -= n {
			carry := 0
			for i := range n {
				prev := int(buf[r+i-n]) + carry
				carry = 0
				if int(buf[r+i]) < prev {
					carry = 1
				}
				buf[r+i] -= byte(prev)
			}
		}
	}
}

// undiffTable adds to each of the rows rows of N bytes at start the contents of the previous one.
func undiffTable(buf []byte, n, start, rows int) {
	end := start + n*rows
	switch n {
	case 2:
		v := binary.LittleEndian.Uint16(buf[start:])
		for r := start + 2; r < end; r += 2 {
			v += binary.LittleEndian.Uint16(buf[r:])
			binary.LittleEndian.PutUint16(buf[r:], v)
		}
	case 4:
		v := binary.LittleEndian.Uint32(buf[start:])
		for r := start + 4; r < end; r += 4 {
			v += binary.LittleEndian.Uint32(buf[r:])
			binary.LittleEndian.PutUint32(buf[r:], v)
		}
	default:
		for r := start + n; r < end; r += n {
			carry := 0
			for i := range n {
				t := int(buf[r+i]) + int(buf[r+i-n]) + carry
				buf[r+i] = byte(t)
				carry = t >> 8
			}
		}
	}
}

// tableEntry is a table announced by the stream: rows rows of typ bytes, starting at buf[start].
type tableEntry struct{ typ, start, rows int }

// dataTables is the list of tables that were not yet undiffed (C DataTables). Tables divided between two
// write chunks are the tricky part: the last table of a chunk is kept, from two rows before the end of what
// was written, and its base row (the one the first row to write adds to) is saved undiffed.
type dataTables struct {
	list          []tableEntry
	baseData      [maxTableRow]byte // base row of a table continuing from the previous chunk, undiffed
	original      [maxTableRow]byte // the diffed bytes it temporarily replaces
	baseDataBytes int
}

func (t *dataTables) add(typ, start, rows int) {
	t.list = append(t.list, tableEntry{typ, start, rows})
}

// filled reports that the list is full and must be written out before a table can be added.
func (t *dataTables) filled() bool { return len(t.list) >= maxTables }

// process diffs or undiffs all tables of the list up to the buffer point writeEnd.
func (t *dataTables) process(buf []byte, undiff bool, writeEnd int) error {
	for _, p := range t.list {
		// Truncate the number of rows if the table ends after the output pointer.
		rows := min(p.rows, 1+(writeEnd-p.start)/p.typ)
		if p.start < 0 || p.start+p.typ > len(buf) || p.start+p.typ*max(rows, 0) > len(buf) {
			return errTable
		}
		if undiff {
			undiffTable(buf, p.typ, p.start, rows)
		} else {
			diffTable(buf, p.typ, p.start, rows)
		}
	}
	return nil
}

// undiff restores the contents of the tables in the list, preparing buf[writeStart:writeEnd] to be written.
func (t *dataTables) undiff(buf []byte, writeStart, writeEnd int) error {
	if len(t.list) > 0 && t.list[0].start < writeStart {
		// The first table continues from the previous write chunk: put the saved base row at its
		// beginning for the duration of the undiffing, then restore the original bytes.
		p := t.list[0]
		t.baseDataBytes = min(p.typ, writeStart-p.start)
		if p.start < 0 || p.start+t.baseDataBytes > len(buf) {
			return errTable
		}
		copy(t.original[:], buf[p.start:p.start+t.baseDataBytes])
		copy(buf[p.start:], t.baseData[:t.baseDataBytes])
	}
	return t.process(buf, true, writeEnd)
}

// diff is called after the data was written. It diffs the tables again so that LZ matches may use them, and
// empties the list, leaving only the last table when it continues past writeEnd.
func (t *dataTables) diff(buf []byte, writeStart, writeEnd int) error {
	var tail *tableEntry
	if n := len(t.list); n > 0 {
		p := t.list[n-1]
		processed := (writeEnd - p.start) / p.typ
		if processed < p.rows {
			// Keep two more rows: one as the base for undiffing, another because it may be divided
			// between two write chunks.
			processed = max(processed-2, 0)
			p.start += processed * p.typ
			p.rows -= processed
			if p.start < 0 || p.start+p.typ > len(buf) {
				return errTable
			}
			copy(t.baseData[:], buf[p.start:p.start+p.typ])
			tail = &p
		}
	}
	if err := t.process(buf, false, writeEnd); err != nil {
		return err
	}
	if len(t.list) > 0 && t.list[0].start < writeStart {
		copy(buf[t.list[0].start:], t.original[:t.baseDataBytes])
	}
	t.list = t.list[:0]
	if tail != nil {
		t.list = append(t.list, *tail)
	}
	return nil
}

// shift moves the list entries when the output pointer returns from oldPos to newPos at the buffer start.
// The head of a table that lies before oldPos is copied before the new buffer start, where undiffing needs it.
func (t *dataTables) shift(buf []byte, oldPos, newPos int) error {
	if len(t.list) > 1 {
		return errTable
	}
	for i := range t.list {
		p := &t.list[i]
		old := p.start
		p.start -= oldPos - newPos
		n := newPos - p.start
		if n < 0 || p.start < 0 || old+n > len(buf) {
			return errTable
		}
		copy(buf[p.start:newPos], buf[old:old+n])
	}
	return nil
}
