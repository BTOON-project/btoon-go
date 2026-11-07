package btoon_test

import (
	"reflect"
	"testing"
	"time"

	btoon "github.com/BTOON-project/btoon-go"
)

// decodeMap encodes v and decodes it back into a dynamic map.
func decodeMap(t *testing.T, v any) map[string]any {
	t.Helper()
	got, err := btoon.Decode(mustMarshal(t, v))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("decoded %T, want a map", got)
	}
	return m
}

func keysOf(m map[string]any) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func TestStructTags(t *testing.T) {
	type T struct {
		Plain    int
		Renamed  int    `btoon:"renamed"`
		JSONOnly int    `json:"json_only"`
		Both     int    `btoon:"from_btoon" json:"from_json"`
		Skipped  int    `btoon:"-"`
		JSONSkip int    `json:"-"`
		Dash     int    `btoon:"-,"`
		Empty    string `btoon:"empty,omitempty"`
		Zero     string `btoon:",omitzero"`
		unexp    int
	}
	m := decodeMap(t, T{Plain: 1, Renamed: 2, JSONOnly: 3, Both: 4, Skipped: 5, JSONSkip: 6, Dash: 7, unexp: 8})
	want := map[string]any{"Plain": int64(1), "renamed": int64(2), "json_only": int64(3), "from_btoon": int64(4), "-": int64(7)}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("got %#v\nwant %#v", m, want)
	}
	// Keys are in canonical order regardless of field order.
	wantHex(t, struct {
		Name   string `btoon:"name"`
		Age    int    `btoon:"age"`
		Active bool   `btoon:"active"`
	}{"Alice", 30, true}, "83a6616374697665c3a36167651ea46e616d65a5416c696365") // SPEC §4 example
}

func TestOmitEmptyAndOmitZero(t *testing.T) {
	type inner struct{ X int }
	type T struct {
		S   string            `btoon:",omitempty"`
		I   int               `btoon:",omitempty"`
		F   float64           `btoon:",omitempty"`
		B   bool              `btoon:",omitempty"`
		P   *int              `btoon:",omitempty"`
		A   any               `btoon:",omitempty"`
		Sl  []int             `btoon:",omitempty"`
		M   map[string]int    `btoon:",omitempty"`
		St  inner             `btoon:",omitempty"` // structs are never empty
		Tm  time.Time         `btoon:",omitempty"` // nor is time.Time
		Tz  time.Time         `btoon:",omitzero"`  // but omitzero uses IsZero
		Iz  inner             `btoon:",omitzero"`
		Arr [0]int            `btoon:",omitempty"`
		Pm  *map[string]int   `btoon:",omitzero"`
		E   map[string]string `btoon:",omitempty"`
	}
	got := keysOf(decodeMap(t, T{E: map[string]string{}}))
	if len(got) != 2 || !contains(got, "St") || !contains(got, "Tm") {
		t.Fatalf("got keys %v, want [St Tm]", got)
	}
	one := 1
	got = keysOf(decodeMap(t, T{S: "x", I: 1, P: &one, Sl: []int{1}, Tz: time.Unix(1, 0), Iz: inner{1}}))
	for _, k := range []string{"S", "I", "P", "Sl", "St", "Tm", "Tz", "Iz"} {
		if !contains(got, k) {
			t.Fatalf("missing %s in %v", k, got)
		}
	}
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

type Base struct {
	ID   int    `btoon:"id"`
	Kind string `btoon:"kind"`
}

type Audit struct {
	Created string `btoon:"created"`
	Kind    string `btoon:"kind"` // conflicts with Base.Kind at the same depth
}

type unexportedEmbed struct {
	Visible int `btoon:"visible"`
	hidden  int
}

func TestEmbeddedStructs(t *testing.T) {
	type Doc struct {
		Base
		*Audit
		unexportedEmbed
		Title string `btoon:"title"`
	}
	d := Doc{Base: Base{ID: 1, Kind: "k"}, Audit: &Audit{Created: "now", Kind: "a"}, unexportedEmbed: unexportedEmbed{Visible: 3}, Title: "t"}
	m := decodeMap(t, d)
	// "kind" is ambiguous (same depth, both tagged) and is dropped, as in encoding/json.
	want := map[string]any{"id": int64(1), "created": "now", "visible": int64(3), "title": "t"}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("got %#v\nwant %#v", m, want)
	}
	// A nil embedded pointer contributes no fields.
	d.Audit = nil
	if _, ok := decodeMap(t, d)["created"]; ok {
		t.Fatal("fields of a nil embedded pointer were encoded")
	}

	// A shallower field wins over a promoted one.
	type Outer struct {
		Base
		Kind string `btoon:"kind"`
	}
	if m := decodeMap(t, Outer{Base: Base{Kind: "inner"}, Kind: "outer"}); m["kind"] != "outer" {
		t.Fatalf("got %#v", m)
	}
	// A tagged embedded struct is a nested map, not flattened.
	type Tagged struct {
		Base `btoon:"base"`
	}
	if m := decodeMap(t, Tagged{Base{ID: 2}}); !reflect.DeepEqual(m["base"], map[string]any{"id": int64(2), "kind": ""}) {
		t.Fatalf("got %#v", m)
	}

	// Unmarshal allocates embedded pointers and fills promoted fields.
	var back Doc
	if err := btoon.Unmarshal(mustMarshal(t, map[string]any{"id": 5, "created": "c", "visible": 6, "title": "x"}), &back); err != nil {
		t.Fatal(err)
	}
	if back.ID != 5 || back.Audit == nil || back.Created != "c" || back.Visible != 6 || back.Title != "x" {
		t.Fatalf("got %+v", back)
	}
}

func TestStructRoundTrip(t *testing.T) {
	type Address struct {
		City string `btoon:"city"`
		Zip  string `btoon:"zip,omitempty"`
	}
	type User struct {
		ID       uint64            `btoon:"id"`
		Name     string            `json:"name"`
		Email    *string           `btoon:"email,omitempty"`
		Tags     []string          `btoon:"tags"`
		Scores   map[string]int    `btoon:"scores"`
		Address  Address           `btoon:"address"`
		Friends  []*Address        `btoon:"friends"`
		Created  time.Time         `btoon:"created"`
		Avatar   []byte            `btoon:"avatar"`
		Vec      []float32         `btoon:"vec"`
		Meta     map[string]any    `btoon:"meta"`
		Ratio    float32           `btoon:"ratio"`
		Labels   map[string]string `btoon:"labels,omitempty"`
		Checksum [4]byte           `btoon:"checksum"`
	}
	email := "a@example.com"
	in := User{
		ID: 1 << 60, Name: "Ada", Email: &email, Tags: []string{"x", "y"},
		Scores: map[string]int{"go": 10}, Address: Address{City: "Tallinn"},
		Friends: []*Address{{City: "Tartu", Zip: "50090"}, nil},
		Created: time.Date(2025, 3, 4, 5, 6, 7, 8, time.UTC), Avatar: []byte{1, 2},
		Vec: []float32{0.5, -1}, Meta: map[string]any{"k": []any{int64(1), "two"}},
		Ratio: 0.25, Checksum: [4]byte{9, 8, 7, 6},
	}
	b := mustMarshal(t, in)
	var out User
	if err := btoon.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip\n in: %+v\nout: %+v", in, out)
	}
	// Encoding is deterministic.
	if string(b) != string(mustMarshal(t, out)) {
		t.Fatal("re-encoding differs")
	}
}
