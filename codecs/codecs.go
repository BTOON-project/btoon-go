// Package codecs registers the optional compression algorithms of the BTOON
// compression envelope (SPEC §6) with package btoon: LZ4 (block format),
// Zstandard, Brotli and Snappy (raw block format). zlib is always built in.
//
// Import the package for its side effect:
//
//	import _ "github.com/BTOON-project/btoon-go/codecs"
//
// The codecs use github.com/pierrec/lz4/v4 (to compress),
// github.com/klauspost/compress (zstd), github.com/andybalholm/brotli and
// github.com/golang/snappy, all without cgo. LZ4 blocks are decompressed by
// this package's own decoder. Package btoon itself has no third-party
// dependencies.
//
// Every decompressor stops at the size declared by the envelope (which
// btoon has already checked against DecodeOptions.MaxDecompressedSize) and
// fails unless the payload expands to exactly that size. An LZ4 block must
// be valid under the block format, end-of-block conditions included
// (SPEC §6): it ends with a sequence of literals only, its last 5 bytes are
// literals, its last match starts at least 12 bytes before the end, and no
// match offset is 0 or reaches back before the start of the output. A
// Zstandard payload must be a single frame, with no skippable frames.
package codecs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/andybalholm/brotli"
	"github.com/golang/snappy"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"

	btoon "github.com/BTOON-project/btoon-go"
)

func init() {
	btoon.RegisterCodec(btoon.LZ4, LZ4)
	btoon.RegisterCodec(btoon.Zstd, Zstd)
	btoon.RegisterCodec(btoon.Brotli, Brotli)
	btoon.RegisterCodec(btoon.Snappy, Snappy)
}

// The codecs registered by this package.
var (
	LZ4    btoon.Codec = lz4Codec{}
	Zstd   btoon.Codec = &zstdCodec{}
	Brotli btoon.Codec = brotliCodec{}
	Snappy btoon.Codec = snappyCodec{}
)

var errSize = errors.New("payload does not decompress to the declared size")

// --- LZ4 (block format, no frame) ----------------------------------------------------

type lz4Codec struct{}

var lz4Levels = [...]lz4.CompressionLevel{lz4.Level1, lz4.Level2, lz4.Level3, lz4.Level4, lz4.Level5, lz4.Level6, lz4.Level7, lz4.Level8, lz4.Level9}

var lz4Fast = sync.Pool{New: func() any { return new(lz4.Compressor) }}

// Compress uses the fast LZ4 compressor for level 0 (and below) and the
// high-compression compressor for levels 1 to 9.
func (lz4Codec) Compress(src []byte, level int) ([]byte, error) {
	dst := make([]byte, lz4.CompressBlockBound(len(src)))
	var n int
	var err error
	if level <= 0 {
		c := lz4Fast.Get().(*lz4.Compressor)
		n, err = c.CompressBlock(src, dst)
		lz4Fast.Put(c)
	} else {
		c := lz4.CompressorHC{Level: lz4Levels[min(level, len(lz4Levels))-1]}
		n, err = c.CompressBlock(src, dst)
	}
	if err != nil {
		return nil, err
	}
	return dst[:n], nil
}

// Decompress uses this package's own block decoder rather than
// pierrec/lz4's, which accepts blocks that break the block format's
// end-of-block conditions and rejects some that liblz4 accepts. See
// decodeLZ4Block.
func (lz4Codec) Decompress(src []byte, size int) ([]byte, error) {
	return decodeLZ4Block(src, size)
}

// --- Zstandard (RFC 8878 frame) --------------------------------------------------------

type zstdCodec struct {
	encoders sync.Map // level -> *zstd.Encoder (EncodeAll is safe for concurrent use)
}

func (c *zstdCodec) Compress(src []byte, level int) ([]byte, error) {
	if e, ok := c.encoders.Load(level); ok {
		return e.(*zstd.Encoder).EncodeAll(src, nil), nil
	}
	opts := []zstd.EOption{zstd.WithEncoderConcurrency(1)}
	if level != 0 {
		opts = append(opts, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(level)))
	}
	enc, err := zstd.NewWriter(nil, opts...)
	if err != nil {
		return nil, err
	}
	e, _ := c.encoders.LoadOrStore(level, enc)
	return e.(*zstd.Encoder).EncodeAll(src, nil), nil
}

// Zstandard frames may declare a window larger than their content (for
// example when written by a streaming compressor). Windows up to 8 MiB,
// enough for every non-"ultra" level, are always accepted; larger windows
// only when the declared size needs them, and never above 128 MiB (the
// default limit of the reference zstd library).
const (
	zstdMinWindowLimit = 8 << 20
	zstdMaxWindowLimit = 128 << 20
)

