// Package rep implements the rep method: a long-range LZ77 preprocessor with a large sliding window.
//
// The decoder keeps a ring buffer of BlockSize bytes. The stream (little-endian uint32 unless noted) is:
//
//	BlockSize  the window size, repeated from the method parameters
//	blocks:    ComprSize, then ComprSize bytes; a ComprSize of 0 ends the stream
//
// A block holds
//
//	num        number of matches
//	lens       num int32 match lengths
//	offsets    num int32 match offsets: how far back in the ring the match starts
//	datalens   num+1 int32 lengths of the literal runs
//	literals   the bytes of all literal runs, back to back
//
// and decodes to: literal run 0, match 0, literal run 1, match 1, ..., literal run num. Everything
// is written into the ring, wrapping at BlockSize, and a match may overlap the bytes it produces.
package rep
