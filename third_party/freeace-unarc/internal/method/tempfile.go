package method

import (
	"io"
	"math"
	"strings"
)

// TempfileName is FreeArc's buffering pseudo-method: it stores everything it reads in a temp file
// and passes it on once the stage before it has finished.
const TempfileName = "tempfile"

// tempfileCodec marks a chain stage that runStages turns into a temp file; its Decode is a plain copy
// for callers that run it on its own.
type tempfileCodec struct{}

func (tempfileCodec) DecompressionMem() uint64 { return 0 }

func (tempfileCodec) Decode(dst io.Writer, src io.Reader) error {
	_, err := io.Copy(dst, src)
	return err
}

// ParseTempfile recognises "tempfile" (with any parameters, as the C external-compressor template does).
func ParseTempfile(params []string, _ *Env) (Codec, bool) {
	if params[0] != TempfileName {
		return nil, false
	}
	return tempfileCodec{}, true
}

// unknownMem is what C GetDecompressionMem returns for an unparsable method: (MemSize)-2.
const unknownMem = math.MaxUint32 - 1

// decompressionMem mirrors C GetDecompressionMem: a 32-bit MemSize.
func decompressionMem(m string, env *Env) uint64 {
	c, ok := Lookup(m, env)
	if !ok {
		return unknownMem
	}
	return uint64(uint32(c.DecompressionMem()))
}

// MaxMem is the memory a chain may use before AddTempfile splits it: min(3/4 of RAM, 4 GiB)
// computed the way C does it, on a RAM size capped at 4 GiB - 1.
func MaxMem() uint64 {
	return uint64(min(PhysicalMemory(), math.MaxUint32)) / 4 * 3
}

// AddTempfile inserts "tempfile" between the methods of compressor when their combined memory needs exceed
// maxmem, as C AddTempfile does; it returns compressor unchanged when they fit.
func AddTempfile(compressor string, maxmem uint64, env *Env) string {
	cm := Split(compressor)
	memi := make([]uint64, len(cm))
	var mem uint64
	for i, m := range cm {
		memi[i] = decompressionMem(m, env)
		mem += memi[i]
	}
	if mem <= maxmem {
		return compressor
	}
	var b strings.Builder
	b.WriteString(cm[0])
	mem = memi[0]
	for i := 1; i < len(cm); i++ {
		if mem > 0 && mem+memi[i] > maxmem {
			b.WriteString("+" + TempfileName)
			mem = 0
		}
		b.WriteString("+" + cm[i])
		mem += memi[i]
	}
	return b.String()
}
