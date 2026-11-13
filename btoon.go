package btoon

import (
	"reflect"
	"strconv"
)

const (
	// Version is the version of this library.
	Version = "0.2.0"
	// SpecVersion is the version of the BTOON specification it implements.
	SpecVersion = "0.2"
)

const (
	// DefaultMaxDepth is the default nesting limit (SPEC §7).
	DefaultMaxDepth = 128
	// MaxDepthLimit is the largest MaxDepth that EncodeOptions and
	// DecodeOptions accept (SPEC §7). A larger one fails with
	// CodeInvalidArgument, because deeper nesting could exhaust the stack.
	MaxDepthLimit = 1000
	// DefaultMaxDecompressedSize is the default limit for the uncompressed
	// size of a compression envelope (SPEC §6, §7.1): 64 MiB. It also bounds
	// the column-name bytes a tabular array copies into its rows.
	DefaultMaxDecompressedSize = 64 << 20
	// DefaultMaxValues is the default limit for the number of values a
	// document decodes to (SPEC §7.1): 2^20.
	DefaultMaxValues = 1 << 20
)

// EncodeOptions configure MarshalWithOptions. The zero value gives the
// defaults of the cross-language API (SPEC-conformant canonical output).
type EncodeOptions struct {
	// Compress wraps the document in a compression envelope (SPEC §6) using
	// the Compression algorithm. Documents smaller than 256 bytes, and
	// documents that the envelope would not shrink by at least 5%, are
	// written uncompressed.
	Compress bool
	// Compression is the algorithm used when Compress is set. The zero value
	// is Zlib. Other algorithms need a registered Codec; without one,
	// MarshalWithOptions fails with CodeUnsupportedCompression.
	Compression Compression
	// CompressionLevel is passed to the codec; 0 selects its default.
	CompressionLevel int
	// DisableTabular turns off the columnar encoding of arrays of uniform
	// maps (SPEC §5.6). It is the inverse of the cross-language
	// auto_tabular option, so that the zero value is the default.
	DisableTabular bool
	// MaxDepth limits container nesting. 0 means DefaultMaxDepth, and more
	// than MaxDepthLimit is an error. Deeper values and cyclic data
	// structures fail with CodeMaxDepth.
	MaxDepth int
	// HMACKey, if not nil, signs the output with HMAC-SHA256 (SPEC §8). It
	// must not be empty, and should be at least 32 random bytes.
	HMACKey []byte
	// PreserveTimeOffset writes a time.Time whose Location is not UTC with
	// its UTC offset, using the 14-byte timestamp form. By default times are
	// normalized to UTC, as SPEC §5.1 recommends.
	PreserveTimeOffset bool
}

// DecodeOptions configure DecodeWithOptions and UnmarshalWithOptions. The
// zero value gives the defaults: strict mode, depth 128, a 64 MiB envelope
// limit, at most 2^20 values, compression envelopes accepted.
type DecodeOptions struct {
	// NonStrict selects SPEC §7 non-strict mode: trailing bytes are
	// ignored, duplicate map keys are allowed (the last one wins) and string
	// values are not checked for valid UTF-8. Map keys and tabular column
	// names must be valid UTF-8 in both modes, and bounds, depth, size and
	// allocation checks still apply.
	NonStrict bool
	// MaxDepth limits container nesting. 0 means DefaultMaxDepth, and more
	// than MaxDepthLimit is an error.
	MaxDepth int
	// MaxDecompressedSize limits the uncompressed size of a compression
	// envelope, and the column-name bytes a tabular array copies into its
	// rows (rows × the sum of the column names' lengths, SPEC §7.1). 0 means
	// DefaultMaxDecompressedSize. Exceeding it fails with CodeSizeLimit.
	MaxDecompressedSize int
	// MaxValues limits how many values a document decodes to (SPEC §7.1).
	// Every value counts: the top-level value, array elements, map values,
	// extensions, tabular rows and cells, including those inside a
	// compression envelope. Map keys don't. 0 means DefaultMaxValues.
	// Exceeding it fails with CodeSizeLimit. Raise it to decode large
	// tables; keep the default, or lower it, for untrusted input.
	MaxValues int
	// DisableDecompression rejects compression envelopes with
	// CodeInvalidEnvelope (the cross-language auto_decompress = false).
	DisableDecompression bool
	// HMACKey, if not nil, requires a signed envelope (SPEC §8) and verifies
	// its MAC before parsing. It must not be empty.
	HMACKey []byte
	// PreserveTimeTypes decodes timestamps, dates and datetimes as
	// Timestamp, Date and DateTime instead of time.Time, so that they
	// re-encode exactly (including the original extension type).
	PreserveTimeTypes bool
	// DisallowUnknownFields makes Unmarshal fail with CodeTypeMismatch when
	// a map key has no matching field in the target struct.
	DisallowUnknownFields bool
}

