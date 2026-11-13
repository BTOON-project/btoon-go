# Changelog

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
