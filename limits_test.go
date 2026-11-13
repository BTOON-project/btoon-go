package btoon_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"

	btoon "github.com/BTOON-project/btoon-go"
)

// decodeN decodes h with MaxValues n.
func decodeN(t *testing.T, h string, n int) (any, error) {
	t.Helper()
	return btoon.DecodeWithOptions(fromHex(t, h), btoon.DecodeOptions{MaxValues: n})
}

// wantValues checks that h decodes to exactly values values: it decodes
// with MaxValues = values and fails with size_limit below. (MaxValues 0
// means the default, so a single value is only checked to decode.)
func wantValues(t *testing.T, h string, values int) {
	t.Helper()
	if _, err := decodeN(t, h, values); err != nil {
		t.Fatalf("%s with MaxValues %d: %v", h, values, err)
	}
	if values > 1 {
		_, err := decodeN(t, h, values-1)
		wantErrCode(t, err, btoon.CodeSizeLimit)
	}
}

// tabular builds a tabular extension (SPEC §5.6.1) whose columns hold the
// given encoded cells.
func tabular(rows int, names []string, cols [][]byte) []byte {
	p := binary.BigEndian.AppendUint32(nil, 1)
	p = binary.BigEndian.AppendUint32(p, uint32(len(names)))
	p = binary.BigEndian.AppendUint32(p, uint32(rows))
	for _, n := range names {
		p = binary.BigEndian.AppendUint32(p, uint32(len(n)))
		p = append(p, n...)
	}
	p = append(p, make([]byte, len(names))...) // type codes (informational)
	for _, c := range cols {
		p = binary.BigEndian.AppendUint32(p, uint32(len(c)))
		p = append(p, c...)
	}
	out := binary.BigEndian.AppendUint32([]byte{0xc9}, uint32(len(p)))
	return append(append(out, 0xf6), p...)
}

// SPEC §7.1: a value counts once, when it starts: the top-level value,
// array elements, map values and extensions of every type. Map keys don't.
func TestMaxValuesCountsEveryValue(t *testing.T) {
	wantValues(t, "c0", 1)
	wantValues(t, "90", 1)
	wantValues(t, "93010203", 4)
	wantValues(t, "9291019102", 5)
	wantValues(t, "82a161c0a162c0", 3)                       // the map and two values; keys don't count
	wantValues(t, "81a1619101", 3)                           // map, array, element
	wantValues(t, "81a161a0", 2)                             // a string value counts, its key doesn't
	wantValues(t, "92d40100d6ff65920080", 3)                 // a generic extension and a timestamp
	wantValues(t, "93c701fc05d7fe0000000000000000c700fb", 4) // bigint, date, float32 vector

	// A value counts before its marker is examined: the third value exceeds
	// the limit before 0xc1 is rejected.
	_, err := decodeN(t, "9201c1", 2)
	wantErrCode(t, err, btoon.CodeSizeLimit)
	_, err = decodeN(t, "9201c1", 3)
	wantErrCode(t, err, btoon.CodeInvalidMarker)

	// Before anything inside it, too: the array exceeds the limit before its
	// elements are looked at.
	_, err = decodeN(t, "91c1", 1)
	wantErrCode(t, err, btoon.CodeSizeLimit)
}

// SPEC §7.1: a tabular array counts once when it starts, its N row maps
// count together once its header is checked, and each cell counts when it
// starts. Column names don't count.
func TestMaxValuesInTables(t *testing.T) {
	// 1 array + 4 rows + 2×4 cells. The two columns share the budget, so the
	// last cell of the second column is the one that fails.
	two := tabular(4, []string{"a", "b"}, [][]byte{{1, 2, 3, 4}, {5, 6, 7, 8}})
	wantValues(t, hex.EncodeToString(two), 13)

	// Nested in an array, followed by another value: the budget carries on
	// after the table.
	wantValues(t, "93"+hex.EncodeToString(two)+"c0c0", 16)

	// Rows count before any cell: with room for the array and its 4 rows
	// but not the first cell, the error is size_limit at the cell, and with
	// room for the array only, size_limit before any cell is decoded, so a
	// bad cell is never reached.
	bad := tabular(4, []string{"a"}, [][]byte{{0xc1, 2, 3, 4}})
	_, err := decodeN(t, hex.EncodeToString(bad), 5)
	wantErrCode(t, err, btoon.CodeSizeLimit)
	_, err = decodeN(t, hex.EncodeToString(bad), 6)
	wantErrCode(t, err, btoon.CodeInvalidMarker)

	// Cells inside cells count too.
	nested := tabular(2, []string{"k"}, [][]byte{{0x92, 1, 2, 0x92, 3, 4}})
	wantValues(t, hex.EncodeToString(nested), 1+2+2+4)

	// A table with columns but no rows is just the array.
	wantValues(t, hex.EncodeToString(tabular(0, []string{"a"}, [][]byte{{}})), 1)
}

