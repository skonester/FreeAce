package tornado

import "github.com/n0madic/go-unarc/internal/method"

func init() { method.Register(Parse) }

// defaultPreset is the preset a bare "tor" stands for (default_Tornado_method).
const defaultPreset = 5

// presetBuffer holds the buffer (dictionary) size of each preset std_Tornado_method[0..11]. The other
// fields of a preset (hash sizes, parser, coder) only matter to the encoder: the decoder reads the coder
// and the minimum match length from the stream.
var presetBuffer = [...]uint32{
	0:  1 * method.MB,
	1:  1 * method.MB,
	2:  2 * method.MB,
	3:  4 * method.MB,
	4:  8 * method.MB,
	5:  16 * method.MB,
	6:  64 * method.MB,
	7:  256 * method.MB,
	8:  1024 * method.MB,
	9:  1024 * method.MB,
	10: 1024 * method.MB,
	11: 1024 * method.MB,
}

// codec is a parsed tor method. Only the buffer size is kept: it is what GetDecompressionMem reports.
type codec struct {
	buffer uint32
	env    *method.Env
}

// Parse mirrors parse_TORNADO: parameters b (buffer), h (hash size), l (row width), c (coder), p (parser),
// u (update step), t (table search), ah/al (auxiliary hash size and row width), a bare number that selects a
// preset 0..11 (C indexes the preset table without a check; larger numbers are rejected here) and otherwise
// a buffer size.
func Parse(params []string, env *method.Env) (method.Codec, bool) {
	if params[0] != "tor" {
		return nil, false
	}
	buffer := presetBuffer[defaultPreset]
	for _, param := range params[1:] {
		if !parseParam(param, &buffer) {
			return nil, false
		}
	}
	return &codec{buffer: buffer, env: env}, true
}

// parseParam applies one parameter to the buffer size and reports whether it is valid.
func parseParam(param string, buffer *uint32) bool {
	if param != "" {
		rest := param[1:]
		switch param[0] {
		case 'b':
			n, ok := method.ParseMem(rest)
			*buffer = n
			return ok
		case 'h':
			_, ok := method.ParseMem(rest)
			return ok
		case 'l', 'c', 'p', 'u', 't':
			_, ok := method.ParseInt(rest)
			return ok
		case 'a':
			switch {
			case len(rest) > 0 && rest[0] == 'h':
				_, ok := method.ParseMem(rest[1:])
				return ok
			case len(rest) > 0 && rest[0] == 'l':
				_, ok := method.ParseInt(rest[1:])
				return ok
			}
		}
	}
	// Not a named parameter: a preset number if it is one, else a buffer size.
	if n, ok := method.ParseInt(param); ok {
		if n >= uint32(len(presetBuffer)) {
			return false
		}
		*buffer = presetBuffer[n]
		return true
	}
	n, ok := method.ParseMem(param)
	*buffer = n
	return ok
}

// DecompressionMem mirrors GetDecompressionMem: the buffer size of the method.
func (c *codec) DecompressionMem() uint64 { return uint64(c.buffer) }

// Dictionary mirrors TORNADO_METHOD::GetDictionary: the buffer size.
func (c *codec) Dictionary() uint64 { return uint64(c.buffer) }
