package codecs_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	btoon "github.com/BTOON-project/btoon-go"
	"github.com/BTOON-project/btoon-go/codecs"
	"github.com/BTOON-project/btoon-go/internal/conformance"
)

var all = []struct {
	name  string
	algo  btoon.Compression
	codec btoon.Codec
}{
	{"lz4", btoon.LZ4, codecs.LZ4},
	{"zstd", btoon.Zstd, codecs.Zstd},
	{"brotli", btoon.Brotli, codecs.Brotli},
	{"snappy", btoon.Snappy, codecs.Snappy},
}

// TestConformanceVectors runs every conformance vector with all the codecs
// registered. Package btoon's tests skip the vectors that need a codec; here
// none may be skipped.
func TestConformanceVectors(t *testing.T) {
	conformance.Run(t, conformance.Load(t, "../testdata/btoon-vectors.json"), true)
}

func sampleDoc() map[string]any {
	rows := make([]any, 200)
	for i := range rows {
		rows[i] = map[string]any{"id": int64(i), "name": fmt.Sprintf("user%d", i), "email": fmt.Sprintf("user%d@example.com", i)}
	}
	return map[string]any{"rows": rows}
}

func envelope(algo btoon.Compression, payload []byte, usize int) []byte {
	out := append([]byte("BTON"), 1, byte(algo), 0, 0)
	out = binary.BigEndian.AppendUint32(out, uint32(len(payload)))
	out = binary.BigEndian.AppendUint32(out, uint32(usize))
	return append(out, payload...)
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var be *btoon.Error
	if !errors.As(err, &be) || be.Code != code {
		t.Fatalf("want error code %q, got %v", code, err)
	}
}

func TestRoundTrip(t *testing.T) {
	doc := sampleDoc()
	raw, err := btoon.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range all {
		for _, level := range []int{0, 1, 5, 9} {
			t.Run(fmt.Sprintf("%s/level%d", c.name, level), func(t *testing.T) {
				out, err := btoon.MarshalWithOptions(doc, btoon.EncodeOptions{Compress: true, Compression: c.algo, CompressionLevel: level})
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.HasPrefix(out, []byte("BTON")) || out[5] != byte(c.algo) {
					t.Fatalf("no %s envelope: % x", c.name, out[:16])
				}
				if len(out) >= len(raw) {
					t.Fatalf("compressed %d bytes to %d", len(raw), len(out))
				}
				got, err := btoon.Decode(out)
				if err != nil {
					t.Fatal(err)
				}
				want, _ := btoon.Decode(raw)
				if !reflect.DeepEqual(got, want) {
					t.Fatal("round trip changed the document")
				}
				// Signed and compressed.
				key := []byte("0123456789abcdef0123456789abcdef")
				out, err = btoon.MarshalWithOptions(doc, btoon.EncodeOptions{Compress: true, Compression: c.algo, HMACKey: key})
				if err != nil {
					t.Fatal(err)
				}
				if got, err = btoon.DecodeWithOptions(out, btoon.DecodeOptions{HMACKey: key}); err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("signed round trip: %v", err)
				}
			})
		}
	}
}

