// Package btoon implements BTOON ("Binary TOON"), a compact, canonical,
// MessagePack-compatible binary serialization format, as specified by
// BTOON spec 0.3 (https://github.com/BTOON-project/btoon-core, docs/SPEC.md).
//
// BTOON is MessagePack with string map keys, plus a columnar ("tabular")
// encoding for arrays of uniform records, extension types for timestamps,
// dates, datetimes, big integers and float vectors, an optional compression
// envelope and an optional HMAC-SHA256 signed envelope.
//
// The package is pure Go with no dependencies outside the standard library.
// zlib compression is built in; LZ4, Zstandard, Brotli and Snappy are
// available by importing github.com/BTOON-project/btoon-go/codecs, or by
// registering a Codec.
//
// # Encoding
//
// Marshal produces the canonical encoding (SPEC §4): integers in the
// smallest format for their value, floats as float64 with NaN canonicalized,
// map keys sorted by their UTF-8 bytes, the tabular encoding exactly when
// the size rule of SPEC §5.6.2 says so, and timestamps in the MessagePack
// form selected by SPEC §5.1. Equal values therefore encode to identical
// bytes in every conforming implementation. Structs are encoded through
// their `btoon` (or, failing that, `json`) field tags, types with an
// encoding.BinaryMarshaler method and structs with an encoding.TextMarshaler
// method through the method; see Marshal for the full mapping. MarshalWithOptions adds compression and
// signing.
//
// # Decoding
//
// Decode returns a dynamic value (nil, bool, int64, uint64, float64,
// string, []byte, []any, map[string]any, time.Time, *big.Int, []float32,
// VectorF64, Ext); Unmarshal stores the document in a typed Go value. Both
// are hardened against hostile input as SPEC §7 requires: every read is
// bounds-checked, nesting is limited (128 by default, at most 1000), nothing
// is allocated before checking that the remaining input could hold it, all
// that containers reserve before their elements arrive is bounded by the
// size of the document, strings and keys must be valid UTF-8, duplicate
// keys are rejected, and compression envelopes are checked against a size
// limit (64 MiB by default) before and while decompressing. Signed
// envelopes are verified, in constant time, before the payload is parsed.
//
// The memory a decoded document takes is bounded as SPEC §7.1 requires: a
// document decodes to at most DecodeOptions.MaxValues values (2^20 by
// default, counting every array element, map value, extension, tabular row
// and cell), and a tabular array's rows × column-name bytes can't exceed
// DecodeOptions.MaxDecompressedSize. Exceeding either fails with
// CodeSizeLimit. With the defaults, decoding takes at most about 210 MB.
//
// # Errors
//
// All errors are *Error values whose Code is one of the SPEC §7 codes
// (CodeTruncated, CodeDuplicateKey, ...), plus CodeInvalidArgument and
// CodeTypeMismatch for Go-specific problems. Use errors.As to inspect them
// or errors.Is with the Err sentinels.
//
// # Streams
//
// Encoder and Decoder write and read a stream of concatenated bare
// documents, such as a log file or a network connection. Each document of a
// stream is bounded like the payload of an envelope: at most
// DecodeOptions.MaxDecompressedSize bytes (64 MiB by default) and
// MaxValues values, or the Decoder fails with CodeSizeLimit.
package btoon
