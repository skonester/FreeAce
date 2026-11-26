// Package method parses FreeArc compression method strings ("lzma:64mb:normal+aes-256/ctr:...")
// and runs chains of decoders. Codec implementations live in internal/codec and register here.
package method

import "strings"

// Limits of FreeArc's C method parser (Compression.h); longer input is truncated the way C does it.
const (
	maxMethodStrlen        = 2048 // MAX_METHOD_STRLEN
	maxParameters          = 200  // MAX_PARAMETERS
	maxMethodsInCompressor = 100  // MAX_METHODS_IN_COMPRESSOR
)

// Memory size units of FreeArc ("kb", "mb", "gb" suffixes).
const (
	KB = 1 << 10
	MB = 1 << 20
	GB = 1 << 30
)

// split mirrors C split(): at most max-1 pieces, the last one keeps the rest of s unsplit.
func split(s string, sep byte, max int) []string {
	parts := make([]string, 0, 4)
	for len(parts) < max-2 {
		i := strings.IndexByte(s, sep)
		if i < 0 {
			break
		}
		parts = append(parts, s[:i])
		s = s[i+1:]
	}
	return append(parts, s)
}

// strncopy mirrors C strncopy(to, from, len): keeps at most n-1 bytes.
func strncopy(s string, n int) string {
	if len(s) > n-1 {
		return s[:n-1]
	}
	return s
}

// Split breaks a compressor ("a+b+c") into its methods, in the order they were applied when compressing.
func Split(compressor string) []string {
	return split(compressor, '+', maxMethodsInCompressor)
}

// Params breaks one method ("lzma:64mb:normal") into its name and parameters, as ParseCompressionMethod does.
func Params(method string) []string {
	return split(strncopy(method, maxMethodStrlen), ':', maxParameters)
}

// Name returns the method name: the part before the first ':'.
func Name(method string) string {
	name, _, _ := strings.Cut(strncopy(method, maxMethodStrlen), ":")
	return name
}

// PublicCompressor is the compressor string for diagnostics: an explicit cipher key is never printed.
func PublicCompressor(compressor string) string {
	if !strings.Contains(compressor, ":k") {
		return compressor
	}
	stages := strings.Split(compressor, "+")
	for i, stage := range stages {
		params := strings.Split(stage, ":")
		for j, param := range params {
			// Encryption can also be the inner method of 4x4.
			if j != 0 && params[0] != "4x4" {
				break
			}
			name, _, _ := strings.Cut(param, "/")
			name, _, _ = strings.Cut(name, "-")
			switch name {
			case "aes", "blowfish", "serpent", "twofish":
				for k := j + 1; k < len(params); k++ {
					if strings.HasPrefix(params[k], "k") {
						params[k] = "k[redacted]"
					}
				}
				stages[i] = strings.Join(params, ":")
			}
		}
	}
	return strings.Join(stages, "+")
}

// ParseInt mirrors C parseInt(): optional leading '=', decimal digits only, uint32 wrap-around.
func ParseInt(s string) (uint32, bool) {
	s = strings.TrimPrefix(s, "=")
	if s == "" {
		return 0, false
	}
	var n uint32
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return n, false
		}
		n = n*10 + uint32(c-'0')
	}
	return n, true
}

// ParseMem mirrors C parseMem(): digits followed by b/k/m/g (rest of the string ignored, so "64mb" is 64 MB),
// or by '^'/nothing meaning a power of two ("24" is 16 MB). uint32 arithmetic as in C.
func ParseMem(s string) (uint32, bool) {
	s = strings.TrimPrefix(s, "=")
	ok := s != ""
	var n uint32
	i := 0
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		n = n*10 + uint32(s[i]-'0')
	}
	var c byte
	if i < len(s) {
		c = s[i]
	}
	switch c {
	case 'b':
		return n, ok
	case 'k':
		return n * KB, ok
	case 'm':
		return n * MB, ok
	case 'g':
		return n * GB, ok
	case '^', 0:
		return uint32(1) << (n & 31), ok
	}
	return 0, false
}
