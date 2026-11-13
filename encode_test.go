package btoon_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	btoon "github.com/BTOON-project/btoon-go"
)

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := btoon.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal(%#v): %v", v, err)
	}
	return b
}

func wantHex(t *testing.T, v any, want string) {
	t.Helper()
	if got := hex.EncodeToString(mustMarshal(t, v)); got != want {
		t.Fatalf("Marshal(%#v)\n got: %s\nwant: %s", v, got, want)
	}
}

func wantErrCode(t *testing.T, err error, code string) {
	t.Helper()
	var be *btoon.Error
	if !errors.As(err, &be) {
		t.Fatalf("want *btoon.Error %q, got %v", code, err)
	}
	if be.Code != code {
		t.Fatalf("want code %q, got %q: %v", code, be.Code, err)
	}
}

// Integers use the smallest format for the value, whatever the Go type
// (SPEC §4.1).
func TestIntegersIgnoreGoType(t *testing.T) {
	f := loadVectors(t)
	n := 0
	for _, v := range f.Vectors {
		if !v.Canonical || v.Error != "" {
			continue
		}
		tv, err := parseTyped(v.Value)
		if err != nil || tv.kind != "int" {
			continue
		}
		x := tv.bigInt()
		var reprs []any
		reprs = append(reprs, x, *x)
		if x.IsInt64() {
			i := x.Int64()
			reprs = append(reprs, i)
			if i >= math.MinInt32 && i <= math.MaxInt32 {
				reprs = append(reprs, int32(i), int(i))
			}
			if i >= math.MinInt16 && i <= math.MaxInt16 {
				reprs = append(reprs, int16(i))
			}
			if i >= math.MinInt8 && i <= math.MaxInt8 {
				reprs = append(reprs, int8(i))
			}
		}
		if x.Sign() >= 0 && x.IsUint64() {
			u := x.Uint64()
			reprs = append(reprs, u, uintptr(u), uint(u))
			if u <= math.MaxUint32 {
				reprs = append(reprs, uint32(u))
			}
			if u <= math.MaxUint16 {
				reprs = append(reprs, uint16(u))
			}
			if u <= math.MaxUint8 {
				reprs = append(reprs, uint8(u))
			}
		}
		type myInt int64
		if x.IsInt64() {
			reprs = append(reprs, myInt(x.Int64()))
		}
		for _, r := range reprs {
			wantHex(t, r, v.Hex)
			n++
		}
	}
	if n < 100 {
		t.Fatalf("only %d representations checked", n)
	}
}

func TestBigIntBoundaries(t *testing.T) {
	two := big.NewInt(2)
	pow := func(e int64) *big.Int { return new(big.Int).Exp(two, big.NewInt(e), nil) }
	cases := []struct {
		x    *big.Int
		want string
	}{
		{pow(63), "cf8000000000000000"},
		{new(big.Int).Sub(pow(64), big.NewInt(1)), "cfffffffffffffffff"},
		{pow(64), "c709fc010000000000000000"},
		{new(big.Int).Neg(pow(63)), "d38000000000000000"},
		{new(big.Int).Sub(new(big.Int).Neg(pow(63)), big.NewInt(1)), "c709fcff7fffffffffffffff"},
		{new(big.Int).Neg(pow(64)), "c709fcff0000000000000000"},
		{new(big.Int).Neg(new(big.Int).Add(pow(64), big.NewInt(1))), "c709fcfeffffffffffffffff"},
		{pow(71), "c70afc00800000000000000000"},
		{new(big.Int).Neg(pow(71)), "c709fc800000000000000000"},
	}
	for _, c := range cases {
		wantHex(t, c.x, c.want)
		got, err := btoon.Decode(mustMarshal(t, c.x))
		if err != nil {
			t.Fatal(err)
		}
		if g := toBig(got); g == nil || g.Cmp(c.x) != 0 {
			t.Fatalf("round trip of %s gave %#v", c.x, got)
		}
	}
}

func toBig(v any) *big.Int {
	switch x := v.(type) {
	case int64:
		return big.NewInt(x)
	case uint64:
		return new(big.Int).SetUint64(x)
	case *big.Int:
		return x
	}
	return nil
}

