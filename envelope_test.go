package btoon_test

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"

	btoon "github.com/BTOON-project/btoon-go"
)

func bigDoc() map[string]any {
	rows := make([]any, 100)
	for i := range rows {
		rows[i] = map[string]any{"id": int64(i), "name": fmt.Sprintf("user%d", i), "email": fmt.Sprintf("user%d@example.com", i)}
	}
	return map[string]any{"rows": rows}
}

func zlibBytes(t testing.TB, data []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	w.Write(data)
	w.Close()
	return buf.Bytes()
}

func compressionEnvelope(algo btoon.Compression, payload []byte, usize int) []byte {
	out := append([]byte("BTON"), 1, byte(algo), 0, 0)
	out = binary.BigEndian.AppendUint32(out, uint32(len(payload)))
	out = binary.BigEndian.AppendUint32(out, uint32(usize))
	return append(out, payload...)
}

func TestCompressionRoundTrip(t *testing.T) {
	doc := bigDoc()
	raw := mustMarshal(t, doc)
	for _, level := range []int{0, 1, 9} {
		out, err := btoon.MarshalWithOptions(doc, btoon.EncodeOptions{Compress: true, CompressionLevel: level})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(out, []byte("BTON\x01\x00\x00\x00")) || len(out) >= len(raw) {
			t.Fatalf("level %d: not a smaller zlib envelope: % x", level, out[:16])
		}
		if binary.BigEndian.Uint32(out[8:]) != uint32(len(out)-16) || binary.BigEndian.Uint32(out[12:]) != uint32(len(raw)) {
			t.Fatal("bad sizes in the envelope header")
		}
		got, err := btoon.Decode(out)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := btoon.Decode(raw)
		if !reflect.DeepEqual(got, want) {
			t.Fatal("round trip changed the document")
		}
	}
	_, err := btoon.MarshalWithOptions(doc, btoon.EncodeOptions{Compress: true, CompressionLevel: 42})
	wantErrCode(t, err, btoon.CodeInvalidArgument)
}

func TestCompressionIsSkippedWhenNotWorthwhile(t *testing.T) {
	small := strings.Repeat("a", 200) // compressible but under 256 bytes
	if out, _ := btoon.MarshalWithOptions(small, btoon.EncodeOptions{Compress: true}); bytes.HasPrefix(out, []byte("BTON")) {
		t.Fatal("compressed a document under 256 bytes")
	}
	random := make([]byte, 4096)
	rand.New(rand.NewSource(1)).Read(random)
	out, _ := btoon.MarshalWithOptions(random, btoon.EncodeOptions{Compress: true})
	if bytes.HasPrefix(out, []byte("BTON")) {
		t.Fatal("wrapped incompressible data")
	}
	if !bytes.Equal(out, mustMarshal(t, random)) {
		t.Fatal("output is not the plain encoding")
	}
}

func TestUnsupportedCompression(t *testing.T) {
	// No optional codec is registered in this package's tests.
	for _, c := range []btoon.Compression{btoon.LZ4, btoon.Zstd, btoon.Brotli} {
		_, err := btoon.MarshalWithOptions("tiny", btoon.EncodeOptions{Compress: true, Compression: c})
		wantErrCode(t, err, btoon.CodeUnsupportedCompression)
		_, err = btoon.Decode(compressionEnvelope(c, []byte{1, 2, 3}, 10))
		wantErrCode(t, err, btoon.CodeUnsupportedCompression)
	}
	_, err := btoon.MarshalWithOptions("tiny", btoon.EncodeOptions{Compress: true, Compression: 9})
	wantErrCode(t, err, btoon.CodeUnsupportedCompression)
	if btoon.Compression(9).String() != "compression(9)" || btoon.Zstd.String() != "zstd" {
		t.Fatal("Compression.String")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("RegisterCodec accepted an unknown algorithm")
		}
	}()
	btoon.RegisterCodec(9, zlibAsSnappy{})
}

// zlibAsSnappy is a test codec: zlib, registered under another id.
type zlibAsSnappy struct{}

