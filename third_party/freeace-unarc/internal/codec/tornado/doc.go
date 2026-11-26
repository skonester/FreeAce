// Package tornado implements the decompressor of the tor method, FreeArc's Tornado LZ77 compressor.
//
// A stream starts with a 6-byte header: the entropy coder (1 byte, 2 bit, 3 Huffman, 4 arithmetic), the
// shortest match length, and the size of the sliding window as a little-endian uint32. What follows is a
// sequence of elements, each a literal or a match (length, distance), coded by the chosen coder, and ends with
// a marker element. Besides, the encoder replaces data tables (rows of 1..256 bytes that differ little from
// the previous one) with their row differences, announcing each with a special element; the decoder keeps them
// diffed in the window, so that matches see what the encoder saw, and undiffs them while writing the output.
//
// The decoder reproduces the output of the C decoder for every valid stream. Streams that make the C decoder
// exit, read outside its buffers or produce garbage are reported as method.ErrBadData; so are truncated
// streams. Memory use is bounded by the window size of the header, and the window is only allocated as far as
// the output reaches.
//
// The method is registered under the name tor; "tor:N" selects preset N (0..11), and the parameters b, h, l,
// c, p, u, t, ah and al are accepted as in the C parser. The decompression memory is the window size.
package tornado
