package btoon_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"testing"

	btoon "github.com/BTOON-project/btoon-go"
)

func TestNonStrictMode(t *testing.T) {
	ns := btoon.DecodeOptions{NonStrict: true}
	// Trailing data is ignored.
	if v, err := btoon.DecodeWithOptions(fromHex(t, "0102"), ns); err != nil || v != int64(1) {
		t.Fatalf("trailing: %v %v", v, err)
	}
	// Duplicate keys: the last one wins.
	v, err := btoon.DecodeWithOptions(fromHex(t, "82a16101a16102"), ns)
	if err != nil || v.(map[string]any)["a"] != int64(2) {
		t.Fatalf("duplicate: %v %v", v, err)
	}
	// UTF-8 is not validated.
	if v, err := btoon.DecodeWithOptions(fromHex(t, "a1ff"), ns); err != nil || v != "\xff" {
		t.Fatalf("utf8: %q %v", v, err)
	}
	if _, err := btoon.DecodeWithOptions(fromHex(t, "c718f600000001000000010000000200000001ff02000000020102"), ns); err != nil {
		t.Fatalf("column name utf8: %v", err)
	}
	// Bounds, depth, extension and tabular checks are not relaxed.
	for h, code := range map[string]string{
		"dbffffffff61":                   btoon.CodeTruncated,
		strings.Repeat("91", 129) + "c0": btoon.CodeMaxDepth,
		"c703ff000000":                   btoon.CodeInvalidExtension,
		"c724f6000000010000000200000002000000016100000001610202000000020102000000020102": btoon.CodeInvalidTabular,
		"c1": btoon.CodeInvalidMarker,
	} {
		_, err := btoon.DecodeWithOptions(fromHex(t, h), ns)
		wantErrCode(t, err, code)
	}
}

func TestMaxDepthOption(t *testing.T) {
	deep := fromHex(t, strings.Repeat("91", 10)+"c0")
	if _, err := btoon.DecodeWithOptions(deep, btoon.DecodeOptions{MaxDepth: 10}); err != nil {
		t.Fatal(err)
	}
	_, err := btoon.DecodeWithOptions(deep, btoon.DecodeOptions{MaxDepth: 9})
	wantErrCode(t, err, btoon.CodeMaxDepth)
	_, err = btoon.DecodeWithOptions(deep, btoon.DecodeOptions{MaxDepth: -1})
	wantErrCode(t, err, btoon.CodeInvalidArgument)
	_, err = btoon.DecodeWithOptions(deep, btoon.DecodeOptions{MaxDepth: 1 << 20})
	wantErrCode(t, err, btoon.CodeInvalidArgument)

	// A tabular array counts as two levels (SPEC §7): the array and its rows.
	tab := fromHex(t, "c71ff600000001000000010000000800000002616202000000080101010101010101") // size-rule-8-tabular
	if _, err := btoon.DecodeWithOptions(tab, btoon.DecodeOptions{MaxDepth: 2}); err != nil {
		t.Fatal(err)
	}
	_, err = btoon.DecodeWithOptions(tab, btoon.DecodeOptions{MaxDepth: 1})
	wantErrCode(t, err, btoon.CodeMaxDepth)
	// The encoder counts the same way.
	v, _ := btoon.Decode(tab)
	if _, err := btoon.MarshalWithOptions(v, btoon.EncodeOptions{MaxDepth: 2}); err != nil {
		t.Fatal(err)
	}
	_, err = btoon.MarshalWithOptions(v, btoon.EncodeOptions{MaxDepth: 1})
	wantErrCode(t, err, btoon.CodeMaxDepth)
}

func TestHostileLengthsDoNotAllocate(t *testing.T) {
	// Each input claims about 4 GiB (or 4 billion elements or cells). The
	// decoder must reject it from the remaining length alone.
	for _, h := range []string{
		"ddffffffff", "dfffffffff", "dbffffffff", "c6ffffffff", "c9ffffffffff",
		"c718f60000000100000001ffffffff000000016102000000020102", // tabular/huge-row-count
		"c70cf6000000010000ffff00000000",                         // 65535 columns in 3 bytes
	} {
		in := fromHex(t, h)
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for i := 0; i < 100; i++ {
			_, _ = btoon.Decode(in)
		}
		runtime.ReadMemStats(&after)
		if perCall := (after.TotalAlloc - before.TotalAlloc) / 100; perCall > 4096 {
			t.Errorf("%s: %d bytes allocated per decode", h, perCall)
		}
		_, err := btoon.Decode(in)
		var be *btoon.Error
		if !errors.As(err, &be) || (be.Code != btoon.CodeTruncated && be.Code != btoon.CodeInvalidTabular) {
			t.Errorf("%s: %v", h, err)
		}
	}
}

