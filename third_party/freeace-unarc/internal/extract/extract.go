// Package extract tests and extracts the files of solid blocks, cutting the decompressed stream into files
// the way PROCESS::ExtractFiles of the C unarc does.
package extract

import (
	"crypto/rand"
	"errors"
	"hash"
	"os"
	"path/filepath"
	"strings"

	"github.com/n0madic/go-unarc/internal/arc"
	"github.com/n0madic/go-unarc/internal/cleanup"
	"github.com/n0madic/go-unarc/internal/method"
)

// UI receives progress and answers the overwrite question.
type UI interface {
	// ProgressFile reports a file (or, during 'x', a directory) being extracted, tested or skipped.
	ProgressFile(isDir bool, operation, name string, size uint64)
	// AskOverwrite asks whether to overwrite name; it answers one of 'y', 'n', 'a', 's', 'q'.
	AskOverwrite(name string, size uint64, mtime uint32) byte
}

// ErrAborted is returned when the user answers 'q' to the overwrite question.
var ErrAborted = errors.New("extraction aborted")

// Options of an extract or test command.
type Options struct {
	Cmd       byte // 'e', 'x' or 't'
	OutDir    string
	Filenames map[string]bool // full archive paths to process; nil means all
	Yes, No   bool            // -o+ / -o-
	UI        UI
	Env       *method.Env
}

func (p *Process) accepts(f *arc.File) bool {
	return p.opts.Filenames == nil || p.opts.Filenames[f.FullName()]
}

// Process extracts or tests one archive, keeping the state (-o+/-o- answers) across solid blocks.
type Process struct {
	opts Options
	base string // destination prefix for diagnostics
	root *os.Root

	// state of the solid block being extracted
	dir          *arc.Dir
	cur          int
	extractUntil int
	included     bool
	bytesToWrite uint64
	crc          hash.Hash32
	xname        string // name shown in progress lines
	outName      string // path shown in diagnostics
	relName      string // path relative to root
	out          *os.File
	tmpName      string
	forget       func()
	replace      bool
}

// New prepares extraction or testing.
func New(opts Options) *Process {
	p := &Process{opts: opts}
	if opts.Cmd != 't' {
		p.base = opts.OutDir
		if p.base != "" && !os.IsPathSeparator(p.base[len(p.base)-1]) {
			p.base += "/"
		}
	}
	return p
}

// Abort discards only the staged file, never the destination of a failed write.
func (p *Process) Abort() {
	if p.out != nil {
		_ = p.out.Close()
		_ = p.root.Remove(p.tmpName)
		p.out = nil
		p.forget()
	}
}

// Close releases the output root and discards any unfinished file.
func (p *Process) Close() {
	p.Abort()
	if p.root != nil {
		_ = p.root.Close()
		p.root = nil
	}
}

func (p *Process) fullDecode() bool { return p.opts.Filenames == nil }

