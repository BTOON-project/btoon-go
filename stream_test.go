package btoon_test

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"

	btoon "github.com/BTOON-project/btoon-go"
)

func TestStreamRoundTrip(t *testing.T) {
	values := []any{
		int64(66), // 0x42, the first byte of both envelope magics
		"BTON",
		map[string]any{"a": []any{int64(1), nil, true}},
		strings.Repeat("x", 100_000),
		bigDoc(),
		[]any{},
		nil,
	}
	var buf bytes.Buffer
	enc := btoon.NewEncoder(&buf)
	for _, v := range values {
		if err := enc.Encode(v); err != nil {
			t.Fatal(err)
		}
	}
	for name, r := range map[string]io.Reader{
		"buffered": bytes.NewReader(buf.Bytes()),
		"one-byte": iotest.OneByteReader(bytes.NewReader(buf.Bytes())),
	} {
		dec := btoon.NewDecoder(r)
		for i, want := range values {
			if !dec.More() {
				t.Fatalf("%s: More() false before document %d", name, i)
			}
			var got any
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("%s: document %d: %v", name, i, err)
			}
			wantV, _ := btoon.Decode(mustMarshal(t, want))
			if !reflect.DeepEqual(got, wantV) {
				t.Fatalf("%s: document %d: got %#v", name, i, got)
			}
		}
		if dec.More() {
			t.Fatalf("%s: More() true at the end", name)
		}
		var x any
		if err := dec.Decode(&x); err != io.EOF {
			t.Fatalf("%s: want io.EOF, got %v", name, err)
		}
	}
}

func TestStreamTypedAndErrors(t *testing.T) {
	type rec struct {
		N int `btoon:"n"`
	}
	var buf bytes.Buffer
	enc := btoon.NewEncoder(&buf)
	for i := 0; i < 3; i++ {
		enc.Encode(rec{i})
	}
	dec := btoon.NewDecoder(&buf)
	for i := 0; i < 3; i++ {
		var r rec
		if err := dec.Decode(&r); err != nil || r.N != i {
			t.Fatalf("got %+v %v", r, err)
		}
	}

	// A stream that ends inside a document.
	dec = btoon.NewDecoder(bytes.NewReader([]byte{0x01, 0x92, 0x01}))
	var x any
	if err := dec.Decode(&x); err != nil || x != int64(1) {
		t.Fatal(err)
	}
	wantErrCode(t, dec.Decode(&x), btoon.CodeTruncated)
	// It reports what Decode reports for the same bytes, which can be an
	// error before the end, or a value counted past MaxValues as the input
	// ends (SPEC §7.1).
	for _, c := range []struct {
		hex  string
		opts btoon.DecodeOptions
		code string
	}{
		{"92a1ff", btoon.DecodeOptions{}, btoon.CodeInvalidUTF8},
		{"81a161", btoon.DecodeOptions{MaxValues: 1}, btoon.CodeSizeLimit},
		{"81a161", btoon.DecodeOptions{}, btoon.CodeTruncated},
		{"a568656c", btoon.DecodeOptions{}, btoon.CodeTruncated},
		{"dc00", btoon.DecodeOptions{}, btoon.CodeTruncated},
	} {
		_, want := btoon.DecodeWithOptions(fromHex(t, c.hex), c.opts)
		wantErrCode(t, want, c.code)
		for _, r := range []io.Reader{bytes.NewReader(fromHex(t, c.hex)), iotest.OneByteReader(bytes.NewReader(fromHex(t, c.hex)))} {
			dec = btoon.NewDecoder(r)
			dec.SetOptions(c.opts)
			wantErrCode(t, dec.Decode(&x), c.code)
		}
	}

	enc.SetOptions(btoon.EncodeOptions{Compress: true})
	wantErrCode(t, enc.Encode(1), btoon.CodeInvalidArgument)
	enc.SetOptions(btoon.EncodeOptions{HMACKey: []byte("k")})
	wantErrCode(t, enc.Encode(1), btoon.CodeInvalidArgument)
	dec.SetOptions(btoon.DecodeOptions{HMACKey: []byte("k")})
	wantErrCode(t, dec.Decode(&x), btoon.CodeInvalidArgument)
	// An empty key is not "no key" either (SPEC §8).
	enc.SetOptions(btoon.EncodeOptions{HMACKey: []byte{}})
	wantErrCode(t, enc.Encode(1), btoon.CodeInvalidArgument)
	dec.SetOptions(btoon.DecodeOptions{HMACKey: []byte{}})
	wantErrCode(t, dec.Decode(&x), btoon.CodeInvalidArgument)
	wantErrCode(t, btoon.NewDecoder(&buf).Decode(x), btoon.CodeInvalidArgument)

	// A reader error is passed through.
	dec = btoon.NewDecoder(iotest.ErrReader(errors.New("disk on fire")))
	if err := dec.Decode(&x); err == nil || err.Error() != "disk on fire" {
		t.Fatalf("got %v", err)
	}
}