// SPEC §7.1: the values inside a compression envelope count; the envelope
// doesn't.
func TestMaxValuesInsideEnvelopes(t *testing.T) {
	arr := make([]any, 1000)
	for i := range arr {
		arr[i] = int64(i % 7)
	}
	doc, err := btoon.MarshalWithOptions(arr, btoon.EncodeOptions{Compress: true})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(doc, []byte("BTON")) {
		t.Fatal("not compressed")
	}
	if _, err := btoon.DecodeWithOptions(doc, btoon.DecodeOptions{MaxValues: 1001}); err != nil {
		t.Fatal(err)
	}
	_, err = btoon.DecodeWithOptions(doc, btoon.DecodeOptions{MaxValues: 1000})
	wantErrCode(t, err, btoon.CodeSizeLimit)

	// Signed and compressed.
	key := []byte("0123456789abcdef0123456789abcdef")
	signed, err := btoon.MarshalWithOptions(arr, btoon.EncodeOptions{Compress: true, HMACKey: key})
	if err != nil {
		t.Fatal(err)
	}
	_, err = btoon.DecodeWithOptions(signed, btoon.DecodeOptions{MaxValues: 1000, HMACKey: key})
	wantErrCode(t, err, btoon.CodeSizeLimit)
}

func TestMaxValuesDefaultAndOptions(t *testing.T) {
	// The default is 2^20: the array and 2^20 - 1 nils, but not one more.
	doc := func(n int) []byte {
		return append(binary.BigEndian.AppendUint32([]byte{0xdd}, uint32(n)), bytes.Repeat([]byte{0xc0}, n)...)
	}
	if v, err := btoon.Decode(doc(btoon.DefaultMaxValues - 1)); err != nil || len(v.([]any)) != btoon.DefaultMaxValues-1 {
		t.Fatalf("at the limit: %v", err)
	}
	_, err := btoon.Decode(doc(btoon.DefaultMaxValues))
	wantErrCode(t, err, btoon.CodeSizeLimit)
	var be *btoon.Error
	if errors.As(err, &be); be.Offset != 1+4+btoon.DefaultMaxValues-1 {
		t.Fatalf("offset %d", be.Offset)
	}
	// Raising it decodes large documents.
	if _, err := btoon.DecodeWithOptions(doc(btoon.DefaultMaxValues), btoon.DecodeOptions{MaxValues: btoon.DefaultMaxValues + 1}); err != nil {
		t.Fatal(err)
	}
	// Unmarshal applies it as Decode does.
	var into []any
	err = btoon.UnmarshalWithOptions(fromHex(t, "93010203"), &into, btoon.DecodeOptions{MaxValues: 3})
	wantErrCode(t, err, btoon.CodeSizeLimit)
	// A negative limit is an error; 0 means the default.
	_, err = btoon.DecodeWithOptions(fromHex(t, "c0"), btoon.DecodeOptions{MaxValues: -1})
	wantErrCode(t, err, btoon.CodeInvalidArgument)
	_, err = btoon.DecodeWithOptions(fromHex(t, "c0"), btoon.DecodeOptions{MaxDecompressedSize: -1})
	wantErrCode(t, err, btoon.CodeInvalidArgument)
	if _, err := btoon.DecodeWithOptions(fromHex(t, "c0"), btoon.DecodeOptions{MaxValues: 0}); err != nil {
		t.Fatal(err)
	}
}

