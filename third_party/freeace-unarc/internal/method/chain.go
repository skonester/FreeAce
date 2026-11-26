package method

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/n0madic/go-unarc/internal/cleanup"
)

// Run decodes src, compressed with the compressor chain (e.g. "exe+lzma+aes"), and writes the result to dst.
// The stages run concurrently, the last method first, connected by pipes; a "tempfile" stage splits the chain:
// everything before it is decoded into a temp file before the rest starts.
// A dst.Write returning ErrStop ends decoding early and counts as success.
func Run(compressor string, dst io.Writer, src io.Reader, env *Env) error {
	return run(compressor, dst, src, env, true, false)
}

// RunFull verifies that every stage finishes without dropping unread input.
func RunFull(compressor string, dst io.Writer, src io.Reader, env *Env) error {
	return run(compressor, dst, src, env, true, true)
}

func run(compressor string, dst io.Writer, src io.Reader, env *Env, announce, full bool) error {
	if env.Debug && announce {
		fmt.Fprintf(env.Stderr, "MultiDecompress %s\n", PublicCompressor(compressor))
	}
	methods := Split(compressor)
	codecs := make([]Codec, len(methods))
	for i, m := range methods {
		c, ok := Lookup(m, env)
		if !ok {
			if announce {
				hint := ""
				if !env.CLSDirSet {
					hint = " (if the setup ships CLS-*.dll for it, pass their folder with -cls)"
				}
				fmt.Fprintf(env.Stderr, "\nERROR: compression method \"%s\" is not supported%s\n", Name(m), hint)
			}
			return fmt.Errorf("%w: %s", ErrUnsupportedMethod, PublicCompressor(m))
		}
		codecs[i] = c
	}
	// Decoding runs the chain backwards: the last method reads the archive, the first one writes the files.
	stages := make([]stage, len(codecs))
	for i, c := range codecs {
		stages[len(codecs)-1-i] = stage{codec: c, name: Name(methods[i])}
	}
	err := runStages(stages, dst, src, env, full)
	if env.Debug && errors.Is(err, ErrBadData) {
		fmt.Fprintf(env.Stderr, "Decompress error: %v\n", err)
	}
	return err
}

// stage is one method of a chain: its codec and the name that the chain puts on the codec's errors.
type stage struct {
	codec Codec
	name  string
}

func runStages(stages []stage, dst io.Writer, src io.Reader, env *Env, full bool) error {
	in := src
	var segment []stage
	for _, s := range stages {
		if _, ok := s.codec.(tempfileCodec); !ok {
			segment = append(segment, s)
			continue
		}
		f, err := spill(segment, in, env, full)
		if err != nil {
			return err
		}
		defer f.close()
		in = f.File
		segment = nil
	}
	if len(segment) == 0 {
		_, err := io.Copy(dst, in)
		if !full && errors.Is(err, ErrStop) {
			err = nil
		}
		return err
	}
	return pipeline(segment, dst, in, full)
}

// tempFile is a temp file that removes itself on close.
type tempFile struct {
	*os.File
	forget func()
	space  *TempSpace
}

func (t tempFile) close() {
	t.File.Close()
	os.Remove(t.Name())
	t.forget()
	t.space.Release()
}

// spill decodes in through stages into a new temp file and returns it rewound.
func spill(stages []stage, in io.Reader, env *Env, full bool) (tempFile, error) {
	f, err := os.CreateTemp(env.Temp(), "unarc-tempfile-*")
	if err != nil {
		return tempFile{}, fmt.Errorf("temp file: %w", err)
	}
	t := tempFile{File: f, forget: cleanup.Register(f.Name()), space: env.TempSpace()}
	dst := t.space.Writer(f)
	if len(stages) == 0 {
		_, err = io.Copy(dst, in)
	} else {
		err = pipeline(stages, dst, in, full)
	}
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		t.close()
		return tempFile{}, err
	}
	return t, nil
}

// firstError keeps the first real error reported by the stages of a pipeline (ErrStop is not one).
type firstError struct {
	mu  sync.Mutex
	err error
}

func (f *firstError) set(err error) {
	if err == nil || errors.Is(err, ErrStop) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err == nil {
		f.err = err
	}
}

func (f *firstError) get() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