func TestTabularDecoderChecks(t *testing.T) {
	for h, code := range map[string]string{
		// num_rows = 0 but a non-empty column.
		"c717f60000000100000001000000000000000161020000000101": btoon.CodeInvalidTabular,
		// version 0.
		"c70cf6000000000000000000000000": btoon.CodeInvalidTabular,
		// Cell that runs past its column (string of 5 in a 2-byte column).
		"c718f600000001000000010000000100000001610500000002a561": btoon.CodeInvalidTabular,
		// Error inside a cell keeps its own code.
		"c718f600000001000000010000000100000001610500000002a1ff": btoon.CodeInvalidUTF8,
	} {
		_, err := btoon.Decode(fromHex(t, h))
		wantErrCode(t, err, code)
	}
	// Empty tabular with columns but no rows decodes to an empty array.
	v, err := btoon.Decode(fromHex(t, "c716f600000001000000010000000000000001610200000000"))
	if err != nil || len(v.([]any)) != 0 {
		t.Fatalf("got %#v %v", v, err)
	}
}

func TestErrorType(t *testing.T) {
	_, err := btoon.Decode(fromHex(t, "92a1ff"))
	var be *btoon.Error
	if !errors.As(err, &be) {
		t.Fatalf("got %T", err)
	}
	if be.Code != btoon.CodeInvalidUTF8 || be.Offset != 2 {
		t.Fatalf("got %+v", be)
	}
	if !errors.Is(err, btoon.ErrInvalidUTF8) || errors.Is(err, btoon.ErrTruncated) {
		t.Fatal("errors.Is does not match by code")
	}
	if got := err.Error(); got != "btoon: invalid_utf8: string is not valid UTF-8 (at byte 2)" {
		t.Fatalf("message %q", got)
	}
	if btoon.ErrTruncated.Error() != "btoon: truncated" {
		t.Fatal(btoon.ErrTruncated.Error())
	}
	_, err = btoon.Decode(fromHex(t, "82a16101a1"))
	if !errors.As(err, &be) || be.Code != btoon.CodeTruncated || be.Offset != 5 {
		t.Fatalf("got %v", err)
	}
}

func TestDecodedTypes(t *testing.T) {
	v, err := btoon.Decode(fromHex(t, "96c0c3ffcb3ff8000000000000a173c40100"))
	if err != nil {
		t.Fatal(err)
	}
	a := v.([]any)
	if a[0] != nil || a[1] != true || a[2] != int64(-1) || a[3] != 1.5 || a[4] != "s" || string(a[5].([]byte)) != "\x00" {
		t.Fatalf("got %#v", a)
	}
	if v, _ := btoon.Decode(fromHex(t, "cfffffffffffffffff")); v != uint64(1<<64-1) {
		t.Fatalf("got %#v", v)
	}
	if v, _ := btoon.Decode(fromHex(t, "cf7fffffffffffffff")); v != int64(1<<63-1) {
		t.Fatalf("got %#v", v)
	}
	// Decoded binary does not alias the input.
	in := fromHex(t, "c40101")
	v, _ = btoon.Decode(in)
	in[2] = 9
	if v.([]byte)[0] != 1 {
		t.Fatal("binary aliases the input")
	}
}

// nestedArrays returns levels nested array32 headers, each announcing every
// byte that follows it, then payload nils (review 2026-11-13, X1).
func nestedArrays(levels, payload int) []byte {
	doc := bytes.Repeat([]byte{0xc0}, levels*5+payload)
	for i := 0; i < levels; i++ {
		doc[i*5] = 0xdd
		binary.BigEndian.PutUint32(doc[i*5+1:], uint32(len(doc)-i*5-5))
	}
	return doc
}

// nestedMaps returns levels nested map32 headers, each announcing half the
// bytes that follow it and holding the next level under the key "". The
// innermost map fails at its first key.
func nestedMaps(levels, payload int) []byte {
	doc := bytes.Repeat([]byte{0xc0}, levels*6+payload)
	for i := 0; i < levels; i++ {
		doc[i*6] = 0xdf
		binary.BigEndian.PutUint32(doc[i*6+1:], uint32((len(doc)-i*6-5)/2))
		doc[i*6+5] = 0xa0
	}
	return doc
}

// nestedTabulars returns levels nested tabulars with one column. The
// innermost one has a single row holding a payload-byte binary; every outer
// level announces as many rows as its payload could hold, but its column
// holds only the next level.
func nestedTabulars(levels, payload int) []byte {
	level := func(cell []byte, rows int) []byte {
		p := binary.BigEndian.AppendUint32(nil, 1)
		p = binary.BigEndian.AppendUint32(p, 1)
		p = binary.BigEndian.AppendUint32(p, uint32(rows))
		p = binary.BigEndian.AppendUint32(p, 1)
		p = append(p, 'a', 9)
		p = binary.BigEndian.AppendUint32(p, uint32(len(cell)))
		p = append(p, cell...)
		out := binary.BigEndian.AppendUint32([]byte{0xc9}, uint32(len(p)))
		return append(append(out, 0xf6), p...)
	}
	bin := binary.BigEndian.AppendUint32([]byte{0xc6}, uint32(payload))
	doc := level(append(bin, make([]byte, payload)...), 1)
	for i := 1; i < levels; i++ {
		doc = level(doc, len(doc)+4)
	}
	return doc
}