// Each document of a stream has its own max_values budget.
func TestMaxValuesPerStreamDocument(t *testing.T) {
	var buf bytes.Buffer
	enc := btoon.NewEncoder(&buf)
	for _, v := range []any{[]any{1, 2}, []any{3, 4}, []any{5, 6, 7}, []any{8, 9}} {
		if err := enc.Encode(v); err != nil {
			t.Fatal(err)
		}
	}
	dec := btoon.NewDecoder(&buf)
	dec.SetOptions(btoon.DecodeOptions{MaxValues: 3})
	var got []int
	for i, want := range []string{"", "", btoon.CodeSizeLimit, ""} {
		err := dec.Decode(&got)
		if want == "" {
			if err != nil {
				t.Fatalf("document %d: %v", i, err)
			}
			continue
		}
		wantErrCode(t, err, want)
	}
	if err := dec.Decode(&got); err != io.EOF {
		t.Fatalf("want io.EOF, got %v", err)
	}
}

// SPEC §5.6.3 and §7.1: a table whose rows × column-name bytes exceed
// max_decompressed_size fails with size_limit before any row exists.
func TestTabularColumnNameBytes(t *testing.T) {
	name := strings.Repeat("k", 1<<16)
	nils := func(n int) []byte { return bytes.Repeat([]byte{0xc0}, n) }
	// 1,024 rows × 64 KiB = 64 MiB, the default limit: accepted.
	if v, err := btoon.Decode(tabular(1024, []string{name}, [][]byte{nils(1024)})); err != nil || len(v.([]any)) != 1024 {
		t.Fatalf("at the limit: %v", err)
	}
	// One more row is over it, from 66 KB of input.
	over := tabular(1025, []string{name}, [][]byte{nils(1025)})
	_, err := btoon.Decode(over)
	wantErrCode(t, err, btoon.CodeSizeLimit)
	if _, err := btoon.DecodeWithOptions(over, btoon.DecodeOptions{MaxDecompressedSize: 1025 << 16}); err != nil {
		t.Fatalf("raised limit: %v", err)
	}
	// The names' total counts: 2 rows × (4 + 4) bytes.
	two := tabular(2, []string{"abcd", "efgh"}, [][]byte{nils(2), nils(2)})
	if _, err := btoon.DecodeWithOptions(two, btoon.DecodeOptions{MaxDecompressedSize: 16}); err != nil {
		t.Fatal(err)
	}
	_, err = btoon.DecodeWithOptions(two, btoon.DecodeOptions{MaxDecompressedSize: 15})
	wantErrCode(t, err, btoon.CodeSizeLimit)
	// Inside a cell the bound applies to each table on its own.
	inner := tabular(2, []string{"abcd"}, [][]byte{nils(2)})
	outer := tabular(1, []string{"x"}, [][]byte{inner})
	if _, err := btoon.DecodeWithOptions(outer, btoon.DecodeOptions{MaxDecompressedSize: 8}); err != nil {
		t.Fatal(err)
	}
	_, err = btoon.DecodeWithOptions(outer, btoon.DecodeOptions{MaxDecompressedSize: 7})
	wantErrCode(t, err, btoon.CodeSizeLimit)
}

// SPEC §5.6.3: after the names and type codes, a decoder checks N × C
// against the remaining payload (invalid_tabular), then the column-name
// bytes (size_limit), then counts the rows (size_limit).
func TestTabularHeaderCheckOrder(t *testing.T) {
	name := strings.Repeat("k", 1<<16)
	// 2,000 rows over 64 MiB of names, but only 1,500 cell bytes:
	// invalid_tabular comes first.
	short := tabular(2000, []string{name}, [][]byte{bytes.Repeat([]byte{0xc0}, 1500)})
	_, err := btoon.Decode(short)
	wantErrCode(t, err, btoon.CodeInvalidTabular)

	// Over both the name bytes and max_values: the name bytes are checked
	// first.
	two := tabular(2, []string{"abcd", "efgh"}, [][]byte{{0xc0, 0xc0}, {0xc0, 0xc0}})
	_, err = btoon.DecodeWithOptions(two, btoon.DecodeOptions{MaxDecompressedSize: 15, MaxValues: 1})
	wantErrCode(t, err, btoon.CodeSizeLimit)
	if !strings.Contains(err.Error(), "max_decompressed_size") {
		t.Fatalf("want the column-name error first, got %v", err)
	}
	_, err = btoon.DecodeWithOptions(two, btoon.DecodeOptions{MaxValues: 2})
	wantErrCode(t, err, btoon.CodeSizeLimit)
	if !strings.Contains(err.Error(), "max_values") {
		t.Fatalf("want the value-count error, got %v", err)
	}
}