// Marshal returns the canonical BTOON encoding of v (SPEC §4).
//
// Go values map to BTOON as follows:
//
//   - nil, nil pointers, nil interfaces, nil slices and nil maps: nil
//   - bool: boolean
//   - signed and unsigned integers: the smallest integer format for the
//     value, regardless of the Go type
//   - *big.Int and big.Int: an integer if it fits in [-2^63, 2^64-1],
//     otherwise a bigint extension
//   - float32 and float64: float64, with NaN canonicalized
//   - string: string (it must be valid UTF-8)
//   - []byte and [N]byte: binary
//   - []float32 and [N]float32: float32 vector; VectorF64: float64 vector
//   - other slices and arrays: array, or tabular when SPEC §5.6.2 says so
//   - maps with string (or string-kind, or interface holding string) keys:
//     map with keys in canonical order
//   - structs: map of their exported fields (see below)
//   - time.Time: timestamp in UTC (see EncodeOptions.PreserveTimeOffset)
//   - Timestamp, Date, DateTime, Ext: the corresponding extension
//   - types implementing encoding.BinaryMarshaler: binary
//
// Struct fields are encoded under their name, or the name given in a
// `btoon:"name,omitempty"` tag. Without a btoon tag, the json tag is used.
// A tag of "-" omits the field; the options omitempty and omitzero work as
// in encoding/json, and embedded structs are flattened by the
// encoding/json rules. Channels, functions and complex numbers are not
// supported.
func Marshal(v any) ([]byte, error) {
	return MarshalWithOptions(v, EncodeOptions{})
}

// MarshalWithOptions is like Marshal but applies o, adding a compression
// and/or signed envelope when requested (layered as
// sign(compress(encode(v))), SPEC §8).
func MarshalWithOptions(v any, o EncodeOptions) ([]byte, error) {
	// An empty key is a configuration error, not "no key" (SPEC §8). Like
	// the other options, it fails before v is looked at.
	if o.HMACKey != nil && len(o.HMACKey) == 0 {
		return nil, newError(CodeInvalidArgument, "HMACKey is empty")
	}
	out, err := encodeBare(v, &o)
	if err != nil {
		return nil, err
	}
	if o.Compress {
		if out, err = compressDocument(out, o.Compression, o.CompressionLevel); err != nil {
			return nil, err
		}
	}
	if o.HMACKey != nil {
		out = signDocument(out, o.HMACKey)
	}
	return out, nil
}

func encodeBare(v any, o *EncodeOptions) ([]byte, error) {
	maxDepth, err := resolveMaxDepth(o.MaxDepth)
	if err != nil {
		return nil, err
	}
	e := encoder{
		buf:        make([]byte, 0, 128),
		maxDepth:   maxDepth,
		noTabular:  o.DisableTabular,
		keepOffset: o.PreserveTimeOffset,
	}
	if err := e.encode(reflect.ValueOf(v), 0); err != nil {
		return nil, err
	}
	return e.buf, nil
}

// Decode decodes a BTOON document, which may be wrapped in a compression
// envelope, into a dynamic Go value:
//
//	nil                    nil
//	boolean                bool
//	integer                int64, or uint64 if above math.MaxInt64
//	bigint extension       *big.Int
//	float                  float64
//	string                 string
//	binary                 []byte
//	array, tabular         []any
//	map                    map[string]any
//	timestamp              time.Time (UTC, or a fixed zone if it has an offset);
//	                       Timestamp if outside time.Time's range
//	date, datetime         time.Time (UTC)
//	float32 vector         []float32
//	float64 vector         VectorF64
//	other extensions       Ext
//
// Decode is strict (SPEC §7): it rejects trailing bytes, invalid UTF-8,
// duplicate keys and malformed extensions, and never allocates much more
// than the input justifies. A document that decodes to more than
// DefaultMaxValues values, or whose envelope declares more than
// DefaultMaxDecompressedSize bytes, fails with CodeSizeLimit (SPEC §7.1).
// A signed document is rejected with CodeAuthenticationFailed; use
// DecodeWithOptions with an HMACKey.
func Decode(data []byte) (any, error) {
	return DecodeWithOptions(data, DecodeOptions{})
}

