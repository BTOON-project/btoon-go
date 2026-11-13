package btoon_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	btoon "github.com/BTOON-project/btoon-go"
)

// The conformance vectors are a verbatim copy of btoon-core's
// tests/vectors/btoon-vectors.json (run `make sync-vectors`). SPEC §10
// describes the format.

type vectorFile struct {
	Spec     string `json:"spec"`
	Defaults struct {
		MaxDepth            int  `json:"max_depth"`
		MaxDecompressedSize int  `json:"max_decompressed_size"`
		MaxValues           int  `json:"max_values"`
		Strict              bool `json:"strict"`
	} `json:"defaults"`
	Vectors []vector `json:"vectors"`
}

type vector struct {
	ID          string          `json:"id"`
	Hex         string          `json:"hex"`
	Value       json.RawMessage `json:"value"`
	Canonical   bool            `json:"canonical"`
	Error       string          `json:"error"`
	EncodeError string          `json:"encode_error"`
	Requires    string          `json:"requires"`
	// Options are the decoder options of SPEC §10. A missing option takes
	// the file's default; an empty hmac_key is an empty key, which is an
	// error, not "no key".
	Options struct {
		Strict              *bool   `json:"strict"`
		MaxDepth            *int    `json:"max_depth"`
		MaxDecompressedSize *int    `json:"max_decompressed_size"`
		MaxValues           *int    `json:"max_values"`
		HMACKey             *string `json:"hmac_key"`
	} `json:"options"`
}

func loadVectors(tb testing.TB) *vectorFile {
	tb.Helper()
	raw, err := os.ReadFile("testdata/btoon-vectors.json")
	if err != nil {
		tb.Fatal(err)
	}
	var f vectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		tb.Fatal(err)
	}
	return &f
}

func (v *vector) bytes(tb testing.TB) []byte {
	b, err := hex.DecodeString(v.Hex)
	if err != nil {
		tb.Fatalf("%s: bad hex: %v", v.ID, err)
	}
	return b
}

// hmacKey returns the vector's key: nil without one, and an empty, non-nil
// slice for an empty key.
func (v *vector) hmacKey(tb testing.TB) []byte {
	if v.Options.HMACKey == nil {
		return nil
	}
	k, err := hex.DecodeString(*v.Options.HMACKey)
	if err != nil {
		tb.Fatalf("%s: bad hmac_key: %v", v.ID, err)
	}
	return append([]byte{}, k...)
}

// decodeOptions maps the vector's options to DecodeOptions. Every option
// is given explicitly (the defaults were checked against the file's), so a
// vector's limits are exactly the ones the file states.
func (v *vector) decodeOptions(tb testing.TB, f *vectorFile) btoon.DecodeOptions {
	o := btoon.DecodeOptions{
		NonStrict:           !f.Defaults.Strict,
		MaxDepth:            f.Defaults.MaxDepth,
		MaxDecompressedSize: f.Defaults.MaxDecompressedSize,
		MaxValues:           f.Defaults.MaxValues,
		HMACKey:             v.hmacKey(tb),
	}
	if v.Options.Strict != nil {
		o.NonStrict = !*v.Options.Strict
	}
	if v.Options.MaxDepth != nil {
		o.MaxDepth = *v.Options.MaxDepth
	}
	if v.Options.MaxDecompressedSize != nil {
		o.MaxDecompressedSize = *v.Options.MaxDecompressedSize
	}
	if v.Options.MaxValues != nil {
		o.MaxValues = *v.Options.MaxValues
	}
	// 0 means "the default" in Go, so a limit of 0 can't be expressed.
	if o.MaxDepth == 0 || o.MaxDecompressedSize == 0 || o.MaxValues == 0 {
		tb.Fatalf("%s: a limit of 0 can't be expressed in DecodeOptions", v.ID)
	}
	return o
}

// codecAvailable reports whether the codec named by a vector's "requires"
// is registered, by probing the encoder.
func codecAvailable(name string) bool {
	algo := map[string]btoon.Compression{"zlib": btoon.Zlib, "lz4": btoon.LZ4, "zstd": btoon.Zstd, "brotli": btoon.Brotli, "snappy": btoon.Snappy}
	c, ok := algo[name]
	if !ok {
		return false
	}
	_, err := btoon.MarshalWithOptions(1, btoon.EncodeOptions{Compress: true, Compression: c})
	return err == nil
}

