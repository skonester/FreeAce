// Package arc reads the structure of FreeArc archives: the footer block found at the end of the file,
// the control block descriptors it lists, and the directory blocks describing files and solid blocks.
package arc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"math"
	"os"

	"github.com/n0madic/go-unarc/internal/method"
)

// Signature starts every block descriptor: "ArC\x01".
const Signature = 0x01437241

// MaxFooterDescriptorSize bounds the tail of the file searched for the footer descriptor.
const MaxFooterDescriptorSize = 4096

// Some RUNE installer archives use this reflected polynomial in place of
// IEEE. The footer descriptor selects the table by matching its checksum.
var runeCRC = crc32.MakeTable(0x8e092638)

// BlockType is the kind of an archive block.
type BlockType uint64

// Block types.
const (
	DescrBlock BlockType = iota
	HeaderBlock
	DataBlockType
	DirBlock
	FooterBlock
	RecoveryBlock
)

// Block describes a block of the archive.
type Block struct {
	Type       BlockType
	Compressor string
	Pos        uint64 // absolute position of the block's data in the file
	OrigSize   uint64
	CompSize   uint64
	CRC        uint32
}

// Error is a fatal error with the exact text unarc prints for it.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

// Errorf builds an Error.
func Errorf(format string, args ...any) *Error { return &Error{Msg: fmt.Sprintf(format, args...)} }

var (
	errBadData   = Errorf("ERROR: archive structure corrupted (bad data)")
	errBadString = Errorf("ERROR: archive structure corrupted (bad string)")
	errSeek      = Errorf("ERROR: file seek operation failed")
)

// Archive is an open archive with its structure read.
type Archive struct {
	File     *os.File
	Size     uint64
	Blocks   []Block // control blocks listed in the footer
	Locked   bool
	Comment  []byte
	SFXSize  uint64
	Name     string
	env      *method.Env
	footer   Block
	footerAt uint64
	crcTable *crc32.Table
}

func (a *Archive) NewChecksum() hash.Hash32 { return crc32.New(a.crcTable) }

// Open opens the archive file and reads its structure.
func Open(name string, env *method.Env) (*Archive, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, Errorf("ERROR: can't open file %s", name)
	}
	a := &Archive{File: f, Name: name, env: env, crcTable: crc32.IEEETable}
	if err := a.readStructure(); err != nil {
		f.Close()
		return nil, err
	}
	return a, nil
}

// Close closes the archive file.
func (a *Archive) Close() error { return a.File.Close() }

// readAt reads size bytes at pos, failing as MYFILE::seek/read do.
func (a *Archive) readAt(pos, size uint64) ([]byte, error) {
	if pos > math.MaxInt64 {
		return nil, errSeek
	}
	if size > math.MaxInt || size > a.Size || pos > a.Size-size {
		return nil, Errorf("ERROR: can't read %d bytes", size)
	}
	buf := make([]byte, size)
	if _, err := a.File.ReadAt(buf, int64(pos)); err != nil {
		return nil, Errorf("ERROR: can't read %d bytes", size)
	}
	return buf, nil
}

// findFooterDescriptor returns the position of the last signature in the last 4096 bytes of the file.
func (a *Archive) findFooterDescriptor() (uint64, error) {
	size := min(a.Size, MaxFooterDescriptorSize)
	notArc := Errorf("ERROR: this is not FreeArc archive or this archive is corrupt")
	if size < 4 {
		return 0, notArc
	}
	buf, err := a.readAt(a.Size-size, size)
	if err != nil {
		return 0, err
	}
	for p := int(size) - 4; p >= 0; p-- {
		if binary.LittleEndian.Uint32(buf[p:]) == Signature {
			return a.Size - size + uint64(p), nil
		}
	}
	return 0, notArc
}

// readLocalDescriptor reads the descriptor stored right after a block, protected by the CRC in its last 4 bytes.
func (a *Archive) readLocalDescriptor(pos uint64) (Block, error) {
	size := min(a.Size-pos, MaxFooterDescriptorSize)
	raw, err := a.readAt(pos, size)
	if err != nil {
		return Block{}, err
	}
	if len(raw) < 8 {
		return Block{}, errBadData
	}
	body := raw[:len(raw)-4]
	expected := binary.LittleEndian.Uint32(raw[len(raw)-4:])
	switch {
	case crc32.ChecksumIEEE(body) == expected:
		a.crcTable = crc32.IEEETable
	case crc32.Checksum(body, runeCRC) == expected:
		a.crcTable = runeCRC
	default:
		return Block{}, Errorf("ERROR: archive structure corrupted (descriptor failed CRC check)")
	}
	b := &buffer{data: body}
	sign, err := b.read4()
	if err != nil {
		return Block{}, err
	}
	var d Block
	t, err := b.readInteger()
	if err == nil {
		d.Type = BlockType(t)
		d.Compressor, err = b.readString()
	}
	if err == nil {
		d.OrigSize, err = b.readInteger()
	}
	if err == nil {
		d.CompSize, err = b.readInteger()
	}
	if err == nil {
		d.CRC, err = b.read4()
	}
	if err != nil {
		return Block{}, err
	}
	if sign != Signature || d.OrigSize == 0 || d.CompSize == 0 || d.CompSize > pos {
		return Block{}, Errorf("ERROR: archive structure corrupted (strange descriptor)")
	}
	d.Pos = pos - d.CompSize
	return d, nil
}