func TestFloatsAreFloat64WithCanonicalNaN(t *testing.T) {
	wantHex(t, math.NaN(), "cb7ff8000000000000") // Go's NaN is 0x7ff8000000000001
	wantHex(t, math.Float64frombits(0xfff0000000000123), "cb7ff8000000000000")
	wantHex(t, float32(math.NaN()), "cb7ff8000000000000")
	wantHex(t, math.Copysign(0, -1), "cb8000000000000000")
	wantHex(t, float32(1.5), "cb3ff8000000000000")
	wantHex(t, float32(0.1), "cb3fb99999a0000000") // exact float32 value, widened
	wantHex(t, math.Inf(-1), "cbfff0000000000000")
	type celsius float32
	wantHex(t, celsius(-2.5), "cbc004000000000000")
}

func TestStringsMustBeUTF8(t *testing.T) {
	_, err := btoon.Marshal("ok\xff")
	wantErrCode(t, err, btoon.CodeInvalidUTF8)
	_, err = btoon.Marshal(map[string]int{"\xc0\x80": 1})
	wantErrCode(t, err, btoon.CodeInvalidUTF8)
	wantHex(t, "é", "a2c3a9")
	type name string
	wantHex(t, name("a"), "a161")
}

func TestMapKeysSortedByUTF8Bytes(t *testing.T) {
	// U+FFFF sorts before U+1F600 in UTF-8 but after it in UTF-16. Expected
	// bytes from btoon-core's independent reference encoder (generate.py).
	wantHex(t, map[string]int{"￿": 1, "\U0001F600": 2, "a": 3}, "83a16103a3efbfbf01a4f09f988002")
	type key string
	wantHex(t, map[key]any{"b": 1, "aa": 2, "a": 3}, "83a16103a2616102a16201")
	wantHex(t, map[any]int{"b": 1, "a": 2}, "82a16102a16201")
	wantHex(t, map[string]int(nil), "c0")
	wantHex(t, map[string]int{}, "80")

	_, err := btoon.Marshal(map[int]string{1: "a"})
	wantErrCode(t, err, btoon.CodeInvalidArgument)
	_, err = btoon.Marshal(map[any]int{1: 1})
	wantErrCode(t, err, btoon.CodeNonStringKey)
	_, err = btoon.Marshal(map[any]int{"a": 1, key("a"): 2})
	wantErrCode(t, err, btoon.CodeDuplicateKey)
}

func TestBinaryAndVectors(t *testing.T) {
	wantHex(t, []byte{}, "c400")
	wantHex(t, []byte(nil), "c0")
	wantHex(t, [3]byte{1, 2, 3}, "c403010203")
	type b8 uint8
	wantHex(t, []b8{1, 2}, "c4020102")
	wantHex(t, [2]b8{1, 2}, "c4020102")

	wantHex(t, []float32{1, 2}, "d7fb0000803f00000040")
	wantHex(t, [2]float32{1, 2}, "d7fb0000803f00000040")
	type embedding []float32
	wantHex(t, embedding{1, 2}, "d7fb0000803f00000040")
	type celsius float32
	wantHex(t, []celsius{1, 2}, "d7fb0000803f00000040")
	wantHex(t, []float32{}, "c700fb")
	wantHex(t, []float32(nil), "c0")
	// Vector NaN payloads are not canonicalized (SPEC §5.5).
	wantHex(t, []float32{math.Float32frombits(0x7fc00001)}, "d6fb0100c07f")
	wantHex(t, btoon.VectorF64{1}, "d7fa000000000000f03f")
	wantHex(t, btoon.VectorF64{math.Float64frombits(0x7ff8000000000001)}, "d7fa010000000000f87f")
	// A plain []float64 is an array of floats.
	wantHex(t, []float64{1.5}, "91cb3ff8000000000000")
}

func TestExt(t *testing.T) {
	wantHex(t, btoon.Ext{Type: 1, Data: []byte{0xaa}}, "d401aa")
	wantHex(t, btoon.Ext{Type: 5}, "c70005")
	wantHex(t, btoon.Ext{Type: -7, Data: make([]byte, 16)}, "d8f9"+strings.Repeat("00", 16))
	wantHex(t, &btoon.Ext{Type: 2, Data: make([]byte, 300)}, "c8012c02"+strings.Repeat("00", 300))
	// The ids BTOON defines can't be written as raw extensions (SPEC §5.7);
	// the typed values write them.
	for _, id := range []int8{-1, -2, -3, -4, -5, -6, -10} {
		_, err := btoon.Marshal(btoon.Ext{Type: id, Data: []byte{0}})
		wantErrCode(t, err, btoon.CodeInvalidArgument)
		_, err = btoon.Marshal([]any{btoon.Ext{Type: id}})
		wantErrCode(t, err, btoon.CodeInvalidArgument)
	}
	// Every other id is written as given: those reserved for future versions
	// (-9…-7 and -128…-11), so that documents using them round-trip, and the
	// application ids 0…127.
	for id := -128; id <= 127; id++ {
		if (id >= -6 && id <= -1) || id == -10 {
			continue
		}
		x := btoon.Ext{Type: int8(id), Data: []byte{7}}
		out, err := btoon.Marshal(x)
		if err != nil || !bytes.Equal(out, []byte{0xd4, byte(int8(id)), 7}) {
			t.Fatalf("id %d: %x %v", id, out, err)
		}
		if v, err := btoon.Decode(out); err != nil || !reflect.DeepEqual(v, x) {
			t.Fatalf("id %d: decoded %#v %v", id, v, err)
		}
	}
}