func TestConformanceVectors(t *testing.T) {
	f := loadVectors(t)
	if f.Spec != btoon.SpecVersion {
		t.Fatalf("vectors are for spec %q, library implements %q", f.Spec, btoon.SpecVersion)
	}
	if f.Defaults.MaxDepth != btoon.DefaultMaxDepth || f.Defaults.MaxDecompressedSize != btoon.DefaultMaxDecompressedSize ||
		f.Defaults.MaxValues != btoon.DefaultMaxValues || !f.Defaults.Strict {
		t.Fatalf("vector defaults %+v differ from the library defaults", f.Defaults)
	}

	var decoded, encoded, rejected, encodeRejected, skipped int
	for i := range f.Vectors {
		v := &f.Vectors[i]
		t.Run(v.ID, func(t *testing.T) {
			if v.Requires != "" && !codecAvailable(v.Requires) {
				skipped++
				t.Skipf("requires the %s codec", v.Requires)
			}
			if v.EncodeError != "" {
				// (d) encode_error vectors give a value that encoders must
				// refuse.
				want, err := parseTyped(v.Value)
				if err != nil {
					t.Fatalf("bad typed value: %v", err)
				}
				out, err := btoon.Marshal(buildGo(want))
				checkCode(t, err, v.EncodeError)
				if err == nil {
					t.Fatalf("encoded to %x", out)
				}
				encodeRejected++
				return
			}
			data := v.bytes(t)
			opts := v.decodeOptions(t, f)

			if v.Error != "" {
				// (c) error vectors are rejected with the matching code, both
				// by Decode and by Unmarshal.
				got, err := btoon.DecodeWithOptions(data, opts)
				checkCode(t, err, v.Error)
				if err == nil {
					t.Fatalf("decoded to %#v", got)
				}
				var x any
				checkCode(t, btoon.UnmarshalWithOptions(data, &x, opts), v.Error)
				rejected++
				return
			}

			want, err := parseTyped(v.Value)
			if err != nil {
				t.Fatalf("bad typed value: %v", err)
			}

			// (a) every non-error vector decodes to its value.
			got, err := btoon.DecodeWithOptions(data, opts)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if err := matchTyped(got, want, false); err != nil {
				t.Fatalf("decoded value: %v\n got: %#v", err, got)
			}
			var viaUnmarshal any
			if err := btoon.UnmarshalWithOptions(data, &viaUnmarshal, opts); err != nil {
				t.Fatalf("unmarshal into any: %v", err)
			}
			if err := matchTyped(viaUnmarshal, want, false); err != nil {
				t.Fatalf("unmarshaled value: %v", err)
			}
			// The same with PreserveTimeTypes, which keeps timestamp, date and
			// datetime apart.
			opts.PreserveTimeTypes = true
			got, err = btoon.DecodeWithOptions(data, opts)
			if err != nil {
				t.Fatalf("decode (PreserveTimeTypes): %v", err)
			}
			if err := matchTyped(got, want, true); err != nil {
				t.Fatalf("decoded value (PreserveTimeTypes): %v\n got: %#v", err, got)
			}
			decoded++

			// (b) canonical vectors re-encode byte for byte from the typed value.
			if v.Canonical {
				goValue := buildGo(want)
				out, err := btoon.MarshalWithOptions(goValue, btoon.EncodeOptions{HMACKey: v.hmacKey(t)})
				if err != nil {
					t.Fatalf("encode: %v", err)
				}
				if !bytes.Equal(out, data) {
					t.Fatalf("encode mismatch\n got: %x\nwant: %x", out, data)
				}
				encoded++
			}
		})
	}
	t.Logf("%d vectors: %d decoded, %d re-encoded canonically, %d rejected with the expected code, %d refused by the encoder, %d skipped (codec not registered)",
		len(f.Vectors), decoded, encoded, rejected, encodeRejected, skipped)
	if decoded+rejected+encodeRejected+skipped != len(f.Vectors) && !t.Failed() {
		t.Fatalf("accounting error")
	}
}