// DecodeWithOptions is like Decode but applies o.
func DecodeWithOptions(data []byte, o DecodeOptions) (any, error) {
	cfg, err := o.config()
	if err != nil {
		return nil, err
	}
	doc, err := openDocument(data, &cfg)
	if err != nil {
		return nil, err
	}
	return cfg.decodeBare(doc, true)
}

// Unmarshal decodes a BTOON document into the value pointed to by v, which
// must be a non-nil pointer. It accepts the same documents as Decode.
//
// Unmarshal follows the conventions of encoding/json: it allocates pointers
// and maps as needed, matches map keys to struct fields by their BTOON name
// (exactly; matching is case-sensitive), ignores unknown keys (see
// DecodeOptions.DisallowUnknownFields), leaves values unchanged for a BTOON
// nil unless they are pointers, interfaces, maps or slices, and stores the
// dynamic values of Decode in interface values.
//
// Integers are stored in integer fields of any size, failing with
// CodeTypeMismatch on overflow; integers and floats are stored in float
// fields; binary is stored in []byte, [N]byte (of equal length) and string;
// strings in string and []byte; timestamps, dates and datetimes in
// time.Time, Timestamp, Date and DateTime when the conversion is exact.
// Types implementing encoding.BinaryUnmarshaler receive binary values.
func Unmarshal(data []byte, v any) error {
	return UnmarshalWithOptions(data, v, DecodeOptions{})
}

// UnmarshalWithOptions is like Unmarshal but applies o.
func UnmarshalWithOptions(data []byte, v any, o DecodeOptions) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return newError(CodeInvalidArgument, "Unmarshal needs a non-nil pointer, not "+describeTarget(v))
	}
	src, err := DecodeWithOptions(data, o)
	if err != nil {
		return err
	}
	a := assigner{disallowUnknown: o.DisallowUnknownFields}
	return a.assign(rv.Elem(), src)
}

func describeTarget(v any) string {
	if v == nil {
		return "nil"
	}
	return reflect.TypeOf(v).String()
}

// decodeConfig is DecodeOptions with defaults applied.
type decodeConfig struct {
	strict          bool
	maxDepth        int
	maxDecompressed int
	maxValues       int
	noDecompress    bool
	hmacKey         []byte
	timeTypes       bool
}

// config checks o and applies the defaults. Options that can't be honored
// fail before any input is read.
func (o *DecodeOptions) config() (decodeConfig, error) {
	maxDepth, err := resolveMaxDepth(o.MaxDepth)
	if err != nil {
		return decodeConfig{}, err
	}
	maxSize, err := resolveLimit("MaxDecompressedSize", o.MaxDecompressedSize, DefaultMaxDecompressedSize)
	if err != nil {
		return decodeConfig{}, err
	}
	maxValues, err := resolveLimit("MaxValues", o.MaxValues, DefaultMaxValues)
	if err != nil {
		return decodeConfig{}, err
	}
	if o.HMACKey != nil && len(o.HMACKey) == 0 {
		return decodeConfig{}, newError(CodeInvalidArgument, "HMACKey is empty")
	}
	return decodeConfig{
		strict:          !o.NonStrict,
		maxDepth:        maxDepth,
		maxDecompressed: maxSize,
		maxValues:       maxValues,
		noDecompress:    o.DisableDecompression,
		hmacKey:         o.HMACKey,
		timeTypes:       o.PreserveTimeTypes,
	}, nil
}

// decodeBare decodes a bare document. If whole is false, trailing bytes are
// never an error. Each call has its own max_values budget, so every
// document of a stream has one.
func (c *decodeConfig) decodeBare(doc []byte, whole bool) (any, error) {
	d := newDecoder(doc, c)
	if whole {
		return d.document()
	}
	return d.value(0)
}

func resolveMaxDepth(n int) (int, error) {
	switch {
	case n == 0:
		return DefaultMaxDepth, nil
	case n < 0:
		return 0, newError(CodeInvalidArgument, "MaxDepth is negative")
	case n > MaxDepthLimit:
		return 0, newError(CodeInvalidArgument, "MaxDepth "+strconv.Itoa(n)+" is above "+strconv.Itoa(MaxDepthLimit))
	}
	return n, nil
}

// resolveLimit applies the default of a size limit whose zero value means
// the default.
func resolveLimit(name string, n, def int) (int, error) {
	switch {
	case n < 0:
		return 0, newError(CodeInvalidArgument, name+" is negative")
	case n == 0:
		return def, nil
	}
	return n, nil
}
