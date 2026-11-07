package codecs_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"

	btoon "github.com/BTOON-project/btoon-go"
	"github.com/BTOON-project/btoon-go/codecs"
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

// TestRequiresVectors decodes the conformance vectors that need an optional
// codec. They hold the same document as compression/zlib, which the root
// package's conformance test checks against its typed value.
func TestRequiresVectors(t *testing.T) {
	raw, err := os.ReadFile("../testdata/btoon-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Vectors []struct {
			ID       string `json:"id"`
			Hex      string `json:"hex"`
			Requires string `json:"requires"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	decode := func(h string) any {
		t.Helper()
		b, err := hex.DecodeString(h)
		if err != nil {
			t.Fatal(err)
		}
		v, err := btoon.Decode(b)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		return v
	}
	var want any
	for _, v := range f.Vectors {
		if v.ID == "compression/zlib" {
			want = decode(v.Hex)
		}
	}
	if want == nil {
		t.Fatal("compression/zlib vector not found")
	}
	n := 0
	for _, v := range f.Vectors {
		if v.Requires == "" {
			continue
		}
		t.Run(v.ID, func(t *testing.T) {
			if got := decode(v.Hex); !reflect.DeepEqual(got, want) {
				t.Fatalf("decoded %#v, want %#v", got, want)
			}
		})
		n++
	}
	if n != 3 {
		t.Fatalf("found %d vectors with requires, want 3", n)
	}
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
				"lying-header":       envelope(c.algo, payload[:8], 200<<20),
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