// TestDecompressExactSize checks that every codec rejects payloads that
// expand to more or fewer bytes than declared, trailing garbage, truncated
// payloads and impossible size claims.
func TestDecompressExactSize(t *testing.T) {
	raw, err := btoon.Marshal(sampleDoc())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range all {
		t.Run(c.name, func(t *testing.T) {
			payload, err := c.codec.Compress(raw, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := btoon.Decode(envelope(c.algo, payload, len(raw))); err != nil {
				t.Fatalf("valid envelope: %v", err)
			}
			cases := map[string][]byte{
				"declared-too-small": envelope(c.algo, payload, len(raw)-1),
				"declared-too-large": envelope(c.algo, payload, len(raw)+1),
				"truncated":          envelope(c.algo, payload[:len(payload)-3], len(raw)),
				"trailing-garbage":   envelope(c.algo, append(append([]byte{}, payload...), 0xde, 0xad, 0xbe, 0xef), len(raw)),
				"lying-header":       envelope(c.algo, payload[:8], btoon.DefaultMaxDecompressedSize),
				"empty-payload":      envelope(c.algo, nil, len(raw)),
			}
			for name, env := range cases {
				_, err := btoon.Decode(env)
				if err == nil {
					t.Errorf("%s: accepted", name)
					continue
				}
				var be *btoon.Error
				if !errors.As(err, &be) || be.Code != btoon.CodeInvalidEnvelope {
					t.Errorf("%s: want invalid_envelope, got %v", name, err)
				}
			}
			// The size limit is checked before the codec runs.
			_, err = btoon.DecodeWithOptions(envelope(c.algo, payload, len(raw)), btoon.DecodeOptions{MaxDecompressedSize: len(raw) - 1})
			wantCode(t, err, btoon.CodeSizeLimit)
		})
	}
}

// TestSnappyIsStrict pins the Snappy decoder to the reference format: this
// block uses an S2 "repeat offset" code (a copy with offset 0), which an
// S2-based decoder expands to "abcdabcdabcd" but Snappy must reject.
func TestSnappyIsStrict(t *testing.T) {
	block := []byte{0x0c, 0x0c, 'a', 'b', 'c', 'd', 0x01, 0x04, 0x01, 0x00}
	_, err := btoon.Decode(envelope(btoon.Snappy, block, 12))
	wantCode(t, err, btoon.CodeInvalidEnvelope)
	// The same block without the repeat code is valid Snappy.
	valid := []byte{0x08, 0x0c, 'a', 'b', 'c', 'd', 0x01, 0x04}
	if _, err := codecs.Snappy.Decompress(valid, 8); err != nil {
		t.Fatalf("valid block: %v", err)
	}
}

func TestSmallDocumentsStayUncompressed(t *testing.T) {
	for _, c := range all {
		out, err := btoon.MarshalWithOptions("hello", btoon.EncodeOptions{Compress: true, Compression: c.algo})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out, []byte{0xa5, 'h', 'e', 'l', 'l', 'o'}) {
			t.Fatalf("%s: % x", c.name, out)
		}
	}
}

func TestConcurrentCodecs(t *testing.T) {
	doc := sampleDoc()
	done := make(chan error, len(all)*8)
	for _, c := range all {
		for g := 0; g < 8; g++ {
			go func(algo btoon.Compression, level int) {
				for i := 0; i < 20; i++ {
					out, err := btoon.MarshalWithOptions(doc, btoon.EncodeOptions{Compress: true, Compression: algo, CompressionLevel: level})
					if err == nil {
						_, err = btoon.Decode(out)
					}
					if err != nil {
						done <- err
						return
					}
				}
				done <- nil
			}(c.algo, g%3)
		}
	}
	for i := 0; i < len(all)*8; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

// zstdPanicFrame is upstream's reproducer for GO-2026-6865
// (klauspost/compress v1.20.2, zstd/decoder_test.go
// TestDecoderHuffWeightsTableLog). Versions before v1.20.2 panic with an
// index out of range while decoding it.
func zstdPanicFrame() []byte {
	return slices.Concat(
		[]byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 0x38, 0x74, 0x06, 0x00, 0x42, 0xb8, 0x32, 0x0d, 0x1a, 0x00, 0x00, 0x00, 0x90, 0xaf, 0xfa, 0xff, 0x03, 0x00, 0x80, 0xff, 0x7f},
		bytes.Repeat([]byte{0xa5, 0x94, 0x52, 0x4a, 0x29}, 37),
		[]byte{0xa5, 0x94, 0x12, 0x00, 0xad, 0x56, 0x03, 0xee, 0xff, 0xdf, 0xb3, 0x1a, 0x1c, 0x1a, 0x00, 0xe0, 0xff, 0x1f, 0x0f, 0xb1, 0x9c, 0x5a, 0xcf, 0x4d, 0xe4, 0x1d, 0x7b, 0xab, 0x99, 0x5e, 0x01, 0x51, 0xa4, 0x73, 0xd8, 0xb1, 0xf3, 0x39, 0x4b, 0x67, 0xf6, 0xab, 0x1a, 0xab, 0x1a, 0xab, 0x1a},
		bytes.Repeat([]byte{0x8c, 0x31, 0xc6, 0x18, 0x63}, 1365),
		[]byte{0x8c, 0x31},
		bytes.Repeat([]byte{0x63, 0x8c, 0x31, 0xc6, 0x18}, 1365),
		[]byte{0x63, 0x2c},
		bytes.Repeat([]byte{0x31, 0xc6, 0x18, 0x63, 0x8c}, 1365),
		[]byte{0x31, 0x46},
		bytes.Repeat([]byte{0x31, 0xc6, 0x18, 0x63, 0x8c}, 1365),
		[]byte{0x31, 0x06, 0x00},
	)
}