// pipeline runs stages concurrently: stages[0] reads src, each stage feeds the next through a pipe,
// the last one writes dst. A stage that fails or finishes early closes its input so the stage before it
// stops on its next write, and closes its output so the stage after it sees the error or EOF.
func pipeline(stages []stage, dst io.Writer, src io.Reader, full bool) error {
	n := len(stages)
	if n == 1 {
		err := decode(stages[0], dst, src, full)
		if !full && errors.Is(err, ErrStop) {
			err = nil
		}
		return err
	}
	readers := make([]io.Reader, n)
	writers := make([]io.Writer, n)
	pipeR := make([]*io.PipeReader, n)
	pipeW := make([]*io.PipeWriter, n)
	readers[0] = src
	for i := 0; i < n-1; i++ {
		r, w := io.Pipe()
		pipeW[i], pipeR[i+1] = w, r
		writers[i], readers[i+1] = w, r
	}
	writers[n-1] = dst

	var first firstError
	var wg sync.WaitGroup
	for i, st := range stages {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := decode(st, writers[i], readers[i], full)
			first.set(err) // before closing the pipes, so the root cause is recorded first
			if w := pipeW[i]; w != nil {
				if err != nil {
					w.CloseWithError(err)
				} else {
					w.Close()
				}
			}
			if r := pipeR[i]; r != nil {
				r.CloseWithError(ErrStop)
			}
		}()
	}
	wg.Wait()
	return first.get()
}

// decode runs one stage, turns a panic on malformed input into ErrBadData and names the stage in that error.
// A stage that only passes on the error of the stage before it (read from their pipe) names it again, but by
// then the pipeline has recorded the first one.
func decode(s stage, dst io.Writer, src io.Reader, full bool) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: decoder panic: %v", ErrBadData, r)
		}
		err = Named(s.name, err)
	}()
	if err := s.codec.Decode(dst, src); err != nil || !full {
		if full && errors.Is(err, ErrStop) {
			return fmt.Errorf("%w: decoder stopped before its end", ErrBadData)
		}
		return err
	}
	// Do not cancel the previous stage at an end marker: wait for EOF or its final check. A byte
	// left over from the previous stage is itself a mismatch between the two stages.
	var extra [1]byte
	if n, err := io.ReadFull(src, extra[:]); n != 0 {
		return fmt.Errorf("%w: bytes left after the decoder finished", ErrBadData)
	} else if err != io.EOF {
		return err
	}
	return nil
}

// limitBuffer collects at most max bytes.
type limitBuffer struct {
	buf bytes.Buffer
	max uint64
}

func (b *limitBuffer) Write(p []byte) (int, error) {
	if uint64(b.buf.Len())+uint64(len(p)) > b.max {
		return 0, ErrOutputTooLarge
	}
	return b.buf.Write(p)
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// DecompressMem decodes a control block held in memory into at most outSize bytes, as C DecompressMem does:
// a single method runs directly, a chain goes through Run (which reports unsupported stages on stderr).
func DecompressMem(compressor string, in []byte, outSize uint64, env *Env) ([]byte, error) {
	if limit := env.ControlLimit(); uint64(len(in)) > limit || outSize > limit {
		return nil, fmt.Errorf("%w: control block exceeds %d bytes", ErrLimit, limit)
	}
	out := &limitBuffer{max: outSize}
	src := &countingReader{r: bytes.NewReader(in)}
	announce := bytes.IndexByte([]byte(compressor), '+') >= 0
	if announce {
		compressor = AddTempfile(compressor, min(MaxMem(), env.DecoderLimit()), env)
	}
	err := run(compressor, out, src, env, announce, false)
	if env.Debug {
		fmt.Fprintf(env.Stderr, "DecompressMem result=%d writeLeft=%d outputSize=%d readLeft=%d inputSize=%d\n",
			CCode(err), outSize-uint64(out.buf.Len()), outSize, int64(len(in))-src.n, len(in))
	}
	if err != nil {
		return nil, err
	}
	return out.buf.Bytes(), nil
}

// CCode maps an error to the FreeArc C error code, for the debug lines that print it.
func CCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrUnsupportedMethod):
		return -2
	case errors.Is(err, ErrOutputTooLarge):
		return -4
	case errors.Is(err, ErrBadData):
		return -7
	case errors.Is(err, ErrStop):
		return -9
	}
	return -1
}