// ExtractBlock processes the files of solid block b of dir d.
func (p *Process) ExtractBlock(a *arc.Archive, d *arc.Dir, b int) error {
	p.dir = d
	p.crc = a.NewChecksum()
	blk := d.Blocks[b]
	p.extractUntil = -1
	// First pass: directories and empty files right away; remember the last file that needs data.
	for p.cur = blk.Start; p.cur < blk.End; p.cur++ {
		if !p.accepts(&d.Files[p.cur]) {
			continue
		}
		if d.Files[p.cur].Size == 0 {
			if err := p.open(true); err != nil {
				return err
			}
			if err := p.close(); err != nil {
				return err
			}
		} else {
			p.extractUntil = p.cur
		}
	}
	if p.extractUntil < 0 && (!p.fullDecode() || blk.CompSize == 0) {
		return nil
	}

	// Second pass: decompress the block and cut the stream into files.
	src, err := a.SectionReader(blk.Pos, blk.CompSize)
	if err != nil {
		return err
	}
	p.cur = blk.Start
	if p.extractUntil >= 0 {
		if err := p.open(false); err != nil {
			return err
		}
	}
	compressor := method.AddTempfile(blk.Compressor, min(method.MaxMem(), p.opts.Env.DecoderLimit()), p.opts.Env)
	if p.fullDecode() {
		err = method.RunFull(compressor, p, src, p.opts.Env)
	} else {
		err = method.Run(compressor, p, src, p.opts.Env)
	}
	var fatal *arc.Error
	switch {
	case errors.As(err, &fatal), errors.Is(err, ErrAborted):
		return err
	case errors.Is(err, method.ErrUnsupportedMethod):
		return arc.Errorf("ERROR: unsupported compression method %s", method.PublicCompressor(blk.Compressor))
	case errors.Is(err, method.ErrLimit):
		return arc.Errorf("ERROR: %v", err)
	case err != nil:
		return arc.Errorf("ERROR: archive data corrupted (decompression fails)")
	}
	stoppedAt := p.cur
	if err := p.close(); err != nil {
		return err
	}
	// The stream ended before the last file that had to be extracted. When that happens inside an extracted
	// file, its CRC check above fails as in C; when inside a skipped one, C silently missed the rest.
	if stoppedAt <= p.extractUntil {
		return arc.Errorf("ERROR: archive data corrupted (decompression fails)")
	}
	return nil
}

// Write receives the decompressed stream of the solid block (DecompressCallback "write").
func (p *Process) Write(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	if p.cur > p.extractUntil {
		if p.fullDecode() {
			return 0, method.ErrBadData
		}
		return 0, method.ErrStop
	}
	n0 := len(buf)
	for len(buf) > 0 {
		n := int(min(p.bytesToWrite, uint64(len(buf))))
		if err := p.write(buf[:n]); err != nil {
			return n0 - len(buf), err
		}
		buf = buf[n:]
		p.bytesToWrite -= uint64(n)
		if p.bytesToWrite == 0 {
			if err := p.close(); err != nil {
				return n0 - len(buf), err
			}
			p.cur++
			if p.cur > p.extractUntil {
				if p.fullDecode() {
					if len(buf) != 0 {
						return n0 - len(buf), method.ErrBadData
					}
					return n0, nil // let the decoder verify the end of the block
				}
				return n0 - len(buf), method.ErrStop
			}
			if err := p.open(false); err != nil {
				return n0 - len(buf), err
			}
		}
	}
	return n0, nil
}

// UnsafePath reports whether a name from the archive could lead outside the output folder:
// an absolute path, a drive letter, or a ".." component (with either kind of slash).
func UnsafePath(name string) bool {
	if name == "" {
		return false
	}
	if name[0] == '/' || name[0] == '\\' {
		return true
	}
	if len(name) >= 2 && name[1] == ':' && ('a' <= name[0]|0x20 && name[0]|0x20 <= 'z') {
		return true
	}
	for _, part := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return true
		}
	}
	return false
}

// open starts file p.cur (PROCESS::outfile_open).
func (p *Process) open(firstPass bool) error {
	f := &p.dir.Files[p.cur]
	p.crc.Reset()
	p.bytesToWrite = f.Size
	if !firstPass && f.Size == 0 {
		return nil // directories and empty files were done in the first pass
	}
	cmd := p.opts.Cmd
	p.included = p.accepts(f)
	p.xname = f.FullName()
	if cmd == 'e' {
		p.xname = f.Name
	}
	p.outName = p.base + p.xname
	if p.included && cmd != 't' && !(f.IsDir && cmd == 'e') {
		p.relName = filepath.FromSlash(p.xname)
		if UnsafePath(p.xname) || !filepath.IsLocal(p.relName) {
			return arc.Errorf("ERROR: unsafe path in archive: %s", p.xname)
		}
		if err := p.openRoot(); err != nil {
			return arc.Errorf("ERROR: can't open output directory: %v", err)
		}
		if f.IsDir {
			if err := p.root.MkdirAll(p.relName, 0o777); err != nil {
				return arc.Errorf("ERROR: can't create directory %s: %v", p.outName, err)
			}
		} else {
			if err := p.askOverwrite(f); err != nil {
				return err
			}
			if p.included {
				if err := p.create(); err != nil {
					return err
				}
			}
		}
	}
	if (firstPass || f.Size > 0) && !(f.IsDir && cmd != 'x') {
		op := "Skipping"
		if p.included {
			op = "Extracting"
			if cmd == 't' {
				op = "Testing"
			}
		}
		p.opts.UI.ProgressFile(f.IsDir, op, p.xname, p.bytesToWrite)
	}
	return nil
}

