package btoon_test

import (
	"encoding/hex"
	"errors"
	"math"
	"math/big"
	"reflect"
	"strings"
	"testing"

	btoon "github.com/BTOON-project/btoon-go"
)

func fromHex(t *testing.T, h string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(h, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestUnmarshalIntegers(t *testing.T) {
	var i8 int8
	if err := btoon.Unmarshal(fromHex(t, "d080"), &i8); err != nil || i8 != -128 {
		t.Fatalf("int8: %v %v", i8, err)
	}
	wantErrCode(t, btoon.Unmarshal(fromHex(t, "cd012c"), &i8), btoon.CodeTypeMismatch) // 300
	var u8 uint8
	wantErrCode(t, btoon.Unmarshal(fromHex(t, "ff"), &u8), btoon.CodeTypeMismatch) // -1
	var u64 uint64
	if err := btoon.Unmarshal(fromHex(t, "cfffffffffffffffff"), &u64); err != nil || u64 != math.MaxUint64 {
		t.Fatalf("uint64: %v %v", u64, err)
	}
	var i64 int64
	wantErrCode(t, btoon.Unmarshal(fromHex(t, "cf8000000000000000"), &i64), btoon.CodeTypeMismatch)
	// A non-minimal bigint that fits is still an integer.
	if err := btoon.Unmarshal(fromHex(t, "c703fcffff80"), &i64); err != nil || i64 != -128 {
		t.Fatalf("bigint into int64: %v %v", i64, err)
	}
	wantErrCode(t, btoon.Unmarshal(fromHex(t, "c709fc010000000000000000"), &u64), btoon.CodeTypeMismatch)
	// Floats are not silently truncated into integers.
	var n int
	wantErrCode(t, btoon.Unmarshal(fromHex(t, "cb3ff8000000000000"), &n), btoon.CodeTypeMismatch)
	type small uint16
	var s small
	if err := btoon.Unmarshal(fromHex(t, "cdffff"), &s); err != nil || s != math.MaxUint16 {
		t.Fatalf("named uint16: %v %v", s, err)
	}
}

func TestUnmarshalBigInt(t *testing.T) {
	var p *big.Int
	if err := btoon.Unmarshal(fromHex(t, "c709fc010000000000000000"), &p); err != nil || p.String() != "18446744073709551616" {
		t.Fatalf("*big.Int: %v %v", p, err)
	}
	var v big.Int
	if err := btoon.Unmarshal(fromHex(t, "d38000000000000000"), &v); err != nil || v.String() != "-9223372036854775808" {
		t.Fatalf("big.Int: %v %v", &v, err)
	}
	var s struct{ N *big.Int }
	if err := btoon.Unmarshal(mustMarshal(t, map[string]any{"N": uint64(math.MaxUint64)}), &s); err != nil || s.N.String() != "18446744073709551615" {
		t.Fatalf("field: %v %v", s.N, err)
	}
	wantErrCode(t, btoon.Unmarshal(fromHex(t, "a161"), &p), btoon.CodeTypeMismatch)
}

func TestUnmarshalFloats(t *testing.T) {
	var f float64
	if err := btoon.Unmarshal(fromHex(t, "05"), &f); err != nil || f != 5 {
		t.Fatalf("int into float64: %v %v", f, err) // JS encoders write 5.0 as 5
	}
	var f32 float32
	if err := btoon.Unmarshal(fromHex(t, "cb3ff8000000000000"), &f32); err != nil || f32 != 1.5 {
		t.Fatalf("float32: %v %v", f32, err)
	}
	wantErrCode(t, btoon.Unmarshal(fromHex(t, "cb7e37e43c8800759c"), &f32), btoon.CodeTypeMismatch) // 1e300
	if err := btoon.Unmarshal(fromHex(t, "cb7ff8000000000000"), &f32); err != nil || !math.IsNaN(float64(f32)) {
		t.Fatalf("NaN: %v %v", f32, err)
	}
	wantErrCode(t, btoon.Unmarshal(fromHex(t, "a161"), &f), btoon.CodeTypeMismatch)
}

func TestUnmarshalStringsAndBinary(t *testing.T) {
	var s string
	if err := btoon.Unmarshal(fromHex(t, "c4026869"), &s); err != nil || s != "hi" {
		t.Fatalf("binary into string: %q %v", s, err)
	}
	var b []byte
	if err := btoon.Unmarshal(fromHex(t, "a26869"), &b); err != nil || string(b) != "hi" {
		t.Fatalf("string into []byte: %q %v", b, err)
	}
	var arr [2]byte
	if err := btoon.Unmarshal(fromHex(t, "c4020102"), &arr); err != nil || arr != [2]byte{1, 2} {
		t.Fatalf("[2]byte: %v %v", arr, err)
	}
	wantErrCode(t, btoon.Unmarshal(fromHex(t, "c403010203"), &arr), btoon.CodeTypeMismatch)
	type raw []byte
	var r raw
	if err := btoon.Unmarshal(fromHex(t, "c4020102"), &r); err != nil || len(r) != 2 {
		t.Fatalf("named []byte: %v %v", r, err)
	}
	type b8 byte
	var rb []b8
	if err := btoon.Unmarshal(fromHex(t, "c4020102"), &rb); err != nil || !reflect.DeepEqual(rb, []b8{1, 2}) {
		t.Fatalf("[]b8: %v %v", rb, err)
	}
	var bad int
	wantErrCode(t, btoon.Unmarshal(fromHex(t, "c40101"), &bad), btoon.CodeTypeMismatch)
}

func TestUnmarshalContainers(t *testing.T) {
	var ints []int
	if err := btoon.Unmarshal(fromHex(t, "93010203"), &ints); err != nil || !reflect.DeepEqual(ints, []int{1, 2, 3}) {
		t.Fatalf("[]int: %v %v", ints, err)
	}
	var arr [2]int
	if err := btoon.Unmarshal(fromHex(t, "93010203"), &arr); err != nil || arr != [2]int{1, 2} {
		t.Fatalf("[2]int from 3 elements: %v %v", arr, err)
	}
	arr = [2]int{7, 7}
	if err := btoon.Unmarshal(fromHex(t, "9101"), &arr); err != nil || arr != [2]int{1, 0} {
		t.Fatalf("[2]int from 1 element: %v %v", arr, err)
	}
	type key string
	var m map[key]int
	if err := btoon.Unmarshal(fromHex(t, "82a16101a16202"), &m); err != nil || m["a"] != 1 || m["b"] != 2 {
		t.Fatalf("map[key]int: %v %v", m, err)
	}
	// Existing maps are merged into, like encoding/json.
	m = map[key]int{"z": 26}
	if err := btoon.Unmarshal(fromHex(t, "81a16101"), &m); err != nil || len(m) != 2 {
		t.Fatalf("merge: %v %v", m, err)
	}
	var ifaceKeys map[any]any
	if err := btoon.Unmarshal(fromHex(t, "81a16101"), &ifaceKeys); err != nil || ifaceKeys["a"] != int64(1) {
		t.Fatalf("map[any]any: %v %v", ifaceKeys, err)
	}
	var intKeys map[int]int
	wantErrCode(t, btoon.Unmarshal(fromHex(t, "81a16101"), &intKeys), btoon.CodeInvalidArgument)
	var nested map[string][]*int
	if err := btoon.Unmarshal(fromHex(t, "81a1619201c0"), &nested); err != nil || *nested["a"][0] != 1 || nested["a"][1] != nil {
		t.Fatalf("nested: %v %v", nested, err)
	}
	var vec []float64
	if err := btoon.Unmarshal(fromHex(t, "d7fb0000803f00000040"), &vec); err != nil || !reflect.DeepEqual(vec, []float64{1, 2}) {
		t.Fatalf("f32 vector into []float64: %v %v", vec, err)
	}
	var vec64 btoon.VectorF64
	if err := btoon.Unmarshal(fromHex(t, "92cb3ff000000000000002"), &vec64); err != nil || !reflect.DeepEqual(vec64, btoon.VectorF64{1, 2}) {
		t.Fatalf("array into VectorF64: %v %v", vec64, err)
	}
	var plain []float64
	if err := btoon.Unmarshal(fromHex(t, "d7fa000000000000f03f"), &plain); err != nil || !reflect.DeepEqual(plain, []float64{1}) {
		t.Fatalf("f64 vector into []float64: %v %v", plain, err)
	}
	var notSlice []int
	wantErrCode(t, btoon.Unmarshal(fromHex(t, "81a16101"), &notSlice), btoon.CodeTypeMismatch)
}

func TestUnmarshalNilAndPointers(t *testing.T) {
	n := 5
	p := &n
	if err := btoon.Unmarshal(fromHex(t, "c0"), &p); err != nil || p != nil {
		t.Fatalf("nil into pointer: %v %v", p, err)
	}
	x := 7
	if err := btoon.Unmarshal(fromHex(t, "c0"), &x); err != nil || x != 7 {
		t.Fatalf("nil into int must leave it unchanged: %v %v", x, err)
	}
	s := []int{1}
	if err := btoon.Unmarshal(fromHex(t, "c0"), &s); err != nil || s != nil {
		t.Fatalf("nil into slice: %v %v", s, err)
	}
	var pp **int
	if err := btoon.Unmarshal(fromHex(t, "2a"), &pp); err != nil || **pp != 42 {
		t.Fatalf("**int: %v", err)
	}
	// An interface holding a pointer is decoded through the pointer.
	var target struct{ A int }
	var iface any = &target
	if err := btoon.Unmarshal(fromHex(t, "81a14101"), &iface); err != nil || target.A != 1 {
		t.Fatalf("interface with pointer: %+v %v", target, err)
	}
	var fresh any
	if err := btoon.Unmarshal(fromHex(t, "81a14101"), &fresh); err != nil || !reflect.DeepEqual(fresh, map[string]any{"A": int64(1)}) {
		t.Fatalf("any: %#v %v", fresh, err)
	}
	for _, bad := range []any{nil, 5, (*int)(nil)} {
		wantErrCode(t, btoon.Unmarshal(fromHex(t, "01"), bad), btoon.CodeInvalidArgument)
	}
}

func TestUnmarshalStructs(t *testing.T) {
	type T struct {
		A int    `btoon:"a"`
		B string `btoon:"b"`
	}
	var v T
	if err := btoon.Unmarshal(fromHex(t, "83a16101a162a178a163c3"), &v); err != nil || v != (T{1, "x"}) {
		t.Fatalf("got %+v %v", v, err)
	}
	err := btoon.UnmarshalWithOptions(fromHex(t, "83a16101a162a178a163c3"), &v, btoon.DecodeOptions{DisallowUnknownFields: true})
	wantErrCode(t, err, btoon.CodeTypeMismatch)
	if !strings.Contains(err.Error(), `"c"`) {
		t.Fatalf("error should name the field: %v", err)
	}
	// Keys match case-sensitively.
	var w T
	if err := btoon.Unmarshal(fromHex(t, "81a14101"), &w); err != nil || w.A != 0 {
		t.Fatalf("case-insensitive match: %+v %v", w, err)
	}
	// Errors carry the path to the offending value.
	type Outer struct {
		Items []T `btoon:"items"`
	}
	var o Outer
	err = btoon.Unmarshal(mustMarshal(t, map[string]any{"items": []any{map[string]any{"a": 1}, map[string]any{"a": "no"}}}), &o)
	wantErrCode(t, err, btoon.CodeTypeMismatch)
	if !strings.Contains(err.Error(), ".items[1].a") {
		t.Fatalf("error lacks the path: %v", err)
	}
	var e btoon.Ext
	wantErrCode(t, btoon.Unmarshal(fromHex(t, "01"), &e), btoon.CodeTypeMismatch)
	if err := btoon.Unmarshal(fromHex(t, "d401aa"), &e); err != nil || e.Type != 1 || e.Data[0] != 0xaa {
		t.Fatalf("Ext: %+v %v", e, err)
	}
}

type binID [3]byte

func (b *binID) UnmarshalBinary(p []byte) error {
	if len(p) != 3 {
		return errors.New("bad length")
	}
	copy(b[:], p)
	b[0] ^= 0xff // prove the method ran
	return nil
}

func TestUnmarshalBinaryUnmarshaler(t *testing.T) {
	var id binID
	if err := btoon.Unmarshal(fromHex(t, "c403010203"), &id); err != nil || id != (binID{0xfe, 2, 3}) {
		t.Fatalf("got %v %v", id, err)
	}
	err := btoon.Unmarshal(fromHex(t, "c40101"), &id)
	wantErrCode(t, err, btoon.CodeTypeMismatch)
	if !strings.Contains(err.Error(), "bad length") {
		t.Fatalf("cause not wrapped: %v", err)
	}
	// Arrays of bytes from an array of integers still work.
	if err := btoon.Unmarshal(fromHex(t, "93010203"), &id); err != nil || id != (binID{1, 2, 3}) {
		t.Fatalf("got %v %v", id, err)
	}
}

func TestUnmarshalTabularIntoStructs(t *testing.T) {
	type row struct {
		X int `btoon:"x"`
		Y int `btoon:"y"`
	}
	// Vector tabular/in-map.
	var doc struct {
		Data []row `btoon:"data"`
	}
	f := loadVectors(t)
	for _, v := range f.Vectors {
		if v.ID == "tabular/in-map" {
			if err := btoon.Unmarshal(v.bytes(t), &doc); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(doc.Data) != 10 || doc.Data[9] != (row{9, -9}) {
		t.Fatalf("got %+v", doc.Data)
	}
}
