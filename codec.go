package btoon

import (
	"bytes"
	"compress/zlib"
	"errors"
	"io"
	"strconv"
	"sync"
)

// Compression identifies a compression algorithm of the compression
// envelope (SPEC §6). The zero value is Zlib.
type Compression uint8

// Compression algorithms defined by SPEC §6. Zlib is built in; the others
// need a Codec registered with RegisterCodec, for example by importing
// github.com/BTOON-project/btoon-go/codecs.
const (
	Zlib   Compression = 0 // RFC 1950 zlib stream (required)
	LZ4    Compression = 1 // LZ4 block format, no frame
	Zstd   Compression = 2 // Zstandard frame (RFC 8878)
	Brotli Compression = 3 // Brotli stream (RFC 7932)
	Snappy Compression = 4 // Snappy raw (block) format
)

var compressionNames = [...]string{"zlib", "lz4", "zstd", "brotli", "snappy"}

// String returns the algorithm's name ("zlib", "lz4", "zstd", "brotli",
// "snappy").
func (c Compression) String() string {
	if int(c) < len(compressionNames) {
		return compressionNames[c]
	}
	return "compression(" + strconv.Itoa(int(c)) + ")"
}

// A Codec implements one compression algorithm for the compression
// envelope. Implementations must be safe for concurrent use. A panic in
// Compress or Decompress is recovered and reported as an error, with
// CodeInvalidArgument from Compress and CodeInvalidEnvelope from Decompress.
type Codec interface {
	// Compress compresses src. level is EncodeOptions.CompressionLevel;
	// 0 selects the codec's default.
	Compress(src []byte, level int) ([]byte, error)

	// Decompress decompresses src, which the envelope declares to expand to
	// exactly size bytes. size has already been checked against
	// DecodeOptions.MaxDecompressedSize. Decompress must stop once it has
	// produced size bytes, must not allocate much more than the input
	// justifies (a header can lie about size), and must return an error if
	// the payload is corrupt, has trailing bytes, or does not expand to
	// exactly size bytes. ReadDecompressed implements the size handling for
	// streaming decompressors.
	Decompress(src []byte, size int) ([]byte, error)
}

var (
	codecsMu sync.RWMutex
	codecs   = map[Compression]Codec{Zlib: zlibCodec{}}
)

// RegisterCodec makes codec available for algorithm c, replacing any codec
// registered before (including the built-in zlib codec). It panics if c is
// not one of the algorithms defined by SPEC §6 or if codec is nil.
// RegisterCodec is usually called from an init function.
func RegisterCodec(c Compression, codec Codec) {
	if int(c) >= len(compressionNames) {
		panic("btoon: RegisterCodec: unknown compression algorithm " + c.String())
	}
	if codec == nil {
		panic("btoon: RegisterCodec: nil codec")
	}
	codecsMu.Lock()
	defer codecsMu.Unlock()
	codecs[c] = codec
}

func lookupCodec(c Compression) Codec {
	codecsMu.RLock()
	defer codecsMu.RUnlock()
	return codecs[c]
}

// ReadDecompressed reads the output of the decompressing reader r, which is
// expected to produce exactly size bytes and then io.EOF. Memory grows with
// the output actually produced rather than with size, so a lying header
// cannot force a large allocation, and reading stops as soon as r produces
// more than size bytes. Errors have code CodeInvalidEnvelope unless r
// itself fails, in which case its error is returned. It is a helper for
// Codec implementations.
func ReadDecompressed(r io.Reader, size int) ([]byte, error) {
	if size < 0 {
		return nil, newError(CodeInvalidArgument, "negative decompressed size")
	}
	const initial = 64 << 10
	buf := make([]byte, 0, min(size, initial))
	var probe [1]byte
	for stalls := 0; ; {
		var n int
		var err error
		if len(buf) < size {
			if len(buf) == cap(buf) {
				// Double the buffer (append grows large slices by only 1.25x,
				// which would allocate about five times the output in total).
				grown := make([]byte, len(buf), min(2*cap(buf), size))
				copy(grown, buf)
				buf = grown
			}
			n, err = r.Read(buf[len(buf):cap(buf)])
			buf = buf[:len(buf)+n]
		} else {
			// All declared bytes are in; anything more is an error.
			n, err = r.Read(probe[:])
			if n > 0 {
				return nil, newError(CodeInvalidEnvelope, "payload decompresses to more than the declared "+strconv.Itoa(size)+" bytes")
			}
		}
		switch {
		case err == io.EOF:
			if len(buf) != size {
				return nil, newError(CodeInvalidEnvelope, "payload decompresses to "+strconv.Itoa(len(buf))+" bytes, but the envelope declares "+strconv.Itoa(size))
			}
			return buf, nil
		case err != nil:
			return nil, err
		case n == 0:
			if stalls++; stalls > 100 {
				return nil, io.ErrNoProgress
			}
		default:
			stalls = 0
		}
	}
}

// zlibCodec is the built-in, mandatory zlib codec (RFC 1950).
type zlibCodec struct{}

func (zlibCodec) Compress(src []byte, level int) ([]byte, error) {
	if level == 0 {
		level = zlib.DefaultCompression
	}
	var buf bytes.Buffer
	w, err := zlib.NewWriterLevel(&buf, level)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(src); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (zlibCodec) Decompress(src []byte, size int) ([]byte, error) {
	br := bytes.NewReader(src)
	zr, err := zlib.NewReader(br)
	if err != nil {
		return nil, err
	}
	out, err := ReadDecompressed(zr, size)
	if err != nil {
		return nil, err
	}
	// bytes.Reader is an io.ByteReader, so the zlib reader consumes exactly
	// the stream (including its Adler-32 checksum) and nothing more.
	if br.Len() != 0 {
		return nil, errors.New("trailing bytes after the zlib stream")
	}
	return out, nil
}