// A stream reports the same result as Decode for every bare conformance
// vector, with the vector's options, except that a stream document can't
// be larger than MaxDecompressedSize.
func TestStreamMatchesDecode(t *testing.T) {
	f := loadVectors(t)
	for _, v := range f.Vectors {
		data := v.Bytes(t)
		if len(data) == 0 || bytes.HasPrefix(data, []byte("BTON")) || bytes.HasPrefix(data, []byte("BTSG")) || v.Error == btoon.CodeTrailingData || v.Options.HMACKey != nil {
			continue
		}
		opts := v.DecodeOptions(t, f)
		dec := btoon.NewDecoder(bytes.NewReader(data))
		dec.SetOptions(opts)
		var got any
		err := dec.Decode(&got)
		want, wantErr := btoon.DecodeWithOptions(data, opts)
		if len(data) > opts.MaxDecompressedSize && wantErr == nil {
			wantErrCode(t, err, btoon.CodeSizeLimit)
			continue
		}
		if (err == nil) != (wantErr == nil) {
			t.Fatalf("%s: stream %v, Decode %v", v.ID, err, wantErr)
		}
		if err != nil {
			var a, b *btoon.Error
			errors.As(err, &a)
			errors.As(wantErr, &b)
			if a == nil || a.Code != b.Code {
				t.Fatalf("%s: stream %v, Decode %v", v.ID, err, wantErr)
			}
			continue
		}
		if !equalValues(got, want) {
			t.Fatalf("%s: stream %#v, Decode %#v", v.ID, got, want)
		}
		if err := dec.Decode(&got); err != io.EOF {
			t.Fatalf("%s: want io.EOF after the document, got %v", v.ID, err)
		}
	}
}

// endless serves a document that never ends, as a peer that keeps sending
// would: an array32 header announcing 2^32-1 elements, then nils. n counts
// the bytes read.
type endless struct {
	prefix []byte
	n      int
}

func (e *endless) Read(p []byte) (int, error) {
	for i := range p {
		if e.n < len(e.prefix) {
			p[i] = e.prefix[e.n]
		} else {
			p[i] = 0xc0
		}
		e.n++
	}
	return len(p), nil
}

