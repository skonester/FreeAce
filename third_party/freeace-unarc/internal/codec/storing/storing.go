// Package storing implements the "storing" method: data kept as is.
package storing

import (
	"io"

	"github.com/n0madic/go-unarc/internal/method"
)

// bufferSize is what the C copy loop reads at a time (BUFFER_SIZE), reported as the method's memory.
const bufferSize = 64 * method.KB

func init() { method.Register(Parse) }

type codec struct{}

func (codec) DecompressionMem() uint64 { return bufferSize }

func (codec) Decode(dst io.Writer, src io.Reader) error {
	buf := make([]byte, bufferSize)
	_, err := io.CopyBuffer(struct{ io.Writer }{dst}, struct{ io.Reader }{src}, buf)
	return err
}

// Parse accepts "storing" without parameters.
func Parse(params []string, _ *method.Env) (method.Codec, bool) {
	if params[0] != "storing" || len(params) > 1 {
		return nil, false
	}
	return codec{}, true
}