func (zlibAsSnappy) Compress(src []byte, _ int) ([]byte, error) {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	w.Write(src)
	w.Close()
	return buf.Bytes(), nil
}

func (zlibAsSnappy) Decompress(src []byte, size int) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	return btoon.ReadDecompressed(r, size)
}

// liar returns a fixed result regardless of its input.
type liar struct{ out []byte }

func (l liar) Compress(src []byte, _ int) ([]byte, error)   { return []byte{0}, nil }
func (l liar) Decompress(src []byte, _ int) ([]byte, error) { return l.out, nil }

func TestRegisteredCodec(t *testing.T) {
	restore := btoon.SwapCodecForTest(btoon.Snappy, zlibAsSnappy{})
	defer restore()
	doc := bigDoc()
	out, err := btoon.MarshalWithOptions(doc, btoon.EncodeOptions{Compress: true, Compression: btoon.Snappy})
	if err != nil {
		t.Fatal(err)
	}
	if out[5] != byte(btoon.Snappy) {
		t.Fatalf("algorithm byte %d", out[5])
	}
	if _, err := btoon.Decode(out); err != nil {
		t.Fatal(err)
	}
	// The decoder checks the codec's output length itself.
	btoon.SwapCodecForTest(btoon.Snappy, liar{out: []byte{1, 2}})
	_, err = btoon.Decode(out)
	wantErrCode(t, err, btoon.CodeInvalidEnvelope)
}

// panicker and indexer are test codecs with the bug class of GO-2026-6865:
// they panic, with a value or with a runtime error.
type panicker struct{ value any }

func (p panicker) Compress([]byte, int) ([]byte, error)   { panic(p.value) }
func (p panicker) Decompress([]byte, int) ([]byte, error) { panic(p.value) }

type indexer struct{}

func (indexer) Compress(src []byte, _ int) ([]byte, error)   { return []byte{src[len(src)]}, nil }
func (indexer) Decompress(src []byte, _ int) ([]byte, error) { return []byte{src[len(src)]}, nil }

// TestCodecPanic checks that a panicking codec makes Marshal and Decode
// fail instead of crashing the program.
func TestCodecPanic(t *testing.T) {
	doc := bigDoc()
	restore := btoon.SwapCodecForTest(btoon.Snappy, zlibAsSnappy{})
	defer restore()
	env, err := btoon.MarshalWithOptions(doc, btoon.EncodeOptions{Compress: true, Compression: btoon.Snappy})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []btoon.Codec{panicker{"codec bug"}, panicker{errors.New("codec bug")}, panicker{42}, indexer{}} {
		btoon.SwapCodecForTest(btoon.Snappy, c)
		_, err := btoon.MarshalWithOptions(doc, btoon.EncodeOptions{Compress: true, Compression: btoon.Snappy})
		wantErrCode(t, err, btoon.CodeInvalidArgument)
		if !strings.Contains(err.Error(), "codec panicked") {
			t.Errorf("Marshal error %q", err)
		}
		_, err = btoon.Decode(env)
		wantErrCode(t, err, btoon.CodeInvalidEnvelope)
		if !strings.Contains(err.Error(), "codec panicked") {
			t.Errorf("Decode error %q", err)
		}
	}
}

func TestReadDecompressed(t *testing.T) {
	data := bytes.Repeat([]byte("btoon"), 50000)
	z := zlibBytes(t, data)
	reader := func() io.Reader {
		r, err := zlib.NewReader(bytes.NewReader(z))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if got, err := btoon.ReadDecompressed(reader(), len(data)); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("exact: %v", err)
	}
	if got, err := btoon.ReadDecompressed(iotest.OneByteReader(reader()), len(data)); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("one byte at a time: %v", err)
	}
	if got, err := btoon.ReadDecompressed(&dataWithEOF{data: data}, len(data)); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("data with EOF: %v", err)
	}
	_, err := btoon.ReadDecompressed(reader(), len(data)-1)
	wantErrCode(t, err, btoon.CodeInvalidEnvelope)
	_, err = btoon.ReadDecompressed(reader(), len(data)+1)
	wantErrCode(t, err, btoon.CodeInvalidEnvelope)
	if _, err := btoon.ReadDecompressed(iotest.TimeoutReader(reader()), len(data)); !errors.Is(err, iotest.ErrTimeout) {
		t.Fatalf("reader errors are passed through: %v", err)
	}
}