func checkCode(t *testing.T, err error, code string) {
	t.Helper()
	var be *btoon.Error
	if !errors.As(err, &be) {
		t.Fatalf("want *btoon.Error with code %q, got %v", code, err)
	}
	if be.Code != code {
		t.Fatalf("want code %q, got %q (%v)", code, be.Code, err)
	}
}

// --- Typed JSON form (SPEC §10) ----------------------------------------------------

type typed struct {
	kind string
	raw  json.RawMessage
}

func parseTyped(raw json.RawMessage) (typed, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return typed{}, err
	}
	if len(m) != 1 {
		return typed{}, fmt.Errorf("typed value must have exactly one key: %s", raw)
	}
	for k, v := range m {
		return typed{k, v}, nil
	}
	panic("unreachable")
}

func (tv typed) str() string {
	var s string
	if err := json.Unmarshal(tv.raw, &s); err != nil {
		panic(fmt.Sprintf("%s: %v", tv.kind, err))
	}
	return s
}

func (tv typed) bigInt() *big.Int {
	x, ok := new(big.Int).SetString(tv.str(), 10)
	if !ok {
		panic("bad integer " + tv.str())
	}
	return x
}

func (tv typed) int64() int64 {
	x := tv.bigInt()
	if !x.IsInt64() {
		panic("not an int64: " + x.String())
	}
	return x.Int64()
}

func parseFloat(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		panic(err)
	}
	return f
}

func (tv typed) strings() []string {
	var ss []string
	if err := json.Unmarshal(tv.raw, &ss); err != nil {
		panic(err)
	}
	return ss
}

// float32s and float64s parse vector elements (SPEC §10): "NaN" is the
// canonical quiet NaN of the width, "0x…" gives the exact bits (most
// significant byte first), and anything else is a decimal or ±Infinity.
func (tv typed) float32s() []float32 {
	ss := tv.strings()
	out := make([]float32, len(ss))
	for i, s := range ss {
		switch {
		case s == "NaN":
			out[i] = math.Float32frombits(0x7fc00000)
		case strings.HasPrefix(s, "0x"):
			bits, err := strconv.ParseUint(s[2:], 16, 32)
			if err != nil || len(s) != 10 {
				panic("bad float32 bits " + s)
			}
			out[i] = math.Float32frombits(uint32(bits))
		default:
			out[i] = float32(parseFloat(s))
		}
	}
	return out
}

func (tv typed) float64s() []float64 {
	ss := tv.strings()
	out := make([]float64, len(ss))
	for i, s := range ss {
		switch {
		case s == "NaN":
			out[i] = math.Float64frombits(0x7ff8000000000000)
		case strings.HasPrefix(s, "0x"):
			bits, err := strconv.ParseUint(s[2:], 16, 64)
			if err != nil || len(s) != 18 {
				panic("bad float64 bits " + s)
			}
			out[i] = math.Float64frombits(bits)
		default:
			out[i] = parseFloat(s)
		}
	}
	return out
}

func (tv typed) array() []typed {
	var items []json.RawMessage
	if err := json.Unmarshal(tv.raw, &items); err != nil {
		panic(err)
	}
	out := make([]typed, len(items))
	for i, it := range items {
		var err error
		if out[i], err = parseTyped(it); err != nil {
			panic(err)
		}
	}
	return out
}

func (tv typed) object() map[string]typed {
	var items map[string]json.RawMessage
	if err := json.Unmarshal(tv.raw, &items); err != nil {
		panic(err)
	}
	out := make(map[string]typed, len(items))
	for k, it := range items {
		var err error
		if out[k], err = parseTyped(it); err != nil {
			panic(err)
		}
	}
	return out
}

type typedTimestamp struct {
	Seconds       string `json:"seconds"`
	Nanoseconds   uint32 `json:"nanoseconds"`
	OffsetMinutes *int16 `json:"offset_minutes"`
}

func (tv typed) timestamp() btoon.Timestamp {
	var ts typedTimestamp
	if err := json.Unmarshal(tv.raw, &ts); err != nil {
		panic(err)
	}
	sec, err := strconv.ParseInt(ts.Seconds, 10, 64)
	if err != nil {
		panic(err)
	}
	out := btoon.Timestamp{Seconds: sec, Nanoseconds: ts.Nanoseconds}
	if ts.OffsetMinutes != nil {
		out.HasOffset, out.OffsetMinutes = true, *ts.OffsetMinutes
	}
	return out
}