func TestUnsupportedTypes(t *testing.T) {
	for _, v := range []any{make(chan int), func() {}, complex(1, 2), struct{ C chan int }{make(chan int)}} {
		_, err := btoon.Marshal(v)
		wantErrCode(t, err, btoon.CodeInvalidArgument)
	}
}

type ipLike struct{ a, b byte }

func (p ipLike) MarshalBinary() ([]byte, error) { return []byte{p.a, p.b}, nil }

type failing struct{}

func (failing) MarshalBinary() ([]byte, error) { return nil, errors.New("boom") }

type ptrMarshaler struct{ x byte }

func (p *ptrMarshaler) MarshalBinary() ([]byte, error) { return []byte{p.x}, nil }

func TestBinaryMarshaler(t *testing.T) {
	wantHex(t, ipLike{1, 2}, "c4020102")
	wantHex(t, []any{ipLike{1, 2}}, "91c4020102")
	wantHex(t, &ptrMarshaler{7}, "c40107")
	wantHex(t, (*ptrMarshaler)(nil), "c0")
	// Addressable values use pointer-receiver methods, like encoding/json.
	wantHex(t, &struct{ P ptrMarshaler }{ptrMarshaler{9}}, "81a150c40109")
	_, err := btoon.Marshal(failing{})
	wantErrCode(t, err, btoon.CodeInvalidArgument)
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error does not wrap the cause: %v", err)
	}
}

// Tabular cases beyond the conformance vectors (other row counts, a bigint
// cell, nil/float/binary first-row type codes). The expected bytes come from
// btoon-core's independent reference encoder (tests/vectors/generate.py).
func TestTabularCrossChecked(t *testing.T) {
	ts := btoon.Timestamp{}
	row := map[string]any{"n": nil, "b": true, "i": -5, "u": uint64(1 << 63), "f": 1.5, "s": "x",
		"bin": []byte{1}, "arr": []any{1}, "m": map[string]any{"a": 1}, "ts": ts}
	rows := func(n int, f func(i int) any) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = f(i)
		}
		return out
	}
	twoTo70 := new(big.Int).Lsh(big.NewInt(1), 70)
	cases := map[string]struct {
		v    any
		want string
	}{
		"all ten type codes": {rows(5, func(int) any { return row }),
			"c80133f6000000010000000a000000050000000361727200000001620000000362696e00000001660000000169000000016d000000016e00000001730000000274730000000175070106040208000509030000000a9101910191019101910100000005c3c3c3c3c30000000fc40101c40101c40101c40101c401010000002dcb3ff8000000000000cb3ff8000000000000cb3ff8000000000000cb3ff8000000000000cb3ff800000000000000000005fbfbfbfbfb0000001481a1610181a1610181a1610181a1610181a1610100000005c0c0c0c0c00000000aa178a178a178a178a1780000001ed6ff00000000d6ff00000000d6ff00000000d6ff00000000d6ff000000000000002dcf8000000000000000cf8000000000000000cf8000000000000000cf8000000000000000cf8000000000000000"},
		"bigint cell is code 9": {rows(8, func(i int) any {
			return map[string]any{"big": new(big.Int).Add(twoTo70, big.NewInt(int64(i))), "k": map[string]any{"z": i}}
		}), "c7a2f600000001000000020000000800000003626967000000016b090800000060c709fc400000000000000000c709fc400000000000000001c709fc400000000000000002c709fc400000000000000003c709fc400000000000000004c709fc400000000000000005c709fc400000000000000006c709fc4000000000000000070000002081a17a0081a17a0181a17a0281a17a0381a17a0481a17a0581a17a0681a17a07"},
		"UTF-8 column order": {rows(7, func(i int) any { return map[string]int{"é": i, "z": i, "Z": i} }),
			"c740f6000000010000000300000007000000015a000000017a00000002c3a9020202000000070001020304050600000007000102030405060000000700010203040506"},
		"nested cells": {rows(8, func(i int) any {
			var v any = "s"
			if i%2 == 1 {
				v = nil
			}
			return map[string]any{"k": []any{i, map[string]any{"x": i}}, "v": v}
		}), "c75cf6000000010000000200000008000000016b0000000176070500000030920081a17800920181a17801920281a17802920381a17803920481a17804920581a17805920681a17806920781a178070000000ca173c0a173c0a173c0a173c0"},
		"tabular inside tabular": {rows(6, func(g int) any {
			return map[string]any{"g": g, "rows": rows(10, func(i int) any { return map[string]any{"n": i} })}
		}), "c7fbf6000000010000000200000006000000016700000004726f7773020700000006000102030405000000d2c720f600000001000000010000000a000000016e020000000a00010203040506070809c720f600000001000000010000000a000000016e020000000a00010203040506070809c720f600000001000000010000000a000000016e020000000a00010203040506070809c720f600000001000000010000000a000000016e020000000a00010203040506070809c720f600000001000000010000000a000000016e020000000a00010203040506070809c720f600000001000000010000000a000000016e020000000a00010203040506070809"},
		"nil, float and binary first-row codes": {rows(8, func(i int) any {
			var a any
			if i > 0 {
				a = i
			}
			return map[string]any{"a": a, "b": 0.5, "c": []byte{byte(i)}}
		}), "c792f600000001000000030000000800000001610000000162000000016300040600000008c00102030405060700000048cb3fe0000000000000cb3fe0000000000000cb3fe0000000000000cb3fe0000000000000cb3fe0000000000000cb3fe0000000000000cb3fe0000000000000cb3fe000000000000000000018c40100c40101c40102c40103c40104c40105c40106c40107"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			wantHex(t, c.v, c.want)
			// And the tabular form decodes back to the same rows.
			b, _ := hex.DecodeString(c.want)
			got, err := btoon.Decode(b)
			if err != nil {
				t.Fatal(err)
			}
			if again := hex.EncodeToString(mustMarshal(t, got)); again != c.want {
				t.Fatalf("decode/encode round trip changed the bytes:\n%s", again)
			}
		})
	}
}

