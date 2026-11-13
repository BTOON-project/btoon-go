package btoon_test

import (
	"bytes"
	"errors"
	"io"
	"reflect"
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
// vector, with the vector's options.
func TestStreamMatchesDecode(t *testing.T) {
	f := loadVectors(t)
	for _, v := range f.Vectors {
		data := v.bytes(t)
		if len(data) == 0 || bytes.HasPrefix(data, []byte("BTON")) || bytes.HasPrefix(data, []byte("BTSG")) || v.Error == btoon.CodeTrailingData || v.Options.HMACKey != nil {
			continue
		}
		opts := v.decodeOptions(t, f)
		dec := btoon.NewDecoder(bytes.NewReader(data))
		dec.SetOptions(opts)
		var got any
		err := dec.Decode(&got)
		want, wantErr := btoon.DecodeWithOptions(data, opts)
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