func TestDecompressionBomb(t *testing.T) {
	// 64 MiB of zeros compress to about 64 KiB.
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	io.CopyN(w, zeros{}, 64<<20)
	w.Close()
	for _, c := range []struct {
		declared int
		limit    int
		code     string
	}{
		{1000, 0, btoon.CodeInvalidEnvelope},    // claims 1000 bytes, produces 64 MiB
		{1 << 20, 0, btoon.CodeInvalidEnvelope}, // claims 1 MiB
		{64 << 20, 1 << 20, btoon.CodeSizeLimit},
	} {
		env := compressionEnvelope(btoon.Zlib, buf.Bytes(), c.declared)
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := btoon.DecodeWithOptions(env, btoon.DecodeOptions{MaxDecompressedSize: c.limit})
		runtime.ReadMemStats(&after)
		wantErrCode(t, err, c.code)
		if alloc := after.TotalAlloc - before.TotalAlloc; alloc > uint64(c.declared)*2+1<<20 {
			t.Errorf("declared %d: allocated %d bytes", c.declared, alloc)
		}
	}
}

// dataWithEOF returns its last bytes together with io.EOF.
type dataWithEOF struct{ data []byte }

func (r *dataWithEOF) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, io.EOF
	}
	return n, nil
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestEnvelopeOptions(t *testing.T) {
	out, _ := btoon.MarshalWithOptions(bigDoc(), btoon.EncodeOptions{Compress: true})
	_, err := btoon.DecodeWithOptions(out, btoon.DecodeOptions{DisableDecompression: true})
	wantErrCode(t, err, btoon.CodeInvalidEnvelope)
	_, err = btoon.DecodeWithOptions(out, btoon.DecodeOptions{MaxDecompressedSize: 100})
	wantErrCode(t, err, btoon.CodeSizeLimit)
	_, err = btoon.DecodeWithOptions(out, btoon.DecodeOptions{MaxDecompressedSize: -1})
	wantErrCode(t, err, btoon.CodeInvalidArgument)

	// The magic is only an envelope from 16 bytes on; shorter inputs are
	// bare documents (0x42 is the integer 66).
	short := []byte("BTON\x01\x00\x00\x00\x00\x00\x00")
	_, err = btoon.Decode(short)
	wantErrCode(t, err, btoon.CodeTrailingData)
	if v, err := btoon.DecodeWithOptions(short, btoon.DecodeOptions{NonStrict: true}); err != nil || v != int64(66) {
		t.Fatalf("got %v %v", v, err)
	}
	// An envelope is never reinterpreted as a bare document, not even in
	// non-strict mode.
	bad := compressionEnvelope(btoon.Zlib, []byte("garbage"), 5)
	_, err = btoon.DecodeWithOptions(bad, btoon.DecodeOptions{NonStrict: true})
	wantErrCode(t, err, btoon.CodeInvalidEnvelope)
}

