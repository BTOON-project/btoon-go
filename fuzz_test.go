package btoon_test

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	btoon "github.com/BTOON-project/btoon-go"
)

var knownCodes = map[string]bool{
	btoon.CodeTruncated: true, btoon.CodeInvalidMarker: true, btoon.CodeTrailingData: true,
	btoon.CodeInvalidUTF8: true, btoon.CodeNonStringKey: true, btoon.CodeDuplicateKey: true,
	btoon.CodeMaxDepth: true, btoon.CodeInvalidExtension: true, btoon.CodeInvalidTabular: true,
	btoon.CodeInvalidEnvelope: true, btoon.CodeSizeLimit: true, btoon.CodeUnsupportedCompression: true,
	btoon.CodeAuthenticationFailed: true,
}

// FuzzDecode checks that decoding never panics, that every failure is a
// *btoon.Error with a SPEC §7 code, and that decode→encode→decode is stable:
// the re-encoding decodes to an equal value, and encoding that value again
// reproduces the same (canonical) bytes.
func FuzzDecode(f *testing.F) {
	for _, v := range loadVectors(f).Vectors {
		f.Add(v.bytes(f))
	}
	key := []byte("btoon-conformance-key-0123456789")
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, opts := range []btoon.DecodeOptions{{}, {PreserveTimeTypes: true}} {
			v, err := btoon.DecodeWithOptions(data, opts)
			if err != nil {
				var be *btoon.Error
				if !errors.As(err, &be) || !knownCodes[be.Code] {
					t.Fatalf("unexpected error %v", err)
				}
				continue
			}
			var viaUnmarshal any
			if err := btoon.UnmarshalWithOptions(data, &viaUnmarshal, opts); err != nil || !equalValues(v, viaUnmarshal) {
				t.Fatalf("Unmarshal into any disagrees with Decode: %v", err)
			}
			eo := btoon.EncodeOptions{PreserveTimeOffset: true}
			enc, err := btoon.MarshalWithOptions(v, eo)
			if err != nil {
				t.Fatalf("re-encoding %#v: %v", v, err)
			}
			v2, err := btoon.DecodeWithOptions(enc, opts)
			if err != nil {
				t.Fatalf("decoding the re-encoding %x: %v", enc, err)
			}
			if !equalValues(v, v2) {
				t.Fatalf("decode(encode(v)) != v\n   v: %#v\n  v2: %#v", v, v2)
			}
			enc2, err := btoon.MarshalWithOptions(v2, eo)
			if err != nil || !bytes.Equal(enc, enc2) {
				t.Fatalf("encoding is not a fixed point: %x vs %x (%v)", enc, enc2, err)
			}
		}
		// Other modes must not panic either.
		_, _ = btoon.DecodeWithOptions(data, btoon.DecodeOptions{NonStrict: true})
		_, _ = btoon.DecodeWithOptions(data, btoon.DecodeOptions{HMACKey: key})
		dec := btoon.NewDecoder(bytes.NewReader(data))
		for i := 0; i < 8; i++ {
			var x any
			if dec.Decode(&x) != nil {
				break
			}
		}
	})
}

// equalValues compares decoded values semantically: integers by value
// across int64/uint64/*big.Int, NaN equal to NaN, -0.0 distinct from 0.0,
// times by instant and offset.
func equalValues(a, b any) bool {
	if ia, ib := toBig(a), toBig(b); ia != nil || ib != nil {
		return ia != nil && ib != nil && ia.Cmp(ib) == 0
	}
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool, string, btoon.Timestamp, btoon.Date, btoon.DateTime:
		return a == b
	case float64:
		y, ok := b.(float64)
		return ok && (math.IsNaN(x) && math.IsNaN(y) || math.Float64bits(x) == math.Float64bits(y))
	case []byte:
		y, ok := b.([]byte)
		return ok && bytes.Equal(x, y)
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !equalValues(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, xv := range x {
			yv, ok := y[k]
			if !ok || !equalValues(xv, yv) {
				return false
			}
		}
		return true
	case time.Time:
		y, ok := b.(time.Time)
		if !ok || !x.Equal(y) || (x.Location() == time.UTC) != (y.Location() == time.UTC) {
			return false
		}
		_, ox := x.Zone()
		_, oy := y.Zone()
		return ox == oy
	case []float32:
		y, ok := b.([]float32)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if math.Float32bits(x[i]) != math.Float32bits(y[i]) {
				return false
			}
		}
		return true
	case btoon.VectorF64:
		y, ok := b.(btoon.VectorF64)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if math.Float64bits(x[i]) != math.Float64bits(y[i]) {
				return false
			}
		}
		return true
	case btoon.Ext:
		y, ok := b.(btoon.Ext)
		return ok && x.Type == y.Type && bytes.Equal(x.Data, y.Data)
	}
	panic(fmt.Sprintf("equalValues: unexpected decoded type %T", a))
}