// Nested containers used to reserve their announced length up front, each
// level checked against the same remaining bytes. 127 nested array32 headers
// in 1 MiB reserved 2 GiB, 127 map32 headers 5 GiB, and 64 nested tabulars
// built 3.5 GiB of rows before failing (review 2026-11-13, X1). Reservations
// now come out of one budget per document, the size of the document.
func TestNestedContainersPreallocateBoundedMemory(t *testing.T) {
	const payload = 64 << 10
	for _, c := range []struct {
		name string
		doc  []byte
		code string
	}{
		{"127 nested array32", nestedArrays(127, payload), btoon.CodeTruncated},
		{"127 nested map32", nestedMaps(127, payload), btoon.CodeNonStringKey},
		{"64 nested tabulars", nestedTabulars(64, payload), btoon.CodeInvalidTabular},
	} {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := btoon.Decode(c.doc)
		runtime.ReadMemStats(&after)
		wantErrCode(t, err, c.code)
		allocated := after.TotalAlloc - before.TotalAlloc
		if limit := 16<<20 + 32*uint64(len(c.doc)); allocated > limit {
			t.Errorf("%s: %d-byte input allocated %d MiB (limit %d MiB)", c.name, len(c.doc), allocated>>20, limit>>20)
		}
	}
}

// With MaxDepth raised to its limit, 65,535 nested array16 headers that each
// announce 1,024 elements (198 KB) reserved 1.1 GiB. A fixed allowance per
// container, as a fallback for when the budget runs out, would add up the
// same way, so there is none: past the budget, containers start empty.
func TestDeepNestingReservesWithinTheDocumentSize(t *testing.T) {
	const levels = 1<<16 - 1
	doc := bytes.Repeat([]byte{0xc0}, levels*3+1024)
	for i := 0; i < levels; i++ {
		copy(doc[i*3:], []byte{0xdc, 0x04, 0x00})
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := btoon.DecodeWithOptions(doc, btoon.DecodeOptions{MaxDepth: 1 << 16})
	runtime.ReadMemStats(&after)
	wantErrCode(t, err, btoon.CodeTruncated)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 32<<20 {
		t.Errorf("%d-byte input allocated %d MiB", len(doc), allocated>>20)
	}
}

// A valid document never spends more than its budget, so containers start
// empty only when a header announces more elements than the document holds,
// and the document fails to decode. Until it fails, those containers must
// decode exactly as the others do.
func TestContainersDecodeTheSameWithoutABudget(t *testing.T) {
	var docs [][]byte
	for _, v := range loadVectors(t).Vectors {
		if v.Options.HMACKey == "" && !strings.HasPrefix(v.Hex, "42544f4e") && !strings.HasPrefix(v.Hex, "42545347") {
			docs = append(docs, v.bytes(t))
		}
	}
	// Containers larger than the doubling starts with, and tables with
	// more cells, and more columns, than that.
	for _, n := range []int{15, 16, 17, 1025, 70000} {
		arr := make([]any, n)
		m := make(map[string]any, n)
		for i := range arr {
			arr[i] = int64(i)
			m["k"+strconv.Itoa(i)] = []any{int64(i)}
		}
		docs = append(docs, mustMarshal(t, arr), mustMarshal(t, m), mustMarshal(t, []any{arr, m}))
	}
	for _, shape := range [][2]int{{3000, 3}, {3, 1500}, {40, 20}} {
		rows := make([]any, shape[0])
		for r := range rows {
			row := make(map[string]any, shape[1])
			for c := 0; c < shape[1]; c++ {
				row["c"+strconv.Itoa(c)] = []any{int64(r*shape[1] + c), "x"}
			}
			rows[r] = row
		}
		doc := mustMarshal(t, rows)
		if !bytes.Contains(doc[:8], []byte{0xf6}) {
			t.Fatalf("%v: not encoded as a table", shape)
		}
		docs = append(docs, doc)
	}
	for _, doc := range docs {
		want, wantErr := btoon.Decode(doc)
		got, err := btoon.DecodeWithoutBudgetForTest(doc)
		var we, ge *btoon.Error
		switch {
		case wantErr != nil:
			if !errors.As(wantErr, &we) || !errors.As(err, &ge) || we.Code != ge.Code || we.Offset != ge.Offset {
				t.Errorf("% x: got %v, want %v", doc[:min(len(doc), 16)], err, wantErr)
			}
		case err != nil || !equalValues(got, want):
			t.Errorf("% x: decoded differently (%v)", doc[:min(len(doc), 16)], err)
		}
	}
}
