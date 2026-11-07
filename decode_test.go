package btoon_test

import (
	"errors"
	"runtime"
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