func TestSignedEnvelope(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	doc := map[string]any{"a": int64(1)}
	signed, err := btoon.MarshalWithOptions(doc, btoon.EncodeOptions{HMACKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(signed, []byte("BTSG\x01\x01\x00\x00")) || len(signed) != 40+4 {
		t.Fatalf("got % x", signed)
	}
	if v, err := btoon.DecodeWithOptions(signed, btoon.DecodeOptions{HMACKey: key}); err != nil || !reflect.DeepEqual(v, doc) {
		t.Fatalf("got %v %v", v, err)
	}
	var into map[string]int
	if err := btoon.UnmarshalWithOptions(signed, &into, btoon.DecodeOptions{HMACKey: key}); err != nil || into["a"] != 1 {
		t.Fatalf("got %v %v", into, err)
	}

	_, err = btoon.Decode(signed)
	wantErrCode(t, err, btoon.CodeAuthenticationFailed)
	_, err = btoon.DecodeWithOptions(signed, btoon.DecodeOptions{HMACKey: []byte("another key, also 32 bytes long!")})
	wantErrCode(t, err, btoon.CodeAuthenticationFailed)
	_, err = btoon.DecodeWithOptions(mustMarshal(t, doc), btoon.DecodeOptions{HMACKey: key})
	wantErrCode(t, err, btoon.CodeAuthenticationFailed)
	for i := range signed { // every flipped bit is detected
		tampered := bytes.Clone(signed)
		tampered[i] ^= 0x10
		_, err := btoon.DecodeWithOptions(tampered, btoon.DecodeOptions{HMACKey: key})
		if err == nil {
			t.Fatalf("byte %d: tampering not detected", i)
		}
	}
	// The header is authenticated and checked.
	v2 := bytes.Clone(signed)
	v2[4] = 2
	_, err = btoon.DecodeWithOptions(v2, btoon.DecodeOptions{HMACKey: key})
	wantErrCode(t, err, btoon.CodeInvalidEnvelope)
	_, err = btoon.DecodeWithOptions(signed[:39], btoon.DecodeOptions{HMACKey: key})
	wantErrCode(t, err, btoon.CodeInvalidEnvelope)

	// An empty, non-nil key is a configuration error, not "no key" (SPEC §8):
	// every entry point rejects it, even where the input isn't signed.
	_, err = btoon.MarshalWithOptions(doc, btoon.EncodeOptions{HMACKey: []byte{}})
	wantErrCode(t, err, btoon.CodeInvalidArgument)
	_, err = btoon.MarshalWithOptions(doc, btoon.EncodeOptions{HMACKey: []byte(""), Compress: true})
	wantErrCode(t, err, btoon.CodeInvalidArgument)
	for _, in := range [][]byte{signed, mustMarshal(t, doc)} {
		_, err = btoon.DecodeWithOptions(in, btoon.DecodeOptions{HMACKey: []byte{}})
		wantErrCode(t, err, btoon.CodeInvalidArgument)
		var m map[string]any
		err = btoon.UnmarshalWithOptions(in, &m, btoon.DecodeOptions{HMACKey: []byte{}})
		wantErrCode(t, err, btoon.CodeInvalidArgument)
	}
	// "No key" is a nil slice.
	if v, err := btoon.DecodeWithOptions(mustMarshal(t, doc), btoon.DecodeOptions{HMACKey: nil}); err != nil || !reflect.DeepEqual(v, doc) {
		t.Fatalf("nil key: %v %v", v, err)
	}

	// Signed and compressed: sign(compress(encode(v))).
	both, err := btoon.MarshalWithOptions(bigDoc(), btoon.EncodeOptions{HMACKey: key, Compress: true})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(both, []byte("BTSG")) || !bytes.HasPrefix(both[40:], []byte("BTON")) {
		t.Fatal("wrong envelope layering")
	}
	if _, err := btoon.DecodeWithOptions(both, btoon.DecodeOptions{HMACKey: key}); err != nil {
		t.Fatal(err)
	}
}

// RFC 1950 lets a zlib stream ask for a preset dictionary (FLG.FDICT). BTOON
// has no way to supply one, so such a payload is invalid_envelope. Go's
// compress/zlib accepted one whose DICTID is the Adler-32 of an empty
// dictionary, while every other implementation rejected it (review X2).
func TestZlibPresetDictionaryIsRejected(t *testing.T) {
	// FLG = 0xbb (FDICT set), DICTID = 1, then the deflate stream of "hello".
	_, err := btoon.Decode(fromHex(t, "42544f4e01000000000000120000000678bb000000015b9a919a93930f000a0b02ba"))
	wantErrCode(t, err, btoon.CodeInvalidEnvelope)
	// The same stream without FDICT and DICTID.
	if v, err := btoon.Decode(fromHex(t, "42544f4e010000000000000e00000006789c5b9a919a93930f000a0b02ba")); err != nil || v != "hello" {
		t.Fatalf("got %v %v", v, err)
	}
}
