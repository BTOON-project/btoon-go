package btoon_test

import (
	"math"
	"testing"
	"time"

	btoon "github.com/BTOON-project/btoon-go"
)

func TestDecodeTimes(t *testing.T) {
	cases := []struct {
		hex    string
		want   time.Time
		offset int // seconds east of UTC; 0 means time.UTC
	}{
		{"d6ff65920080", time.Unix(1704067200, 0), 0},
		{"d7ff7735940065920080", time.Unix(1704067200, 500000000), 0},
		{"c70cff075bcd15fffffff1886e0900", time.Date(1, 1, 1, 0, 0, 0, 123456789, time.UTC), 0},
		{"c70eff0000000000000000659200800078", time.Unix(1704067200, 0), 120 * 60},
		{"c70eff000003e70000000065920080feb6", time.Unix(1704067200, 999), -330 * 60},
		{"d7fe0000018bcfe5687b", time.UnixMilli(1700000000123), 0}, // date
		{"d7fd17979cfe3d85cd15", time.Unix(0, 1700000000123456789), 0},
	}
	for _, c := range cases {
		got, err := btoon.Decode(fromHex(t, c.hex))
		if err != nil {
			t.Fatal(err)
		}
		tm, ok := got.(time.Time)
		if !ok || !tm.Equal(c.want) {
			t.Fatalf("%s: got %#v, want %v", c.hex, got, c.want)
		}
		_, off := tm.Zone()
		if c.offset == 0 && tm.Location() != time.UTC || off != c.offset {
			t.Fatalf("%s: zone %v, want offset %d", c.hex, tm.Location(), c.offset)
		}
		// time.Time targets get the same value; Unmarshal into any likewise.
		var direct time.Time
		if err := btoon.Unmarshal(fromHex(t, c.hex), &direct); err != nil || !direct.Equal(c.want) {
			t.Fatalf("%s: Unmarshal: %v %v", c.hex, direct, err)
		}
	}
	// A 14-byte timestamp keeps its offset through PreserveTimeOffset.
	got, _ := btoon.Decode(fromHex(t, "c70eff000003e70000000065920080feb6"))
	b, err := btoon.MarshalWithOptions(got, btoon.EncodeOptions{PreserveTimeOffset: true})
	if err != nil || string(b) != string(fromHex(t, "c70eff000003e70000000065920080feb6")) {
		t.Fatalf("offset round trip: %x %v", b, err)
	}
}

func TestPreserveTimeTypes(t *testing.T) {
	opts := btoon.DecodeOptions{PreserveTimeTypes: true}
	for hex, want := range map[string]any{
		"d6ff65920080":                       btoon.Timestamp{Seconds: 1704067200},
		"c70eff0000000000000000659200800078": btoon.Timestamp{Seconds: 1704067200, HasOffset: true, OffsetMinutes: 120},
		"d7feffffffffffffffff":               btoon.Date(-1),
		"d7fdffffffffffffffff":               btoon.DateTime(-1),
	} {
		got, err := btoon.DecodeWithOptions(fromHex(t, hex), opts)
		if err != nil || got != want {
			t.Fatalf("%s: got %#v %v, want %#v", hex, got, err, want)
		}
		// These types re-encode exactly.
		if b := mustMarshal(t, got); string(b) != string(fromHex(t, hex)) {
			t.Fatalf("%s: re-encoded as %x", hex, b)
		}
	}
}

func TestTimestampOutsideTimeRange(t *testing.T) {
	// seconds = math.MaxInt64 is a valid 12-byte timestamp that time.Time
	// cannot represent; Decode returns a Timestamp instead.
	h := "c70cff000000007fffffffffffffff"
	got, err := btoon.Decode(fromHex(t, h))
	if err != nil {
		t.Fatal(err)
	}
	if got != (btoon.Timestamp{Seconds: math.MaxInt64}) {
		t.Fatalf("got %#v", got)
	}
	if b := mustMarshal(t, got); string(b) != string(fromHex(t, h)) {
		t.Fatalf("re-encoded as %x", b)
	}
	var tm time.Time
	wantErrCode(t, btoon.Unmarshal(fromHex(t, h), &tm), btoon.CodeTypeMismatch)
	var ts btoon.Timestamp
	if err := btoon.Unmarshal(fromHex(t, h), &ts); err != nil || ts.Seconds != math.MaxInt64 {
		t.Fatalf("got %+v %v", ts, err)
	}
	// The most negative instant is representable.
	got, err = btoon.Decode(fromHex(t, "c70cff000000008000000000000000"))
	if tm, ok := got.(time.Time); err != nil || !ok || tm.Unix() != math.MinInt64 {
		t.Fatalf("got %#v %v", got, err)
	}
}

func TestUnmarshalTimeConversions(t *testing.T) {
	type T struct {
		TS btoon.Timestamp `btoon:"t"`
		D  btoon.Date      `btoon:"d"`
		DT btoon.DateTime  `btoon:"dt"`
		TT time.Time       `btoon:"tt"`
	}
	instant := time.Date(2024, 5, 6, 7, 8, 9, 10_000_000, time.UTC) // whole milliseconds
	b := mustMarshal(t, map[string]any{"t": instant, "d": instant, "dt": instant, "tt": btoon.Date(instant.UnixMilli())})
	var v T
	if err := btoon.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if v.TS != btoon.NewTimestamp(instant) || v.D != btoon.NewDate(instant) || v.DT != btoon.NewDateTime(instant) || !v.TT.Equal(instant) {
		t.Fatalf("got %+v", v)
	}
	if !v.D.Time().Equal(instant) || !v.DT.Time().Equal(instant) || !v.TS.Time().Equal(instant) {
		t.Fatal("Time methods disagree")
	}
	// A timestamp with sub-millisecond precision does not fit a Date.
	var d btoon.Date
	wantErrCode(t, btoon.Unmarshal(mustMarshal(t, time.Unix(0, 1)), &d), btoon.CodeTypeMismatch)
	// Nor does an instant beyond 2262 fit a DateTime.
	var dt btoon.DateTime
	wantErrCode(t, btoon.Unmarshal(mustMarshal(t, time.Date(2300, 1, 1, 0, 0, 0, 0, time.UTC)), &dt), btoon.CodeTypeMismatch)
	// Extreme dates convert exactly.
	if err := btoon.Unmarshal(mustMarshal(t, btoon.Date(math.MinInt64)), &d); err != nil || d != math.MinInt64 {
		t.Fatalf("got %v %v", d, err)
	}
	if err := btoon.Unmarshal(mustMarshal(t, btoon.DateTime(math.MinInt64)), &dt); err != nil || dt != math.MinInt64 {
		t.Fatalf("got %v %v", dt, err)
	}
	if err := btoon.Unmarshal(mustMarshal(t, btoon.DateTime(math.MaxInt64)), &dt); err != nil || dt != math.MaxInt64 {
		t.Fatalf("got %v %v", dt, err)
	}
	// An offset survives into a Timestamp target.
	var ts btoon.Timestamp
	if err := btoon.Unmarshal(fromHex(t, "c70eff0000000000000000659200800078"), &ts); err != nil || !ts.HasOffset || ts.OffsetMinutes != 120 {
		t.Fatalf("got %+v %v", ts, err)
	}
	wantErrCode(t, btoon.Unmarshal(fromHex(t, "01"), &ts), btoon.CodeTypeMismatch)
}