type person struct {
	ID     int    `btoon:"id"`
	Name   string `btoon:"name"`
	Active bool   `btoon:"active"`
}

func TestTabularFromStructs(t *testing.T) {
	f := loadVectors(t)
	var want string
	for _, v := range f.Vectors {
		if v.ID == "tabular/people" {
			want = v.Hex
		}
	}
	people := make([]person, 8)
	for i := range people {
		people[i] = person{ID: i, Name: "user" + string(rune('0'+i)), Active: i%2 == 0}
	}
	wantHex(t, people, want)
	ptrs := make([]*person, len(people))
	for i := range people {
		ptrs[i] = &people[i]
	}
	wantHex(t, ptrs, want)
	wantHex(t, [8]person(people), want)

	// The decoded tabular unmarshals back into the structs.
	var back []person
	b, _ := hex.DecodeString(want)
	if err := btoon.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if len(back) != 8 || back[3] != people[3] {
		t.Fatalf("got %+v", back)
	}
}

func TestTabularQualification(t *testing.T) {
	type rec struct {
		A int `btoon:"a"`
		B int `btoon:"b,omitempty"`
	}
	full := make([]rec, 10)
	for i := range full {
		full[i] = rec{A: i, B: 1}
	}
	if b := mustMarshal(t, full); b[0] != 0xc7 {
		t.Fatalf("uniform records should be tabular: %x", b[:4])
	}
	// omitempty drops a key from one row: the key sets differ, so plain.
	full[4].B = 0
	if b := mustMarshal(t, full); b[0] != 0xdc && b[0]>>4 != 0x9 {
		t.Fatalf("non-uniform records should be a plain array: %x", b[:4])
	}
	// A nil element, or a non-map element, disqualifies the array.
	mixed := []any{map[string]int{"ab": 1}, nil}
	for i := 0; i < 10; i++ {
		mixed = append(mixed, map[string]int{"ab": 1})
	}
	if b := mustMarshal(t, mixed); b[0] != 0xdc && b[0]>>4 != 0x9 {
		t.Fatalf("got %x", b[:4])
	}
	// Empty maps never use tabular.
	empties := make([]map[string]int, 20)
	for i := range empties {
		empties[i] = map[string]int{}
	}
	if b := mustMarshal(t, empties); b[0] != 0xdc {
		t.Fatalf("got %x", b[:4])
	}
	// DisableTabular.
	rows := make([]map[string]int, 8)
	for i := range rows {
		rows[i] = map[string]int{"ab": 1}
	}
	b, err := btoon.MarshalWithOptions(rows, btoon.EncodeOptions{DisableTabular: true})
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(b) != "98"+strings.Repeat("81a2616201", 8) {
		t.Fatalf("got %x", b)
	}
	// Structs that marshal themselves are not maps.
	ips := make([]ipLike, 10)
	if b := mustMarshal(t, ips); b[0] != 0x9a {
		t.Fatalf("got %x", b[:4])
	}
}