func (p *Process) openRoot() error {
	if p.root != nil {
		return nil
	}
	dir := p.opts.OutDir
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return err
	}
	var err error
	p.root, err = os.OpenRoot(dir)
	return err
}

func (p *Process) askOverwrite(f *arc.File) error {
	p.replace = p.opts.Yes && !p.opts.No
	if _, err := p.root.Lstat(p.relName); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return arc.Errorf("ERROR: can't open file %s: %v", p.outName, err)
	}
	switch {
	case p.opts.No:
		p.included = false
	case !p.opts.Yes:
		switch p.opts.UI.AskOverwrite(p.outName, f.Size, f.Time) {
		case 'y':
			p.replace = true
		case 'n':
			p.included = false
		case 'a':
			p.opts.Yes = true
			p.replace = true
		case 's':
			p.opts.No = true
			p.included = false
		case 'q':
			return ErrAborted
		}
	}
	return nil
}

// create stages the file alongside its destination so publishing it needs no cross-volume copy.
func (p *Process) create() error {
	parent := filepath.Dir(p.relName)
	if err := p.root.MkdirAll(parent, 0o777); err != nil {
		return arc.Errorf("ERROR: can't open file %s: %v", p.outName, err)
	}
	for range 10 {
		name := filepath.Join(parent, ".unarc-"+rand.Text())
		out, err := p.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return arc.Errorf("ERROR: can't open file %s: %v", p.outName, err)
		}
		root := p.root
		p.out, p.tmpName = out, name
		p.forget = cleanup.RegisterFunc(func() {
			_ = out.Close()
			_ = root.Remove(name)
		})
		return nil
	}
	return arc.Errorf("ERROR: can't stage file %s", p.outName)
}

func (p *Process) write(buf []byte) error {
	p.crc.Write(buf)
	if p.out != nil && len(buf) > 0 {
		if _, err := p.out.Write(buf); err != nil {
			return arc.Errorf("ERROR: file write operation failed")
		}
	}
	return nil
}

// close finishes file p.cur: checks its CRC, closes it and sets its time (PROCESS::outfile_close).
func (p *Process) close() error {
	if p.included {
		f := &p.dir.Files[p.cur]
		if p.crc.Sum32() != f.CRC {
			return arc.Errorf("ERROR: file %s failed CRC check", p.outName)
		}
		if p.bytesToWrite != 0 {
			return arc.Errorf("ERROR: archive data corrupted (decompression fails)")
		}
		if p.out != nil {
			setMtime(p.out, f.Time)
			if err := p.out.Close(); err != nil {
				return arc.Errorf("ERROR: can't close file %s", p.outName)
			}
			if err := p.publish(); err != nil {
				return arc.Errorf("ERROR: can't publish file %s: %v", p.outName, err)
			}
			p.out = nil
			p.forget()
		}
	}
	p.included = false
	return nil
}

// publish uses an exclusive link when replacement was not authorized. Unlike a second Lstat,
// it cannot overwrite a file that appears between the prompt and the commit.
func (p *Process) publish() error {
	if p.replace {
		return p.root.Rename(p.tmpName, p.relName)
	}
	if err := p.root.Link(p.tmpName, p.relName); err != nil {
		return err
	}
	return p.root.Remove(p.tmpName)
}
