// FreeAce's read-only FreeArc helper. Only built-in data decoders are registered.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/n0madic/go-unarc/internal/arc"
	_ "github.com/n0madic/go-unarc/internal/codec/fourxfour"
	_ "github.com/n0madic/go-unarc/internal/codec/lzma"
	_ "github.com/n0madic/go-unarc/internal/codec/rep"
	_ "github.com/n0madic/go-unarc/internal/codec/storing"
	_ "github.com/n0madic/go-unarc/internal/codec/tornado"
	"github.com/n0madic/go-unarc/internal/extract"
	"github.com/n0madic/go-unarc/internal/method"
)

const maxEntries = 1_000_000
const maxPathBytes = 32 * 1024 * 1024
const maxOutput = 1 << 40

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) < 2 || (args[0] != "l" && args[0] != "t" && args[0] != "x") {
		return fmt.Errorf("Usage: freeace-unarc l|t archive, or x archive destination [member-list]")
	}
	env := &method.Env{Stderr: os.Stderr}
	opts := extract.Options{Cmd: args[0][0], Env: env}
	if opts.Cmd == 'x' {
		if len(args) < 3 || len(args) > 4 {
			return fmt.Errorf("FreeArc extraction requires a destination and takes one optional member list")
		}
		opts.OutDir = args[2]
		env.TempDir = opts.OutDir
	} else if len(args) != 2 {
		return fmt.Errorf("Unexpected FreeArc arguments")
	}
	a, err := arc.Open(args[1], env)
	if err != nil {
		return err
	}
	defer a.Close()
	directories, bytes, err := readDirectories(a)
	if err != nil {
		return err
	}
	if opts.Cmd == 'l' {
		out := bufio.NewWriter(stdout)
		fmt.Fprintf(out, "--\nType = FreeArc\nPhysical Size = %d\n\n----------\n", a.Size)
		for _, dir := range directories {
			for _, f := range dir.Files {
				folder := "-"
				if f.IsDir {
					folder = "+"
				}
				fmt.Fprintf(out, "Path = %s\nSize = %d\nFolder = %s\nModified = %s\n\n",
					f.FullName(), f.Size, folder, time.Unix(int64(f.Time), 0).UTC().Format(time.DateTime))
			}
		}
		return out.Flush()
	}
	if len(args) == 4 {
		opts.Filenames, bytes, err = readSelection(args[3], directories)
		if err != nil {
			return err
		}
	}
	opts.UI = &progress{out: stdout, total: bytes}
	process := extract.New(opts)
	defer process.Close()
	for _, dir := range directories {
		for i := range dir.Blocks {
			if err := process.ExtractBlock(a, dir, i); err != nil {
				return err
			}
		}
	}
	_, err = fmt.Fprintln(stdout, "100%\nEverything is Ok")
	return err
}

func readSelection(listfile string, directories []*arc.Dir) (map[string]bool, uint64, error) {
	file, err := os.Open(listfile)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	requested := make(map[string]bool)
	for scanner.Scan() {
		name := strings.ReplaceAll(scanner.Text(), "\\", "/")
		if !safeName(name) {
			return nil, 0, fmt.Errorf("Invalid FreeArc member selection")
		}
		requested[name] = false
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	if len(requested) == 0 {
		return nil, 0, fmt.Errorf("Empty FreeArc member selection")
	}
	selected := make(map[string]bool, len(requested))
	var size uint64
	for _, dir := range directories {
		for _, f := range dir.Files {
			name := f.FullName()
			for candidate := name; candidate != ""; {
				if _, ok := requested[candidate]; ok {
					requested[candidate] = true
					selected[name] = true
				}
				separator := strings.LastIndexByte(candidate, '/')
				if separator < 0 {
					break
				}
				candidate = candidate[:separator]
			}
			if selected[name] {
				size += f.Size
			}
		}
	}
	for name, found := range requested {
		if !found {
			return nil, 0, fmt.Errorf("FreeArc member %q was not found", name)
		}
	}
	return selected, size, nil
}

func readDirectories(a *arc.Archive) ([]*arc.Dir, uint64, error) {
	var directories []*arc.Dir
	var bytes uint64
	var pathBytes int
	seen := make(map[string]bool)
	header := false
	for _, block := range a.Blocks {
		switch block.Type {
		case arc.HeaderBlock:
			content, err := a.ReadControlBlock(block)
			if err != nil {
				return nil, 0, err
			}
			if block.Pos != 0 || header || !strings.HasPrefix(string(content), "ArC\x01") {
				return nil, 0, fmt.Errorf("Invalid FreeArc header")
			}
			header = true
		case arc.DirBlock:
			dir, err := a.ReadDir(block)
			if err != nil {
				return nil, 0, err
			}
			for _, f := range dir.Files {
				name := f.FullName()
				key := strings.ToLower(name)
				if !safeName(name) || strings.ContainsAny(f.Name, "/\\") {
					return nil, 0, fmt.Errorf("Unsafe archive path %q", name)
				}
				if seen[key] {
					return nil, 0, fmt.Errorf("Duplicate archive path %q", name)
				}
				seen[key] = true
				pathBytes += len(name)
				if len(seen) > maxEntries || pathBytes > maxPathBytes || f.Size > maxOutput-bytes {
					return nil, 0, fmt.Errorf("FreeArc archive exceeds the output limit")
				}
				if f.IsDir && f.Size != 0 {
					return nil, 0, fmt.Errorf("FreeArc directory contains unexpected data")
				}
				bytes += f.Size
			}
			directories = append(directories, dir)
		}
	}
	if !header || len(directories) == 0 {
		return nil, 0, fmt.Errorf("FreeArc header or directory is missing")
	}
	return directories, bytes, nil
}

func safeName(name string) bool {
	if !utf8.ValidString(name) || extract.UnsafePath(name) || !filepath.IsLocal(filepath.FromSlash(name)) ||
		strings.ContainsAny(name, ":\\*?\"<>|") || strings.ContainsFunc(name, func(r rune) bool { return r < 32 }) {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
	}
	return true
}

type progress struct {
	out         io.Writer
	total, done uint64
	last        uint64
}

func (*progress) AskOverwrite(string, uint64, uint32) byte { return 'q' }

func (p *progress) ProgressFile(isDir bool, operation, _ string, size uint64) {
	if isDir || operation == "Skipping" || p.total == 0 {
		return
	}
	percent := min(99, p.done*100/p.total)
	if percent > p.last {
		fmt.Fprintf(p.out, "%d%%\n", percent)
		p.last = percent
	}
	p.done += size
}