// TestZstdCraftedFrames checks that the frames behind GO-2026-6865 are
// rejected without a panic, both by the codec and through Decode. The
// second case splits the frame in two: before the fix, decoding the first
// half left bad state in a pool and decoding the second half then panicked.
func TestZstdCraftedFrames(t *testing.T) {
	f := zstdPanicFrame()
	two := slices.Concat(f[:6], []byte{0x75, 0x06, 0x00}, f[9:215], f[:6], f[215:])
	for name, payloads := range map[string][][]byte{
		"one-frame":    {f},
		"primed-cache": {two[:215], two[215:]},
	} {
		t.Run(name, func(t *testing.T) {
			for _, p := range payloads {
				if _, err := codecs.Zstd.Decompress(p, 1<<20); err == nil {
					t.Fatal("codec accepted a crafted frame")
				}
				_, err := btoon.Decode(envelope(btoon.Zstd, p, 1<<20))
				wantCode(t, err, btoon.CodeInvalidEnvelope)
			}
		})
	}
}

// TestZstdSingleFrame checks that the payload must be exactly one
// Zstandard frame (SPEC §6): further frames, skippable frames and a payload
// split over two frames are rejected, as they are by btoon-core.
func TestZstdSingleFrame(t *testing.T) {
	raw, err := btoon.Marshal(sampleDoc())
	if err != nil {
		t.Fatal(err)
	}
	frame, err := codecs.Zstd.Compress(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	half, err := codecs.Zstd.Compress(raw[:len(raw)/2], 0)
	if err != nil {
		t.Fatal(err)
	}
	rest, err := codecs.Zstd.Compress(raw[len(raw)/2:], 0)
	if err != nil {
		t.Fatal(err)
	}
	// A frame with no content: single segment, content size 0, and one
	// empty raw block that is the last.
	empty := []byte{0x28, 0xb5, 0x2f, 0xfd, 0x20, 0x00, 0x01, 0x00, 0x00}
	if out, err := codecs.Zstd.Decompress(empty, 0); err != nil || len(out) != 0 {
		t.Fatalf("empty frame: %v", err)
	}
	skippable := []byte{0x50, 0x2a, 0x4d, 0x18, 0x04, 0x00, 0x00, 0x00, 0xde, 0xad, 0xbe, 0xef}
	cases := map[string][]byte{
		"empty-frame-after":     slices.Concat(frame, empty),
		"empty-frame-before":    slices.Concat(empty, frame),
		"skippable-frame-after": slices.Concat(frame, skippable),
		"skippable-frame-first": slices.Concat(skippable, frame),
		"two-halves":            slices.Concat(half, rest),
		"frame-twice":           slices.Concat(frame, frame),
		"truncated-checksum":    frame[:len(frame)-1],
		"no-frame":              raw[:64],
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := btoon.Decode(envelope(btoon.Zstd, payload, len(raw)))
			wantCode(t, err, btoon.CodeInvalidEnvelope)
		})
	}
	if _, err := btoon.Decode(envelope(btoon.Zstd, frame, len(raw))); err != nil {
		t.Fatalf("valid frame: %v", err)
	}
}
