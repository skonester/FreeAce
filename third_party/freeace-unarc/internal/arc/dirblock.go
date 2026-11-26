package arc

import (
	"fmt"
	"strings"

	"github.com/n0madic/go-unarc/internal/method"
)

// File is one entry of a directory block.
type File struct {
	Dir   string // directory inside the archive, '/'-separated ("" for the root)
	Name  string // name without the directory
	Size  uint64
	Time  uint32 // modification time, Unix seconds
	IsDir bool
	CRC   uint32
}

// FullName is the path of the file inside the archive.
func (f *File) FullName() string {
	if f.Dir == "" {
		return f.Name
	}
	return f.Dir + "/" + f.Name
}

// DataBlock is a solid block: files [Start, End) of the directory, compressed together.
type DataBlock struct {
	Compressor string
	Pos        uint64
	CompSize   uint64
	Start, End int
}

// Dir is a decoded directory block.
type Dir struct {
	Files  []File
	Blocks []DataBlock
}

// unsupportedPathDelimiters are replaced by '/' in directory names (UNSUPPORTED_PATH_DELIMITERS on Unix).
var unsupportedPathDelimiters = strings.NewReplacer("\\", "/")

// ReadDir reads and decodes a directory block.
func (a *Archive) ReadDir(blk Block) (*Dir, error) {
	if blk.Type != DirBlock {
		return nil, Errorf("INTERNAL ERROR: must be dir block")
	}
	data, err := a.ReadControlBlock(blk)
	if err != nil {
		return nil, err
	}
	b := &buffer{data: data}
	nblocks, err := b.readCount(4, errBadData) // four arrays, at least a byte per block in each
	if err != nil {
		return nil, err
	}
	numFiles, err := b.readIntegers(nblocks)
	if err != nil {
		return nil, err
	}
	compressors, err := b.readStrings(nblocks)
	if err != nil {
		return nil, err
	}
	offsets, err := b.readIntegers(nblocks)
	if err != nil {
		return nil, err
	}
	compsizes, err := b.readIntegers(nblocks)
	if err != nil {
		return nil, err
	}
	d := &Dir{Blocks: make([]DataBlock, nblocks)}
	var total uint64
	maxFiles := uint64(b.remaining() / 12) // name, directory, size, time, flag, CRC
	for i := range d.Blocks {
		db := &d.Blocks[i]
		db.Compressor = compressors[i]
		if offsets[i] > blk.Pos || compsizes[i] > offsets[i] {
			return nil, errBadData
		}
		db.Pos = blk.Pos - offsets[i]
		db.CompSize = compsizes[i]
		if a.env.ShowBlocks {
			fmt.Fprintf(a.env.Stderr, "datablock %s %d %d\n", method.PublicCompressor(db.Compressor), int64(db.Pos), int64(db.CompSize))
		}
		if numFiles[i] > maxFiles-total {
			return nil, errBadString
		}
		db.Start = int(total)
		total += numFiles[i]
		db.End = int(total)
	}

	ndirs, err := b.readCount(1, errBadString)
	if err != nil {
		return nil, err
	}
	dirs, err := b.readStrings(ndirs)
	if err != nil {
		return nil, err
	}
	for i := range dirs {
		dirs[i] = unsupportedPathDelimiters.Replace(dirs[i])
	}

	n := int(total)
	names, err := b.readStrings(n)
	if err != nil {
		return nil, err
	}
	dirNumbers, err := b.readIntegers(n)
	if err != nil {
		return nil, err
	}
	sizes, err := b.readIntegers(n)
	if err != nil {
		return nil, err
	}
	d.Files = make([]File, n)
	for i := range d.Files {
		f := &d.Files[i]
		if dirNumbers[i] >= uint64(len(dirs)) {
			return nil, errBadData
		}
		f.Dir, f.Name, f.Size = dirs[dirNumbers[i]], names[i], sizes[i]
	}
	for i := range d.Files {
		if d.Files[i].Time, err = b.read4(); err != nil {
			return nil, err
		}
	}
	for i := range d.Files {
		v, err := b.read1()
		if err != nil {
			return nil, err
		}
		d.Files[i].IsDir = v != 0
	}
	for i := range d.Files {
		if d.Files[i].CRC, err = b.read4(); err != nil {
			return nil, err
		}
	}
	return d, nil
}