func TestDepthLimitAndCycles(t *testing.T) {
	nest := func(n int) any {
		var v any
		for i := 0; i < n; i++ {
			v = []any{v}
		}
		return v
	}
	if _, err := btoon.Marshal(nest(128)); err != nil {
		t.Fatalf("128 levels: %v", err)
	}
	_, err := btoon.Marshal(nest(129))
	wantErrCode(t, err, btoon.CodeMaxDepth)
	if _, err := btoon.MarshalWithOptions(nest(129), btoon.EncodeOptions{MaxDepth: 129}); err != nil {
		t.Fatal(err)
	}
	_, err = btoon.MarshalWithOptions(nest(3), btoon.EncodeOptions{MaxDepth: 2})
	wantErrCode(t, err, btoon.CodeMaxDepth)
	_, err = btoon.MarshalWithOptions(1, btoon.EncodeOptions{MaxDepth: -1})
	wantErrCode(t, err, btoon.CodeInvalidArgument)

	m := map[string]any{}
	m["self"] = m
	_, err = btoon.Marshal(m)
	wantErrCode(t, err, btoon.CodeMaxDepth)

	s := []any{nil}
	s[0] = s
	_, err = btoon.Marshal(s)
	wantErrCode(t, err, btoon.CodeMaxDepth)

	type node struct {
		Next *node
	}
	n := &node{}
	n.Next = n
	_, err = btoon.Marshal(n)
	wantErrCode(t, err, btoon.CodeMaxDepth)

	var x any
	x = &x // a pointer cycle without any container
	_, err = btoon.Marshal(x)
	wantErrCode(t, err, btoon.CodeMaxDepth)

	// A cycle through tabular rows.
	rows := make([]map[string]any, 10)
	for i := range rows {
		rows[i] = map[string]any{"r": nil}
	}
	for i := range rows {
		rows[i]["r"] = rows
	}
	_, err = btoon.Marshal(rows)
	wantErrCode(t, err, btoon.CodeMaxDepth)
}

func TestTimesEncodeAsUTCTimestamps(t *testing.T) {
	loc := time.FixedZone("X", 2*3600)
	tm := time.Date(2024, 1, 1, 2, 0, 0, 0, loc) // 2024-01-01T00:00:00Z
	wantHex(t, tm, "d6ff65920080")
	wantHex(t, tm.UTC(), "d6ff65920080")
	b, err := btoon.MarshalWithOptions(tm, btoon.EncodeOptions{PreserveTimeOffset: true})
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(b) != "c70eff0000000000000000659200800078" { // vector timestamp/offset+120
		t.Fatalf("got %x", b)
	}
	// UTC times keep the plain form even with PreserveTimeOffset.
	b, _ = btoon.MarshalWithOptions(tm.UTC(), btoon.EncodeOptions{PreserveTimeOffset: true})
	if hex.EncodeToString(b) != "d6ff65920080" {
		t.Fatalf("got %x", b)
	}
	// Offsets that are not whole minutes cannot be represented.
	odd := time.Date(2024, 1, 1, 0, 0, 0, 0, time.FixedZone("LMT", 1172))
	_, err = btoon.MarshalWithOptions(odd, btoon.EncodeOptions{PreserveTimeOffset: true})
	wantErrCode(t, err, btoon.CodeInvalidArgument)

	wantHex(t, btoon.Timestamp{Seconds: -1}, "c70cff00000000ffffffffffffffff")
	wantHex(t, btoon.Timestamp{Seconds: 1 << 34, Nanoseconds: 1}, "c70cff000000010000000400000000")
	wantHex(t, btoon.Date(0), "d7fe0000000000000000")
	wantHex(t, btoon.DateTime(-1), "d7fdffffffffffffffff")
	_, err = btoon.Marshal(btoon.Timestamp{Nanoseconds: 1e9})
	wantErrCode(t, err, btoon.CodeInvalidArgument)
	_, err = btoon.Marshal(btoon.Timestamp{HasOffset: true, OffsetMinutes: 1081})
	wantErrCode(t, err, btoon.CodeInvalidArgument)
}
