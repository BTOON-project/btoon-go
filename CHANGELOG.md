# Changelog

## 0.2.0 (2026-11-13)

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