// SPEC §5.6.3: column names are checked front to back, each one for UTF-8
// (in both modes) and then against the earlier names. The first name that
// fails determines the error.
func TestColumnNamesAreCheckedFrontToBack(t *testing.T) {
	cells := [][]byte{{0xc0}, {0xc0}, {0xc0}}
	for _, opts := range []btoon.DecodeOptions{{}, {NonStrict: true}} {
		for _, c := range []struct {
			names []string
			code  string
		}{
			{[]string{"a", "a", "\xff"}, btoon.CodeInvalidTabular},
			{[]string{"\xff", "a", "a"}, btoon.CodeInvalidUTF8},
			{[]string{"a", "\xff", "a"}, btoon.CodeInvalidUTF8},
			{[]string{"a", "b", "a"}, btoon.CodeInvalidTabular},
			// Two invalid names that a replacing decoder would merge.
			{[]string{"\xff", "\xfe", "c"}, btoon.CodeInvalidUTF8},
		} {
			_, err := btoon.DecodeWithOptions(tabular(1, c.names, cells), opts)
			wantErrCode(t, err, c.code)
		}
	}
}

// A container that announces more elements than max_values has left fails
// before it fills them, so it doesn't reserve them either. Otherwise a 64
// MiB envelope of empty maps behind an array32 header reserved 1 GiB of
// slots before max_values stopped it.
func TestReservationsStayWithinMaxValues(t *testing.T) {
	const n = 1 << 22
	arr := append(binary.BigEndian.AppendUint32([]byte{0xdd}, n), bytes.Repeat([]byte{0x80}, n)...)
	m := append(binary.BigEndian.AppendUint32([]byte{0xdf}, n/2), bytes.Repeat([]byte{0xa0, 0xc0}, n/2)...)
	tab := tabular(n/2, []string{"a"}, [][]byte{bytes.Repeat([]byte{0xc0}, n/2)})
	for _, c := range []struct {
		name string
		doc  []byte
		opts btoon.DecodeOptions
	}{
		{"array32 of empty maps", arr, btoon.DecodeOptions{MaxValues: 1000}},
		// Non-strict, so that the repeated key "" doesn't fail first.
		{"map32", m, btoon.DecodeOptions{MaxValues: 1000, NonStrict: true}},
		// The rows fit, the cells don't: no rows are built up front.
		{"table", tab, btoon.DecodeOptions{MaxValues: n/2 + 2}},
	} {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := btoon.DecodeWithOptions(c.doc, c.opts)
		runtime.ReadMemStats(&after)
		wantErrCode(t, err, btoon.CodeSizeLimit)
		if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 4<<20 {
			t.Errorf("%s: %d-byte input allocated %d MiB", c.name, len(c.doc), allocated>>20)
		}
	}
}

// The default envelope limit is 64 MiB (SPEC §7.1; 256 MiB in spec 0.1),
// checked before decompressing.
func TestDefaultMaxDecompressedSize(t *testing.T) {
	z := zlibBytes(t, []byte{0xc0})
	_, err := btoon.Decode(compressionEnvelope(btoon.Zlib, z, 64<<20+1))
	wantErrCode(t, err, btoon.CodeSizeLimit)
	// At the limit the payload is decompressed, and its size doesn't match.
	_, err = btoon.Decode(compressionEnvelope(btoon.Zlib, z, 64<<20))
	wantErrCode(t, err, btoon.CodeInvalidEnvelope)
	if btoon.DefaultMaxDecompressedSize != 64<<20 {
		t.Fatal(btoon.DefaultMaxDecompressedSize)
	}
}
