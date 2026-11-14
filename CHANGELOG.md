# Changelog

## 0.3.0 (2026-11-14)

Implements BTOON spec 0.3 (`SpecVersion` is now `"0.3"`). The wire format is
unchanged. LZ4 envelopes are checked as SPEC §6 now requires, and the remaining
findings of the 2026-11-13 review for this library are fixed. Some of the fixes change
what `Marshal` writes for Go types with marshaling methods; they are marked
**behavior change**.

- **Changed: an LZ4 block must be valid under the block format, end-of-block
  conditions included** (SPEC §6). It ends with a sequence of literals only, its last 5
  bytes of output are literals, its last match starts at least 12 bytes before the end,
  and no match offset is 0 or reaches back before the start of the output; the
  match-length bits of the final token are ignored. A block that breaks these is
  `invalid_envelope`. The `codecs` package now decodes LZ4 blocks itself and uses
  `pierrec/lz4` only to compress. pierrec's decoder accepted blocks that end with a
  match, rejected valid ones whose final token has match-length bits set, and could
  reach different verdicts by platform, since it has assembly decoders for some.
  **Behavior change** for such crafted blocks; no compressor writes them.
  - In a differential test against liblz4 1.11.1 (3 million generated blocks and tens
    of millions of fuzzed ones), the verdicts and output agree, except where liblz4
    is looser than SPEC §6. It skips the end-of-block checks for a short match it copies
    on its fast path, which a sequence can take while at least 32 bytes of output
    remain, and it decodes a match with offset 0 as zeros. A decoder built on liblz4,
    such as btoon-core's, needs a check of its own.
- **Fixed (crash):** `Marshal` panicked on an `omitzero` interface field that holds a
  nil pointer, when the pointer's type has an `IsZero` method. Such a field is zero, as
  in encoding/json.
- **Fixed (crash):** `Unmarshal` panicked, as encoding/json does, on an embedded pointer
  to an unexported struct type that has a tag (``*inner `btoon:"in"` ``) when it had to
  allocate the pointer or set it to nil. That is now `invalid_argument`. `Marshal` and
  `Unmarshal` also panicked when such a field's type has an `IsZero` or `UnmarshalBinary`
  method, which reflection can't call; the field is now encoded and decoded by its kind.
- **Changed: methods with a pointer receiver are used whether or not the value is
  addressable.** `Marshal` called a `MarshalBinary` method with a pointer receiver only
  on addressable values, so a value encoded one way by pointer or in a slice, and another
  way by value or in a map. It now calls the method on a copy, as encoding/json/v2 does.
  **Behavior change** for values that aren't addressable, such as map values or a value
  passed to `Marshal` directly, of such types: they now encode as binary instead of by
  their kind.
- **Changed: methods promoted from an embedded field follow encoding/json's rule,** now
  documented. A struct that embeds a type with `MarshalBinary` encodes as that method's
  result, without its other fields.
  - A struct that embeds `time.Time` gets `time.Time`'s `MarshalBinary` too, and so
    encoded as Go's private binary format. It now encodes as the embedded time, a
    timestamp, or nil for a nil `*time.Time`, much as encoding/json writes it as the
    time's JSON. `Unmarshal` stores timestamps, dates and datetimes in the embedded time.
    The same holds for an embedded `big.Int` and integers.
  - A struct that declares a method of its own with a different result is still encoded
    with it.
  - **Behavior change:** such structs encoded as binary (`time.Time`, when addressable)
    or as `{}` (`big.Int`). The binary that 0.2 wrote still decodes, through the
    promoted `UnmarshalBinary`.
- **Added: `encoding.TextMarshaler` and `encoding.TextUnmarshaler` for struct types.** A
  struct type with `MarshalText` but no `MarshalBinary` encodes as its text, a string,
  as in encoding/json, and `Unmarshal` passes strings to its `UnmarshalText`. A struct's
  fields don't always hold its value: `*big.Float`, `*big.Rat` and `*regexp.Regexp`
  encoded as `{}`. Types of other kinds keep the encoding of their kind, so `net.IP`
  stays binary and integer enums with `MarshalText` stay integers. A struct that gets
  `MarshalText` from an embedded field, such as one that embeds `net.IP`, is still a map
  of its fields. **Behavior change** for struct types with `MarshalText` and exported
  fields, which encoded as a map of those fields.