func (*zstdCodec) Decompress(src []byte, size int) ([]byte, error) {
	// The payload is exactly one Zstandard frame (SPEC §6). The decoder
	// would go on to read further frames, skipping skippable ones, so find
	// where the first frame ends before decoding.
	n, err := zstdFrameSize(src)
	if err != nil {
		return nil, err
	}
	if n != len(src) {
		return nil, errors.New("zstd: trailing bytes after the frame")
	}
	window := uint64(min(max(size, zstdMinWindowLimit), zstdMaxWindowLimit))
	d, err := zstd.NewReader(bytes.NewReader(src),
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderLowmem(true),
		zstd.WithDecoderMaxMemory(window),
		zstd.WithDecoderMaxWindow(window))
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return btoon.ReadDecompressed(d, size)
}

var errZstdTruncated = errors.New("zstd: frame is truncated")

// zstdFrameSize returns the size of the Zstandard frame at the start of src
// (RFC 8878 §3.1.1) by walking its block headers; the decoder checks the
// blocks themselves. Skippable frames are not accepted.
func zstdFrameSize(src []byte) (int, error) {
	const magic = 0xFD2FB528
	if len(src) < 5 || binary.LittleEndian.Uint32(src) != magic {
		return 0, errors.New("zstd: payload does not start with a zstd frame")
	}
	fhd := src[4] // Frame_Header_Descriptor
	singleSegment := fhd&0x20 != 0
	n := 5
	if !singleSegment {
		n++ // Window_Descriptor
	}
	n += [4]int{0, 1, 2, 4}[fhd&3] // Dictionary_ID
	switch fhd >> 6 {              // Frame_Content_Size
	case 0:
		if singleSegment {
			n++
		}
	case 1:
		n += 2
	case 2:
		n += 4
	case 3:
		n += 8
	}
	for last := false; !last; {
		if len(src)-n < 3 {
			return 0, errZstdTruncated
		}
		h := int(src[n]) | int(src[n+1])<<8 | int(src[n+2])<<16
		n += 3
		last = h&1 != 0
		size := h >> 3 // Block_Size
		switch h >> 1 & 3 {
		case 1: // RLE_Block: a single byte, repeated Block_Size times
			size = 1
		case 3:
			return 0, errors.New("zstd: reserved block type")
		}
		if len(src)-n < size {
			return 0, errZstdTruncated
		}
		n += size
	}
	if fhd&0x04 != 0 { // Content_Checksum
		n += 4
	}
	if n > len(src) {
		return 0, errZstdTruncated
	}
	return n, nil
}

// --- Brotli (RFC 7932 stream) ------------------------------------------------------------

type brotliCodec struct{}

func (brotliCodec) Compress(src []byte, level int) ([]byte, error) {
	if level == 0 {
		level = brotli.DefaultCompression
	}
	var buf bytes.Buffer
	w := brotli.NewWriterLevel(&buf, level)
	if _, err := w.Write(src); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decompress fails on trailing bytes after the stream: the brotli reader
// reports them as "excessive input".
func (brotliCodec) Decompress(src []byte, size int) ([]byte, error) {
	return btoon.ReadDecompressed(brotli.NewReader(bytes.NewReader(src)), size)
}

// --- Snappy (raw block format) ---------------------------------------------------------

// snappyCodec uses github.com/golang/snappy, which accepts exactly the
// Snappy format. (The S2-based decoder of klauspost/compress/snappy also
// accepts S2 extensions such as repeat offsets, which the reference Snappy
// library rejects; that would be a parser differential.)
type snappyCodec struct{}

func (snappyCodec) Compress(src []byte, _ int) ([]byte, error) {
	if snappy.MaxEncodedLen(len(src)) < 0 {
		return nil, snappy.ErrTooLarge
	}
	return snappy.Encode(nil, src), nil
}

func (snappyCodec) Decompress(src []byte, size int) ([]byte, error) {
	n, err := snappy.DecodedLen(src)
	if err != nil {
		return nil, err
	}
	if n != size {
		return nil, errSize
	}
	// A snappy block expands at most about 21x (a 3-byte copy of 64
	// bytes); reject impossible claims before allocating.
	if uint64(n) > uint64(len(src))*32+64 {
		return nil, fmt.Errorf("snappy: %d bytes cannot expand to %d", len(src), n)
	}
	out, err := snappy.Decode(make([]byte, n), src)
	if err != nil {
		return nil, err
	}
	if len(out) != size {
		return nil, errSize
	}
	return out, nil
}
