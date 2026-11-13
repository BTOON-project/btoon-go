# BTOON for Go

[![Go Reference](https://pkg.go.dev/badge/github.com/BTOON-project/btoon-go.svg)](https://pkg.go.dev/github.com/BTOON-project/btoon-go)
[![CI](https://github.com/BTOON-project/btoon-go/actions/workflows/ci.yml/badge.svg)](https://github.com/BTOON-project/btoon-go/actions/workflows/ci.yml)

A pure Go implementation of [BTOON](https://github.com/BTOON-project/btoon-core)
("Binary TOON"), a compact binary serialization format that is MessagePack plus:

- a **columnar (tabular) encoding** for arrays of uniform records, where each key is
  written once instead of once per row;
- **extension types** for timestamps, dates, datetimes, big integers, and float32 and
  float64 vectors;
- an optional **compression envelope** (zlib built in; LZ4, Zstandard, Brotli and
  Snappy optional);
- an optional **HMAC-SHA256 signed envelope**.

Encoding is **canonical**: equal values produce identical bytes in every conforming
implementation. Decoding is **hardened** against hostile input.

|                     |                                                                           |
|---------------------|---------------------------------------------------------------------------|
| Library version     | `0.1.2` (`btoon.Version`)                                                 |
| Specification       | BTOON **0.1** (`btoon.SpecVersion`), [SPEC.md](https://github.com/BTOON-project/btoon-core/blob/main/docs/SPEC.md) |
| Go                  | 1.25 or later                                                             |
| Dependencies        | none in package `btoon` (standard library only); see [Compression](#compression) for the optional `codecs` package |
| Conformance         | all 260 vectors of btoon-core's `btoon-vectors.json` (vendored in `testdata/`) pass; the zstd, lz4 and brotli vectors need the `codecs` package |

## Install

```sh
go get github.com/BTOON-project/btoon-go
```

## Usage

```go
import btoon "github.com/BTOON-project/btoon-go"

type User struct {
    ID      int       `btoon:"id"`
    Name    string    `btoon:"name"`
    Email   string    `btoon:"email,omitempty"`
    Created time.Time `btoon:"created"`
}

b, err := btoon.Marshal(User{ID: 1, Name: "Ada", Created: time.Now()})

var u User
err = btoon.Unmarshal(b, &u)

// Without a target type, Decode returns dynamic values
// (map[string]any, []any, int64, string, time.Time, ...).
v, err := btoon.Decode(b)
```

Arrays of records with the same keys are written column by column when that is
smaller, which the format decides by a fixed size rule (SPEC §5.6.2):

```go
users := make([]User, 1000)
b, _ := btoon.Marshal(users) // tabular: each key appears once
```

Compression and signing are options; the layering is always
`sign(compress(encode(v)))`:

```go
key := []byte("...at least 32 random bytes...")
b, err := btoon.MarshalWithOptions(v, btoon.EncodeOptions{Compress: true, HMACKey: key})

v, err := btoon.DecodeWithOptions(b, btoon.DecodeOptions{HMACKey: key})
```

`NewEncoder` and `NewDecoder` write and read streams of concatenated documents:

```go
enc := btoon.NewEncoder(conn)
enc.Encode(msg)

dec := btoon.NewDecoder(conn)
for {
    var msg Message
    if err := dec.Decode(&msg); err == io.EOF {
        break
    } else if err != nil {
        return err
    }
}
```

Stream documents are bare: compression and signed envelopes can't be used in a stream,
because their magic bytes are also the start of a valid document. The stream decoder
buffers one document at a time, so bound untrusted input (for example with
`io.LimitReader`).

See [`examples/basic`](examples/basic/main.go) and the
[package documentation](https://pkg.go.dev/github.com/BTOON-project/btoon-go) for more.

## API

| | |
|---|---|
| `Marshal(v any) ([]byte, error)` | canonical encoding of `v` |
| `MarshalWithOptions(v any, o EncodeOptions) ([]byte, error)` | with compression, signing and other options |
| `Unmarshal(data []byte, v any) error` | decode into the value `v` points to |
| `UnmarshalWithOptions(data []byte, v any, o DecodeOptions) error` | |
| `Decode(data []byte) (any, error)` | decode into dynamic values |
| `DecodeWithOptions(data []byte, o DecodeOptions) (any, error)` | |
| `NewEncoder(w io.Writer) *Encoder`, `NewDecoder(r io.Reader) *Decoder` | document streams |
| `RegisterCodec(c Compression, codec Codec)` | add or replace a compression algorithm |
| `ReadDecompressed(r io.Reader, size int) ([]byte, error)` | size-checked helper for `Codec` implementations |
| `*Error` with `Code string` | every error; see [Errors](#errors) |

## Type mapping

Decoding without a target type (`Decode`, or `Unmarshal` into an `interface{}`):

| BTOON | Go |
|---|---|
| nil | `nil` |
| boolean | `bool` |
| integer | `int64`; `uint64` only above `math.MaxInt64` |
| bigint (ext -4) | `*big.Int` |
| float | `float64` |
| string | `string` |
| binary | `[]byte` |
| array, tabular array | `[]any` (tabular rows are `map[string]any`) |
| map | `map[string]any` |
| timestamp (ext -1) | `time.Time`: UTC, or in a fixed zone when it carries an offset; `btoon.Timestamp` if outside `time.Time`'s range |
| date (ext -2), datetime (ext -3) | `time.Time` (UTC) |
| float32 vector (ext -5) | `[]float32` |
| float64 vector (ext -6) | `btoon.VectorF64` (a `[]float64`) |
| other extensions | `btoon.Ext{Type, Data}` |

With `DecodeOptions.PreserveTimeTypes`, timestamps, dates and datetimes decode as
`btoon.Timestamp`, `btoon.Date` and `btoon.DateTime`, which re-encode byte for byte.

Encoding:

| Go | BTOON |
|---|---|
| `nil`, nil pointers, interfaces, slices and maps | nil |
| `bool` | boolean |
| all integer types | the smallest integer format for the **value**: `int8(5)` and `uint64(5)` are both `05` |
| `*big.Int`, `big.Int` | integer when in [-2^63, 2^64-1], otherwise bigint |
| `float32`, `float64` | float64; NaN is written as `7ff8000000000000` (Go's `math.NaN()` is `…0001`) |
| `string` (must be valid UTF-8) | string |
| `[]byte`, `[N]byte` | binary |
| `[]float32`, `[N]float32` | float32 vector |
| `btoon.VectorF64` | float64 vector (a plain `[]float64` is an array of floats) |
| other slices and arrays | array, or tabular when SPEC §5.6.2 says so |
| maps with string (or string-kind) keys, or `interface{}` keys holding strings | map, keys sorted by UTF-8 bytes |
| structs | map of the exported fields |
| `time.Time` | timestamp, normalized to UTC (see `PreserveTimeOffset`) |
| `btoon.Timestamp`, `btoon.Date`, `btoon.DateTime`, `btoon.Ext` | the corresponding extension |
| `encoding.BinaryMarshaler` | binary |

Channels, functions and complex numbers are rejected with `invalid_argument`, as are
`btoon.Ext` values using a type id that BTOON assigns (-1…-6, -10).

### Structs

Fields are encoded under their Go name or the name in a `btoon:"name,options"` tag.
If a field has no `btoon` tag, its `json` tag is used, so existing types work
unchanged. `-` skips a field, and `omitempty` and `omitzero` work as in
`encoding/json` (`omitzero` uses an `IsZero() bool` method if the type has one, so it
omits zero `time.Time` values). Embedded structs are flattened by the
`encoding/json` rules.

`Unmarshal` follows `encoding/json` conventions: it allocates pointers, maps and
embedded struct pointers as needed, leaves non-nullable values unchanged for a BTOON
nil, and ignores unknown keys unless `DisallowUnknownFields` is set. Key matching is
exact (case-sensitive). Integers fit into any integer type, failing with
`type_mismatch` on overflow; integers and floats fit into float types; binary fits into
`[]byte`, `[N]byte` of the same length, `string` and `encoding.BinaryUnmarshaler`;
strings fit into `string` and `[]byte`; time values fit into `time.Time`, `Timestamp`,
`Date` and `DateTime` when the conversion is exact.

## Options

`EncodeOptions` (the zero value is the default):

| Field | Default | Meaning |
|---|---|---|
| `Compress` | false | Wrap in a compression envelope. Skipped for documents under 256 bytes, or when the envelope would not be at least 5% smaller. |
| `Compression` | `btoon.Zlib` | `Zlib`, `LZ4`, `Zstd`, `Brotli` or `Snappy`. Unregistered algorithms fail with `unsupported_compression`. |
| `CompressionLevel` | 0 | Passed to the codec; 0 is the codec's default. |
| `DisableTabular` | false | Never use the tabular encoding (the cross-language `auto_tabular = false`). |
| `MaxDepth` | 128 | Nesting limit; deeper values and cyclic data fail with `max_depth`. |
| `HMACKey` | nil | Sign the output (SPEC §8). An empty non-nil key is an error. |
| `PreserveTimeOffset` | false | Write a `time.Time` in a non-UTC location with its UTC offset (the 14-byte timestamp form). |

`DecodeOptions`:

| Field | Default | Meaning |
|---|---|---|
| `NonStrict` | false | SPEC §7 non-strict mode: allow trailing bytes and duplicate keys (last wins), skip UTF-8 validation. Bounds, depth and allocation checks still apply. |
| `MaxDepth` | 128 | Nesting limit. A tabular array counts as two levels. |
| `MaxDecompressedSize` | 256 MiB | Largest accepted `uncompressed_size` of a compression envelope. |
| `DisableDecompression` | false | Reject compression envelopes (the cross-language `auto_decompress = false`). |
| `HMACKey` | nil | Require a signed envelope and verify it before parsing. |
| `PreserveTimeTypes` | false | Decode time extensions as `Timestamp`, `Date`, `DateTime`. |
| `DisallowUnknownFields` | false | `Unmarshal` fails on map keys without a matching struct field. |

Go options are struct fields whose zero value is the default, so the cross-language
options that default to true appear inverted (`DisableTabular`, `NonStrict`,
`DisableDecompression`).

## Compression

zlib is built in, as the specification requires. The other algorithms of SPEC §6 come
from the [`codecs`](codecs/codecs.go) package, which registers them when imported:

```go
import _ "github.com/BTOON-project/btoon-go/codecs"

b, err := btoon.MarshalWithOptions(v, btoon.EncodeOptions{Compress: true, Compression: btoon.Zstd})
```

| Algorithm | Package used by `codecs` |
|---|---|
| LZ4 (block format) | `github.com/pierrec/lz4/v4` |
| Zstandard | `github.com/klauspost/compress/zstd` |
| Brotli | `github.com/andybalholm/brotli` |
| Snappy (raw block format) | `github.com/golang/snappy` (the strict reference decoder) |

None of them uses cgo. Only programs that import `codecs` compile them; package `btoon`
itself imports nothing outside the standard library. You can also implement the
`btoon.Codec` interface yourself and call `btoon.RegisterCodec`.

Decoders check the envelope header and the size limit before decompressing, stop at
the declared size while decompressing, and reject payloads that are corrupt, have
trailing bytes, or expand to more or fewer bytes than declared (`invalid_envelope`). A
Zstandard payload must be one frame: further frames and skippable frames are rejected,
as in btoon-core. If a codec panics, which is a bug in that codec, `Marshal` and
`Decode` recover and return an error instead of crashing the program.

## Errors

Every error is a `*btoon.Error`:

```go
var e *btoon.Error
if errors.As(err, &e) {
    log.Println(e.Code, e.Offset)
}
if errors.Is(err, btoon.ErrAuthenticationFailed) { ... }
```

`Code` is one of the SPEC §7 codes, which are shared by all BTOON implementations:

| Code | Meaning |
|---|---|
| `truncated` | Input ends before a value is complete, or a length runs past the input |
| `invalid_marker` | The reserved byte `0xc1` |
| `trailing_data` | Bytes after the top-level value |
| `invalid_utf8` | A string, map key or column name is not well-formed UTF-8 |
| `non_string_key` | A map key is not a string |
| `duplicate_key` | A map has the same key twice |
| `max_depth` | Nesting deeper than `MaxDepth`, including cyclic Go values |
| `invalid_extension` | A malformed BTOON extension payload |
| `invalid_tabular` | A malformed tabular payload |
| `invalid_envelope` | A malformed envelope, or a compressed payload that is corrupt or has the wrong size |
| `size_limit` | A size above a limit, such as `MaxDecompressedSize` |
| `unsupported_compression` | No codec is registered for the algorithm |
| `authentication_failed` | Bad or missing signature, or a signed document decoded without a key |

and two codes specific to Go: `invalid_argument` (unsupported Go types, invalid options,
non-pointer `Unmarshal` targets) and `type_mismatch` (`Unmarshal` cannot store a value in
the target type). There is a `Code…` constant and an `Err…` sentinel for each.

## Conformance and testing

`testdata/btoon-vectors.json` is a verbatim copy of btoon-core's conformance vectors;
`make sync-vectors` refreshes it from `../btoon-core` (override with `CORE=path`). The
conformance test decodes every valid vector and compares it with its typed value,
re-encodes every canonical vector byte for byte, and checks that every invalid vector
fails with the expected code. Without the `codecs` package three vectors (zstd, lz4,
brotli) are skipped; the `codecs` tests decode them.

```sh
make test         # go test -race ./...  (unit tests + conformance vectors)
make vet          # go vet ./...
make fmt-check    # gofmt -l
make fuzz         # native Go fuzzing of the decoder, FUZZTIME=60s
make bench        # benchmarks
make example      # go run ./examples/basic
make sync-vectors # copy ../btoon-core/tests/vectors/btoon-vectors.json
```

CI also runs [govulncheck](https://go.dev/doc/security/vuln/) on every push and weekly,
so a new advisory for a codec dependency fails the build.

`FuzzDecode` checks that decoding never panics, that all failures carry a SPEC §7 code,
and that decode → encode → decode returns an equal value whose encoding is stable.

## Benchmarks

Measured with `make bench` (`go test -bench=. -benchmem`; median of 5 runs) on an
Apple M4 Max (16 cores, 48 GiB RAM), macOS 27.2, Go 1.27.3, darwin/arm64. The data is
1000 records of a struct with seven fields (`int64`, two strings, `bool`, `float64`,
`[]string`, `time.Time`); both libraries encode the same Go values (see
[`bench_test.go`](bench_test.go)). Numbers will differ on other machines.

| Operation (1000 records) | btoon | encoding/json |
|---|---|---|
| Marshal | 272 µs, 559 KB, 1,030 allocs | 258 µs, 142 KB, 4 allocs |
| Unmarshal into `[]struct` | 438 µs, 738 KB, 15,769 allocs | 551 µs, 318 KB, 4,010 allocs |
| Decode into dynamic values | 197 µs, 551 KB, 12,761 allocs | not measured |
| Marshal with zlib envelope | 516 µs, 1.7 MB, 1,055 allocs | — |

| Encoded size | Bytes |
|---|---|
| BTOON (tabular) | 51,508 |
| BTOON with `DisableTabular` | 92,399 |
| BTOON in a zlib envelope | 13,361 |
| JSON | 135,596 |

`Unmarshal` currently decodes into the dynamic representation first and then assigns it
to the target, which accounts for most of its allocations.

## License

MIT; see [LICENSE](LICENSE).