- **Fixed:** a float32 vector of a named element type (`[]Celsius`) keeps every bit of
  its elements, signaling NaNs included, as SPEC §5.5 requires. `Marshal` and
  `Unmarshal` converted the elements through float64, which quiets them. `Unmarshal`
  into a `[N]float32` array did too.
- **Fixed (denial of service): each document of a stream is bounded.** `Decoder`
  buffered a document of any size before decoding it, so a peer could make it buffer
  gigabytes. A stream document can now take at most `MaxDecompressedSize` bytes (64 MiB
  by default), as in btoon-core's `StreamDecoder`, and `Decoder` stops reading a
  document once it can tell that the document has more than `MaxValues` values. Either
  fails with `size_limit`. After that, or after a reader error inside a document,
  `Decode` returns the same error every time, since the decoder has lost its place in
  the stream. **Behavior change** for stream documents over 64 MiB, which need a larger
  `MaxDecompressedSize`.
- **Tests:**
  - The vendored conformance vectors are spec 0.3's (311).
  - The harness moved to `internal/conformance`. The `codecs` tests run all 311 vectors
    with every codec registered; package `btoon`'s tests skip the eleven that need one.
  - Canonical vectors also re-encode byte for byte from what `Decode` returns with
    `PreserveTimeTypes`, which covers the new date, datetime and table vectors
    (SPEC §10).
  - New fuzz targets for the LZ4 decoder, `FuzzLZ4Block` and `FuzzLZ4RoundTrip`. CI
    runs `FuzzLZ4Block`, and `make fuzz` runs all three.
  - `TestIntegersIgnoreGoType` no longer truncates 2^32 to a 32-bit `uint` on 386.

## 0.2.0 (2026-11-14)

Implements BTOON spec 0.2 (`SpecVersion` is now `"0.2"`; decisions D23–D26 in the
workspace's docs/DECISIONS.md). The wire format is unchanged: every valid 0.1 document
that fits the new limits decodes to the same value. The new limits can reject documents
that 0.1.x accepted, hence the minor version bump planned for this release.

- **Added: `DecodeOptions.MaxValues`** (default `DefaultMaxValues`, 2^20 = 1,048,576),
  which bounds how many values a document decodes to (SPEC §7.1). Each value counts once
  when it starts, before anything inside it: the top-level value, array elements, map
  values and extensions of every type. A tabular array counts when it starts, its rows
  count together once its header is checked, and each cell counts when it starts. Map
  keys and envelopes don't count; the values inside a compression envelope do. A
  document over the limit fails with `size_limit`, also through `Unmarshal`, and each
  document of a stream (`Decoder`) has its own budget. 0 means the default; a negative
  value is `invalid_argument`. **Behavior change:** documents of more than 2^20 values,
  such as a table of more than about half a million one-column rows, now need a larger
  `MaxValues`.
- **Added: the tabular column-name bound.** A table whose rows × the total byte length
  of its column names exceed `MaxDecompressedSize` fails with `size_limit` (SPEC §5.6.3).
  After the column names and type codes, a table's header is checked in this order:
  rows × columns against the remaining payload (`invalid_tabular`), then the name bytes,
  then the row count against `MaxValues`.
- **Changed: `DefaultMaxDecompressedSize` is 64 MiB**, down from 256 MiB. Envelopes
  declaring more fail with `size_limit` unless `MaxDecompressedSize` is raised.
- **Changed: `MaxDepth` can't exceed 1000** (new constant `MaxDepthLimit`), in both
  `EncodeOptions` and `DecodeOptions`; a larger value is `invalid_argument`. It was
  65,536.
- **Changed: non-strict mode checks map keys and column names.** `NonStrict` now skips
  UTF-8 validation of string values only; an invalid key or column name is
  `invalid_utf8` in both modes (SPEC §7). Keys compare by their bytes, and a decoder
  that replaced invalid sequences could merge two keys. Column names are still checked
  front to back, each for UTF-8 and then against the earlier names, so the first bad
  name sets the error (SPEC §5.6.3); this was already the order in strict mode.
- **Changed:** `MarshalWithOptions` rejects an empty `HMACKey` before encoding, so an
  invalid key is reported even when the value can't be encoded.
- **Changed:** a stream (`Decoder`) that ends inside a document reports what `Decode`
  reports for the bytes it holds. That is still `truncated`, unless the decoder finds an
  error first: `92a1ff` is `invalid_utf8`, and `81a161` with `MaxValues: 1` is
  `size_limit`, because the map's value counts as it starts (vector
  `limits/counted-before-truncation`). It was always `truncated`.