// Each document of a stream is bounded by MaxDecompressedSize and MaxValues,
// as in btoon-core's StreamDecoder. The Decoder used to buffer a document of
// any size before decoding it (review 2026-11-13).
func TestStreamDocumentLimits(t *testing.T) {
	const slack = 128 << 10 // what the Decoder may read ahead
	var x any

	// The byte limit, with a value limit that doesn't apply first.
	src := &endless{prefix: fromHex(t, "ddffffffff")}
	dec := btoon.NewDecoder(src)
	dec.SetOptions(btoon.DecodeOptions{MaxDecompressedSize: 1 << 20, MaxValues: 1 << 30})
	wantErrCode(t, dec.Decode(&x), btoon.CodeSizeLimit)
	if src.n > 1<<20+slack {
		t.Fatalf("read %d bytes for a 1 MiB limit", src.n)
	}
	// The Decoder can't tell where the next document starts, so the error
	// sticks, and it reads no more.
	read := src.n
	wantErrCode(t, dec.Decode(&x), btoon.CodeSizeLimit)
	if src.n != read {
		t.Fatalf("read on after the error")
	}

	// With the defaults, the value limit stops it after about 2^21 nils.
	src = &endless{prefix: fromHex(t, "ddffffffff")}
	dec = btoon.NewDecoder(src)
	wantErrCode(t, dec.Decode(&x), btoon.CodeSizeLimit)
	if src.n > 2<<20+slack {
		t.Fatalf("read %d bytes for 2^20 values", src.n)
	}

	// A string longer than the limit fails before its bytes are buffered.
	src = &endless{prefix: fromHex(t, "db00200000")} // 2 MiB
	dec = btoon.NewDecoder(src)
	dec.SetOptions(btoon.DecodeOptions{MaxDecompressedSize: 1 << 20})
	wantErrCode(t, dec.Decode(&x), btoon.CodeSizeLimit)
	if src.n > 1<<20+slack {
		t.Fatalf("read %d bytes for a 1 MiB limit", src.n)
	}

	// An error that Decode finds in the bytes before the limit is reported
	// instead: here an invalid string, then a string of 2 MiB.
	src = &endless{prefix: fromHex(t, "92a1ffdb00200000")}
	dec = btoon.NewDecoder(src)
	dec.SetOptions(btoon.DecodeOptions{MaxDecompressedSize: 1 << 10})
	wantErrCode(t, dec.Decode(&x), btoon.CodeInvalidUTF8)

	// A document of exactly the limit decodes, and so does the next one. A
	// stream that ends inside a document over the limit is truncated, as for
	// Decode.
	doc := mustMarshal(t, strings.Repeat("x", 1000))
	two := append(append([]byte{}, doc...), 0x2a)
	dec = btoon.NewDecoder(bytes.NewReader(two))
	dec.SetOptions(btoon.DecodeOptions{MaxDecompressedSize: len(doc)})
	if err := dec.Decode(&x); err != nil || x != strings.Repeat("x", 1000) {
		t.Fatalf("document at the limit: %v", err)
	}
	if err := dec.Decode(&x); err != nil || x != int64(42) {
		t.Fatalf("next document: %v %v", x, err)
	}
	dec = btoon.NewDecoder(bytes.NewReader(doc[:len(doc)-1]))
	dec.SetOptions(btoon.DecodeOptions{MaxDecompressedSize: len(doc) - 1})
	wantErrCode(t, dec.Decode(&x), btoon.CodeTruncated)
	dec = btoon.NewDecoder(bytes.NewReader(doc))
	dec.SetOptions(btoon.DecodeOptions{MaxDecompressedSize: len(doc) - 1})
	wantErrCode(t, dec.Decode(&x), btoon.CodeSizeLimit)

	// A map's keys don't count as values: 600,000 entries are 1.2 million
	// items but 600,001 values, under the default limit.
	m := make(map[string]any, 600_000)
	for i := range 600_000 {
		m[strconv.Itoa(i)] = nil
	}
	dec = btoon.NewDecoder(bytes.NewReader(mustMarshal(t, m)))
	if err := dec.Decode(&x); err != nil {
		t.Fatalf("map of 600,000 entries: %v", err)
	}
}

// flaky returns err once, after the first n bytes of data.
type flaky struct {
	data []byte
	n    int
	err  error
}

func (f *flaky) Read(p []byte) (int, error) {
	if f.n == 0 && f.err != nil {
		err := f.err
		f.err = nil
		return 0, err
	}
	if len(f.data) == 0 {
		return 0, io.EOF
	}
	k := len(p)
	if f.err != nil {
		k = min(k, f.n)
	}
	k = copy(p, f.data[:min(k, len(f.data))])
	f.data, f.n = f.data[k:], f.n-k
	return k, nil
}

// A reader error between documents can be retried, but one inside a
// document leaves the Decoder without its place in the stream, so it
// returns that error from then on.
func TestStreamReaderErrors(t *testing.T) {
	timeout := errors.New("timeout")
	var x any
	dec := btoon.NewDecoder(&flaky{data: fromHex(t, "0102"), n: 1, err: timeout})
	if err := dec.Decode(&x); err != nil || x != int64(1) {
		t.Fatalf("first document: %v %v", x, err)
	}
	if err := dec.Decode(&x); err != timeout {
		t.Fatalf("want the reader's error, got %v", err)
	}
	if err := dec.Decode(&x); err != nil || x != int64(2) {
		t.Fatalf("after a retry: %v %v", x, err)
	}

	dec = btoon.NewDecoder(&flaky{data: fromHex(t, "92010203"), n: 2, err: timeout})
	for range 2 {
		if err := dec.Decode(&x); err != timeout {
			t.Fatalf("want the reader's error, got %v", err)
		}
	}
}