// ReadControlBlock reads, decompresses and CRC-checks a control block.
func (a *Archive) ReadControlBlock(b Block) ([]byte, error) {
	if limit := a.env.ControlLimit(); b.CompSize > limit || b.OrigSize > limit {
		return nil, Errorf("ERROR: %v: control block exceeds %d bytes", method.ErrLimit, limit)
	}
	packed, err := a.readAt(b.Pos, b.CompSize)
	if err != nil {
		return nil, err
	}
	data, err := method.DecompressMem(b.Compressor, packed, b.OrigSize, a.env)
	if errors.Is(err, method.ErrUnsupportedMethod) {
		return nil, Errorf("ERROR: unsupported compression method \"%s\"", method.PublicCompressor(b.Compressor))
	}
	if errors.Is(err, method.ErrLimit) {
		return nil, Errorf("ERROR: %v", err)
	}
	if err != nil || uint64(len(data)) != b.OrigSize {
		return nil, Errorf("ERROR: archive structure corrupted (decompression of control block failed)")
	}
	if crc32.Checksum(data, a.crcTable) != b.CRC {
		return nil, Errorf("ERROR: archive structure corrupted (control block failed CRC check)")
	}
	return data, nil
}

func (a *Archive) readStructure() error {
	st, err := a.File.Stat()
	if err != nil {
		return Errorf("ERROR: can't open file %s", a.Name)
	}
	a.Size = uint64(st.Size())
	pos, err := a.findFooterDescriptor()
	if err != nil {
		return err
	}
	footer, err := a.readLocalDescriptor(pos)
	if err != nil {
		return err
	}
	if footer.Type != FooterBlock {
		return Errorf("ERROR: archive structure corrupted (footer block not found)")
	}
	a.footer, a.footerAt = footer, pos
	data, err := a.ReadControlBlock(footer)
	if err != nil {
		return err
	}
	return a.parseFooter(data)
}

func (a *Archive) parseFooter(data []byte) error {
	b := &buffer{data: data}
	// A descriptor takes at least 9 bytes: type, empty compressor, three integers, CRC.
	n, err := b.readCount(9, errBadData)
	if err != nil {
		return err
	}
	a.Blocks = make([]Block, n)
	for i := range a.Blocks {
		d, err := b.readDescriptor()
		if err != nil {
			return err
		}
		if d.Pos > a.footer.Pos || d.CompSize > d.Pos {
			return errBadData
		}
		d.Pos = a.footer.Pos - d.Pos // stored relative to the footer block
		a.Blocks[i] = d
	}
	if n > 0 {
		a.SFXSize = a.Blocks[0].Pos
	}
	locked, err := b.read1()
	if err != nil {
		return err
	}
	a.Locked = locked != 0
	// Old-style comment: UCS-4 characters, of which C keeps the low bytes.
	cmtlen, err := b.readCount(4, errBadData)
	if err != nil {
		return err
	}
	a.Comment = make([]byte, cmtlen)
	for i := range a.Comment {
		a.Comment[i] = b.data[b.p+i*4]
	}
	if err := b.skip(cmtlen * 4); err != nil {
		return err
	}
	if !b.eof() {
		if _, err := b.readString(); err != nil { // recovery record settings
			return err
		}
	}
	if !b.eof() {
		cmtlen, err := b.readCount(1, errBadData)
		if err != nil {
			return err
		}
		if cmtlen > 0 {
			a.Comment = append([]byte(nil), b.data[b.p:b.p+cmtlen]...)
		}
	}
	return nil
}

// SectionReader returns a reader over the packed data of a block contained in the archive.
func (a *Archive) SectionReader(pos, size uint64) (io.Reader, error) {
	if pos > math.MaxInt64 {
		return nil, errSeek
	}
	if pos > a.Size || size > a.Size-pos {
		return nil, Errorf("ERROR: archive structure corrupted (data block extends past the archive)")
	}
	return io.NewSectionReader(a.File, int64(pos), int64(size)), nil
}
