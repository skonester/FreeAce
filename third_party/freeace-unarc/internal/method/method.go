package method

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// Errors a decoder may return. Callers match them with errors.Is.
var (
	// ErrUnsupportedMethod: no registered parser recognises the method string.
	ErrUnsupportedMethod = errors.New("unsupported compression method")
	// ErrBadData: the compressed stream is invalid. A codec does not put its own name in these errors: the
	// chain prefixes the method that failed (see Named).
	ErrBadData = errors.New("compressed data corrupted")
	// ErrPasswordRequired: an encryption stage carries no key and no password was given.
	ErrPasswordRequired = errors.New("the archive is encrypted: give its password with -p{password} or UNARC_PASSWORD")
	// ErrWrongPassword: the password does not match the stored check code.
	ErrWrongPassword = errors.New("wrong password")
	// ErrStop is returned by a final writer that needs no more data; a chain ending with it succeeded.
	ErrStop = errors.New("no more data required")
	// ErrOutputTooLarge: a control block decompressed to more bytes than its descriptor says.
	ErrOutputTooLarge = errors.New("output block too small")
)

// Named prefixes a bad data or limit error with the name of the method that reported it; other errors (a write
// error of the destination, ErrStop, a missing password) pass unchanged.
func Named(name string, err error) error {
	if !errors.Is(err, ErrBadData) && !errors.Is(err, ErrLimit) {
		return err
	}
	return fmt.Errorf("%s: %w", name, err)
}

// Decoder decodes whole streams.
type Decoder interface {
	// Decode reads the whole compressed stream from src and writes the decoded data to dst.
	// A dst.Write error must be returned as is (it may be ErrStop).
	Decode(dst io.Writer, src io.Reader) error
}

// Codec decodes one stage of a compressor chain. Decode calls may run concurrently on one Codec (4x4 decodes
// its blocks in parallel), so the decoding state lives in the call, not in the Codec. A codec whose state is
// worth keeping between streams (a dictionary, a model) may also have a method
//
//	NewDecoder() Decoder
//
// returning a Decoder for one goroutine that reuses its memory from one stream to the next and decodes each
// exactly as a fresh Decode does; 4x4 gives one to each worker.
type Codec interface {
	// DecompressionMem is the memory the stage needs, used to decide where to buffer through temp files.
	DecompressionMem() uint64
	Decoder
}

// Parser builds a Codec from the method's name and parameters (params[0] is the name).
// It reports false when the method is not its own or its parameters are invalid.
type Parser func(params []string, env *Env) (Codec, bool)

// Env carries the run-time configuration the C code kept in globals and environment variables.
type Env struct {
	Ctx        context.Context
	TempDir    string // -w, else $TMPDIR
	Password   string // -p / UNARC_PASSWORD; empty means none
	CLSDir     string // -cls / UNARC_CLS_DIR
	CLSDirSet  bool   // UNARC_CLS_DIR given at all (even empty), which silences the -cls hint
	Wine       string // UNARC_WINE, default "wine"
	CLSHost    string // UNARC_CLSHOST
	Srep       string // UNARC_SREP: external decoder in place of the built-in one
	CLSKeep    bool   // UNARC_CLS_KEEP: keep a plugin stage's input
	Debug      bool   // UNARC_DEBUG
	ShowBlocks bool   // UNARC_BLOCKS: print each solid block's compressor, position and size
	ExeDir     string // folder of the unarc executable (arc.ini, srep, clshost.exe, external programs)
	Stdout     io.Writer
	Stderr     io.Writer
	External   []Parser // compressors declared in arc.ini, tried before the built-in ones
	Limits     Limits
	tempUsed   atomic.Uint64
}

// NewEnv returns an Env filled from the process environment.
func NewEnv() *Env {
	cls, clsSet := os.LookupEnv("UNARC_CLS_DIR")
	_, keep := os.LookupEnv("UNARC_CLS_KEEP")
	_, debug := os.LookupEnv("UNARC_DEBUG")
	_, blocks := os.LookupEnv("UNARC_BLOCKS")
	env := &Env{
		Ctx:        context.Background(),
		Password:   os.Getenv("UNARC_PASSWORD"),
		CLSDir:     cls,
		CLSDirSet:  clsSet,
		Wine:       os.Getenv("UNARC_WINE"),
		CLSHost:    os.Getenv("UNARC_CLSHOST"),
		Srep:       os.Getenv("UNARC_SREP"),
		CLSKeep:    keep,
		Debug:      debug,
		ShowBlocks: blocks,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Limits:     limitsFromEnv(),
	}
	if env.Wine == "" {
		env.Wine = "wine"
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		env.ExeDir = filepath.Dir(exe)
	}
	return env
}

// Temp returns the folder for temporary files: -w if given, else $TMPDIR or the system default.
func (e *Env) Temp() string {
	if e.TempDir != "" {
		return e.TempDir
	}
	return os.TempDir()
}

var (
	registryMu sync.RWMutex
	registry   []Parser
	fallback   []Parser
)

// Register adds a built-in method parser. Parsers are tried in registration order.
func Register(p Parser) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = append(registry, p)
}

// RegisterFallback adds a parser tried after every built-in one, whatever the registration order: C links
// its plugin parser (srep, CLS-*.dll) last, so a plugin never shadows a built-in method of the same name.
func RegisterFallback(p Parser) {
	registryMu.Lock()
	defer registryMu.Unlock()
	fallback = append(fallback, p)
}

// Lookup builds the codec for one method string the way ParseCompressionMethod searches: the external
// compressors first ("tempfile", then arc.ini), then the built-in ones, then the fallback (plugin) ones.
func Lookup(m string, env *Env) (Codec, bool) {
	params := Params(m)
	if c, ok := ParseTempfile(params, env); ok {
		return c, true
	}
	for _, p := range env.External {
		if c, ok := p(params, env); ok {
			return c, true
		}
	}
	// The parsers run without the lock: a parser may call Lookup for an inner method (4x4), and a recursive
	// RLock deadlocks once a Register is waiting.
	registryMu.RLock()
	lists := [][]Parser{registry, fallback}
	registryMu.RUnlock()
	for _, list := range lists {
		for _, p := range list {
			if c, ok := p(params, env); ok {
				return c, true
			}
		}
	}
	return nil, false
}