- **Fixed (denial of service):** decoded size is now bounded by the limits above. An
  8 KB zlib envelope of a table of nil cells peaked at 3.3 GB and now fails with
  `size_limit` at 33 MB; a 61 KB envelope of empty maps behind an array32 header peaked
  at 4.3 GB and now fails at 187 MB (peak RSS, darwin/arm64). A container also no longer
  reserves more slots than `MaxValues` has left, since it would fail before filling
  them: a 64 MiB envelope announcing 64 Mi elements reserved 1 GiB of slots. With the
  defaults, the largest documents that decode take about 210 MB.
- **Unchanged, now covered by the vectors:** `Ext` values with the ids BTOON defines
  (-1…-6 and -10) are `invalid_argument`, and every other id, including those reserved
  for future versions (-9…-7, -128…-11), is written as given (SPEC §5.7). zlib payloads
  with FDICT set are `invalid_envelope` (since 0.1.2). An empty, non-nil `HMACKey` is
  `invalid_argument`.
- **Tests:** the vendored conformance vectors are spec 0.2's (298). The harness honors
  every vector option (`strict`, `max_depth`, `max_decompressed_size`, `max_values` and
  `hmac_key`, where an empty string is an empty key), runs the `encode_error` vectors,
  and reads float vector elements bit for bit (`"NaN"` is the canonical quiet NaN of the
  width, `"0x…"` gives exact bits).

## 0.1.2 (2026-11-13)

- **Fixed (denial of service):**
  - Nested containers could reserve far more memory than the document justifies. Each
    level checked its announced element count against the same remaining bytes, so 1 MiB
    documents of 127 nested array32 headers, 127 nested map32 headers or 64 nested
    tabulars reserved 2 GiB, 5 GiB and 3.5 GiB before failing; as zlib envelopes they are
    about 1.5 KB. With `MaxDepth` raised to 65,536, 198 KB of nested array16 headers
    reserved 1.1 GiB. Reservations now come out of one budget per document, the
    document's size in bytes: a container reserves exactly what it announces while the
    budget covers it, and otherwise starts empty and grows. A valid document never
    exceeds the budget, so decoding speed and memory are unchanged. The documents above
    now peak at 45 MiB, 80 MiB, 57 MiB and 3 MiB.
  - `Unmarshal` error messages describe an integer larger than 128 bits by its size
    ("integer of 67108863 bits") instead of formatting it in decimal. An 8 MiB bigint,
    which fits in an 8 KB envelope, took about 12 s of CPU per failed `Unmarshal`.
- **Fixed:** a zlib payload whose header requires a preset dictionary (FLG.FDICT) is
  rejected with `invalid_envelope`. `compress/zlib` accepted it when the dictionary id
  was 1 (the Adler-32 of an empty dictionary), which every other BTOON implementation
  rejects.

## 0.1.1 (2026-11-13)

- **Fixed (crash):** a crafted zstd envelope could panic programs that import
  `btoon-go/codecs` (GO-2026-6865 in klauspost/compress). The module now requires
  klauspost/compress v1.20.4 and Go 1.25, and a panic in any codec is returned as an
  error (`invalid_envelope` when decompressing, `invalid_argument` when compressing).
- **Fixed:** a zstd payload must be exactly one frame. A second frame or a skippable
  frame is `invalid_envelope`.

## 0.1.0 (2026-11-07)

First release, implementing BTOON spec 0.1.