func (tv typed) ext() btoon.Ext {
	var x struct {
		Type int8   `json:"type"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(tv.raw, &x); err != nil {
		panic(err)
	}
	data, err := hex.DecodeString(x.Data)
	if err != nil {
		panic(err)
	}
	return btoon.Ext{Type: x.Type, Data: data}
}

// buildGo turns a typed value into the idiomatic Go value to encode.
func buildGo(tv typed) any {
	switch tv.kind {
	case "nil":
		return nil
	case "bool":
		var b bool
		if err := json.Unmarshal(tv.raw, &b); err != nil {
			panic(err)
		}
		return b
	case "int":
		x := tv.bigInt()
		switch {
		case x.IsInt64():
			return x.Int64()
		case x.IsUint64():
			return x.Uint64()
		}
		return x
	case "float":
		return parseFloat(tv.str())
	case "string":
		return tv.str()
	case "binary":
		b, err := hex.DecodeString(tv.str())
		if err != nil {
			panic(err)
		}
		return b
	case "array":
		items := tv.array()
		out := make([]any, len(items))
		for i, it := range items {
			out[i] = buildGo(it)
		}
		return out
	case "map":
		items := tv.object()
		out := make(map[string]any, len(items))
		for k, it := range items {
			out[k] = buildGo(it)
		}
		return out
	case "timestamp":
		ts := tv.timestamp()
		if !ts.HasOffset && ts.Seconds > -1<<62 && ts.Seconds < 1<<62 {
			return ts.Time() // time.Time, the idiomatic type
		}
		return ts
	case "date":
		return btoon.Date(tv.int64())
	case "datetime":
		return btoon.DateTime(tv.int64())
	case "float32_vector":
		return tv.float32s()
	case "float64_vector":
		return btoon.VectorF64(tv.float64s())
	case "ext":
		return tv.ext()
	}
	panic("unknown typed kind " + tv.kind)
}

// matchTyped compares a decoded value with a typed value, checking that it
// has the Go type documented for Decode. Comparison is semantic: integers by
// value across int64/uint64/*big.Int, NaN equals NaN, -0.0 differs from 0.0,
// and maps are order-insensitive. timeTypes selects the
// DecodeOptions.PreserveTimeTypes mapping.
func matchTyped(got any, want typed, timeTypes bool) error {
	switch want.kind {
	case "nil":
		if got != nil {
			return fmt.Errorf("want nil, got %T", got)
		}
	case "bool":
		var b bool
		_ = json.Unmarshal(want.raw, &b)
		if g, ok := got.(bool); !ok || g != b {
			return fmt.Errorf("want %v, got %#v", b, got)
		}
	case "int":
		w := want.bigInt()
		var g *big.Int
		switch x := got.(type) {
		case int64:
			g = big.NewInt(x)
		case uint64:
			if x <= math.MaxInt64 {
				return fmt.Errorf("uint64 %d should be int64", x)
			}
			g = new(big.Int).SetUint64(x)
		case *big.Int:
			g = x
		default:
			return fmt.Errorf("want integer %s, got %T", w, got)
		}
		if g.Cmp(w) != 0 {
			return fmt.Errorf("want %s, got %s", w, g)
		}
	case "float":
		w := parseFloat(want.str())
		g, ok := got.(float64)
		if !ok {
			return fmt.Errorf("want float64, got %T", got)
		}
		if !sameFloat(g, w) {
			return fmt.Errorf("want %v, got %v", w, g)
		}
	case "string":
		if g, ok := got.(string); !ok || g != want.str() {
			return fmt.Errorf("want %q, got %#v", want.str(), got)
		}
	case "binary":
		w, _ := hex.DecodeString(want.str())
		g, ok := got.([]byte)
		if !ok || g == nil || !bytes.Equal(g, w) {
			return fmt.Errorf("want binary %x, got %#v", w, got)
		}
	case "array":
		items := want.array()
		g, ok := got.([]any)
		if !ok || g == nil {
			return fmt.Errorf("want []any, got %T", got)
		}
		if len(g) != len(items) {
			return fmt.Errorf("want %d elements, got %d", len(items), len(g))
		}
		for i := range items {
			if err := matchTyped(g[i], items[i], timeTypes); err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
		}
	case "map":
		items := want.object()
		g, ok := got.(map[string]any)
		if !ok || g == nil {
			return fmt.Errorf("want map[string]any, got %T", got)
		}
		if len(g) != len(items) {
			return fmt.Errorf("want %d keys, got %d", len(items), len(g))
		}
		keys := make([]string, 0, len(items))
		for k := range items {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			gv, ok := g[k]
			if !ok {
				return fmt.Errorf("missing key %q", k)
			}
			if err := matchTyped(gv, items[k], timeTypes); err != nil {
				return fmt.Errorf(".%s: %w", k, err)
			}
		}
	case "timestamp":
		w := want.timestamp()
		// With PreserveTimeTypes, and for instants past what time.Time can hold
		// (seconds since year 1 must fit in an int64), Decode returns a Timestamp.
		if timeTypes || w.Seconds > math.MaxInt64-62135596800 {
			if g, ok := got.(btoon.Timestamp); !ok || g != w {
				return fmt.Errorf("want %+v, got %#v", w, got)
			}
			return nil
		}
		g, ok := got.(time.Time)
		if !ok {
			return fmt.Errorf("want time.Time, got %T", got)
		}
		if g.Unix() != w.Seconds || uint32(g.Nanosecond()) != w.Nanoseconds {
			return fmt.Errorf("want %+v, got %v", w, g)
		}
		_, off := g.Zone()
		switch {
		case w.HasOffset && (g.Location() == time.UTC || off != int(w.OffsetMinutes)*60):
			return fmt.Errorf("want offset %d minutes, got %v", w.OffsetMinutes, g)
		case !w.HasOffset && g.Location() != time.UTC:
			return fmt.Errorf("want UTC, got %v", g)
		}
	case "date", "datetime":
		n := want.int64()
		var wt time.Time
		var wv any
		if want.kind == "date" {
			wt, wv = time.UnixMilli(n), btoon.Date(n)
		} else {
			wt, wv = time.Unix(0, n), btoon.DateTime(n)
		}
		if timeTypes {
			if got != wv {
				return fmt.Errorf("want %#v, got %#v", wv, got)
			}
			return nil
		}
		g, ok := got.(time.Time)
		if !ok || !g.Equal(wt) || g.Location() != time.UTC {
			return fmt.Errorf("want %v (UTC), got %#v", wt.UTC(), got)
		}
	case "float32_vector":
		// Vectors are compared bit for bit, NaN payloads included.
		w := want.float32s()
		g, ok := got.([]float32)
		if !ok || g == nil || len(g) != len(w) {
			return fmt.Errorf("want []float32 of %d, got %#v", len(w), got)
		}
		for i := range w {
			if math.Float32bits(g[i]) != math.Float32bits(w[i]) {
				return fmt.Errorf("[%d]: want %#08x, got %#08x", i, math.Float32bits(w[i]), math.Float32bits(g[i]))
			}
		}
	case "float64_vector":
		w := want.float64s()
		g, ok := got.(btoon.VectorF64)
		if !ok || g == nil || len(g) != len(w) {
			return fmt.Errorf("want VectorF64 of %d, got %#v", len(w), got)
		}
		for i := range w {
			if math.Float64bits(g[i]) != math.Float64bits(w[i]) {
				return fmt.Errorf("[%d]: want %#016x, got %#016x", i, math.Float64bits(w[i]), math.Float64bits(g[i]))
			}
		}
	case "ext":
		w := want.ext()
		g, ok := got.(btoon.Ext)
		if !ok || g.Type != w.Type || !bytes.Equal(g.Data, w.Data) || g.Data == nil {
			return fmt.Errorf("want %#v, got %#v", w, got)
		}
	default:
		return fmt.Errorf("unknown typed kind %q", want.kind)
	}
	return nil
}

func sameFloat(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return math.Float64bits(a) == math.Float64bits(b)
}
