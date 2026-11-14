package btoon_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/url"
	"testing"
	"time"

	btoon "github.com/BTOON-project/btoon-go"
)

// link embeds url.URL, whose MarshalBinary has a pointer receiver.
type link struct {
	url.URL
	Title string `btoon:"title"`
}

func hexOf(s string) string { return hex.EncodeToString([]byte(s)) }

// A method with a pointer receiver is used whether or not the value is
// addressable, as in encoding/json/v2. Before 0.3.0 a value that wasn't
// addressable (passed by value, or held in a map) was encoded by its kind,
// so link encoded as a map of its fields by value and as binary by pointer
// (review 2026-11-13).
func TestMethodsDontDependOnAddressability(t *testing.T) {
	u, err := url.Parse("https://btoon.net/docs")
	if err != nil {
		t.Fatal(err)
	}
	l := link{URL: *u, Title: "Docs"}
	// link gets MarshalBinary from url.URL by promotion, so it encodes as
	// the URL's binary form, without its other fields: the encoding/json
	// rule for a promoted marshaling method.
	want := "c416" + hexOf("https://btoon.net/docs")
	wantHex(t, l, want)
	wantHex(t, &l, want)
	wantHex(t, []link{l}, "91"+want)
	wantHex(t, map[string]link{"a": l}, "81a161"+want)
	wantHex(t, [1]link{l}, "91"+want)
	var back link
	if err := btoon.Unmarshal(mustMarshal(t, l), &back); err != nil || back.URL != *u {
		t.Fatalf("got %+v, %v", back, err)
	}

	wantHex(t, ptrMarshaler{7}, "c40107")
	wantHex(t, map[string]ptrMarshaler{"a": {7}}, "81a161c40107")
	wantHex(t, []any{ptrMarshaler{7}}, "91c40107")
	wantHex(t, struct{ P ptrMarshaler }{ptrMarshaler{9}}, "81a150c40109")
}

// event embeds time.Time, as in the review's probe.
type event struct {
	time.Time
	Name string `btoon:"name"`
}

// stamp is a common pattern: a named wrapper that adds methods to time.Time.
type stamp struct{ time.Time }

// nestedStamp embeds time.Time at depth 2.
type nestedStamp struct {
	stamp
	Note string
}

// customStamp declares its own MarshalBinary, which wins.
type customStamp struct{ time.Time }

func (customStamp) MarshalBinary() ([]byte, error) { return []byte("custom"), nil }

type maybeStamp struct{ *time.Time }

type bigNum struct{ *big.Int }

type bigValue struct{ big.Int }

// A struct that gets time.Time's marshaling methods by embedding it encodes
// as the embedded time, a timestamp, the way encoding/json writes it as the
// time's JSON. It used to encode as time.Time's private binary format
// (review 2026-11-13). The same holds for big.Int and integers.
func TestEmbeddedTimeAndBigInt(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	ts := mustMarshal(t, at)
	for _, v := range []any{
		event{at, "launch"}, &event{at, "launch"}, []event{{at, "launch"}}[0],
		stamp{at}, nestedStamp{stamp{at}, "n"}, maybeStamp{&at},
	} {
		if got := mustMarshal(t, v); !bytes.Equal(got, ts) {
			t.Fatalf("%T: got %x, want the timestamp %x", v, got, ts)
		}
	}
	// In a container, too, and a slice of them is no table.
	wantHex(t, []stamp{{at}, {at}}, "92"+hex.EncodeToString(ts)+hex.EncodeToString(ts))
	// A nil embedded pointer encodes as nil.
	wantHex(t, maybeStamp{}, "c0")
	// A struct's own MarshalBinary is used.
	wantHex(t, customStamp{at}, "c406"+hexOf("custom"))
	// The time keeps its offset with PreserveTimeOffset.
	local := at.In(time.FixedZone("", 2*3600))
	withOffset, err := btoon.MarshalWithOptions(local, btoon.EncodeOptions{PreserveTimeOffset: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := btoon.MarshalWithOptions(stamp{local}, btoon.EncodeOptions{PreserveTimeOffset: true})
	if err != nil || !bytes.Equal(got, withOffset) {
		t.Fatalf("got %x %v, want %x", got, err, withOffset)
	}

	// Unmarshal stores timestamps in the embedded time.
	var ev event
	if err := btoon.Unmarshal(ts, &ev); err != nil || !ev.Time.Equal(at) {
		t.Fatalf("got %+v, %v", ev, err)
	}
	var ns nestedStamp
	if err := btoon.Unmarshal(ts, &ns); err != nil || !ns.Time.Equal(at) {
		t.Fatalf("got %+v, %v", ns, err)
	}
	var ms maybeStamp
	if err := btoon.Unmarshal(ts, &ms); err != nil || ms.Time == nil || !ms.Time.Equal(at) {
		t.Fatalf("got %+v, %v", ms, err)
	}
	// Dates and datetimes too.
	if err := btoon.Unmarshal(mustMarshal(t, btoon.NewDate(at)), &ev); err != nil || !ev.Time.Equal(at) {
		t.Fatalf("date: got %+v, %v", ev, err)
	}
	// What 0.2 wrote for an addressable event, time.Time's binary format,
	// still decodes through the promoted UnmarshalBinary.
	old, err := at.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	ev = event{}
	if err := btoon.Unmarshal(mustMarshal(t, old), &ev); err != nil || !ev.Time.Equal(at) {
		t.Fatalf("time.Time binary: got %+v, %v", ev, err)
	}

	big30, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	if got, want := mustMarshal(t, bigNum{big30}), mustMarshal(t, big30); !bytes.Equal(got, want) {
		t.Fatalf("bigNum: got %x, want %x", got, want)
	}
	wantHex(t, bigNum{big.NewInt(5)}, "05")
	wantHex(t, bigValue{*big.NewInt(-5)}, "fb")
	wantHex(t, &bigValue{*big.NewInt(300)}, "cd012c")
	wantHex(t, bigNum{}, "c0")
	var bn bigNum
	if err := btoon.Unmarshal(mustMarshal(t, big30), &bn); err != nil || bn.Int == nil || bn.Cmp(big30) != 0 {
		t.Fatalf("got %v, %v", bn.Int, err)
	}
	var bv bigValue
	if err := btoon.Unmarshal(fromHex(t, "cd012c"), &bv); err != nil || bv.Int64() != 300 {
		t.Fatalf("got %v, %v", &bv.Int, err)
	}
}

type color int

func (c color) MarshalText() ([]byte, error) {
	switch c {
	case 0:
		return []byte("red"), nil
	case 1:
		return []byte("green"), nil
	}
	return nil, errors.New("no such color")
}

func (c *color) UnmarshalText(b []byte) error {
	switch string(b) {
	case "red":
		*c = 0
	case "green":
		*c = 1
	default:
		return errors.New("no such color")
	}
	return nil
}

// version is a struct whose text is its value.
type version struct{ Major, Minor int }

func (v version) MarshalText() ([]byte, error) {
	if v.Major < 0 {
		return nil, errors.New("negative version")
	}
	return fmt.Appendf(nil, "%d.%d", v.Major, v.Minor), nil
}

func (v *version) UnmarshalText(b []byte) error {
	_, err := fmt.Sscanf(string(b), "%d.%d", &v.Major, &v.Minor)
	return err
}

// endpoint gets MarshalText from net.IP by embedding it.
type endpoint struct {
	net.IP
	Port int `btoon:"port"`
}

type badText struct{}

func (badText) MarshalText() ([]byte, error) { return []byte{0xff}, nil }

type binaryAndText struct{}

func (binaryAndText) MarshalBinary() ([]byte, error) { return []byte{1}, nil }
func (binaryAndText) MarshalText() ([]byte, error)   { return []byte("text"), nil }

// A struct type that implements encoding.TextMarshaler, but not
// BinaryMarshaler, encodes as its text, as in encoding/json: its fields
// don't always hold its value. *big.Float and *big.Rat, whose fields are
// unexported, used to encode as {} (review 2026-11-13). Types of other
// kinds keep the encoding of their kind, so that what 0.2 wrote for them
// doesn't change.
func TestTextMarshaler(t *testing.T) {
	wantHex(t, big.NewFloat(1.5), "a3"+hexOf("1.5"))
	wantHex(t, *big.NewFloat(1.5), "a3"+hexOf("1.5"))
	wantHex(t, big.NewRat(1, 3), "a3"+hexOf("1/3"))
	wantHex(t, version{1, 2}, "a3"+hexOf("1.2"))
	wantHex(t, &version{1, 2}, "a3"+hexOf("1.2"))
	wantHex(t, map[string]version{"v": {3, 4}}, "81a176a3"+hexOf("3.4"))
	wantHex(t, struct {
		F *big.Float `btoon:"f"`
	}{big.NewFloat(2)}, "81a166a132")
	// BinaryMarshaler comes first.
	wantHex(t, binaryAndText{}, "c40101")
	_, err := btoon.Marshal(badText{})
	wantErrCode(t, err, btoon.CodeInvalidUTF8)
	_, err = btoon.Marshal(version{-1, 0})
	wantErrCode(t, err, btoon.CodeInvalidArgument)

	// Other kinds keep the encoding of their kind.
	wantHex(t, color(1), "01")
	wantHex(t, slog.LevelWarn, "04")
	wantHex(t, net.ParseIP("192.0.2.1").To4(), "c404c0000201")
	// So does a struct that gets its MarshalText from an embedded field:
	// encoding it as the field's text would drop the other fields.
	ep := endpoint{net.IP{192, 0, 2, 1}, 80}
	wantHex(t, ep, "82a24950c404c0000201a4706f727450")

	// Unmarshal gives strings to the UnmarshalText of struct types.
	var f big.Float
	if err := btoon.Unmarshal(mustMarshal(t, "1.5"), &f); err != nil || f.String() != "1.5" {
		t.Fatalf("big.Float: %v %v", &f, err)
	}
	var r *big.Rat
	if err := btoon.Unmarshal(mustMarshal(t, big.NewRat(1, 3)), &r); err != nil || r.String() != "1/3" {
		t.Fatalf("big.Rat: %v %v", r, err)
	}
	var v version
	if err := btoon.Unmarshal(mustMarshal(t, version{5, 6}), &v); err != nil || v != (version{5, 6}) {
		t.Fatalf("version: %v %v", v, err)
	}
	wantErrCode(t, btoon.Unmarshal(mustMarshal(t, "x"), &v), btoon.CodeTypeMismatch)
	// Other kinds decode as before.
	var c color
	if err := btoon.Unmarshal(mustMarshal(t, 1), &c); err != nil || c != 1 {
		t.Fatalf("color: %v %v", c, err)
	}
	wantErrCode(t, btoon.Unmarshal(mustMarshal(t, "green"), &c), btoon.CodeTypeMismatch)
	var ip net.IP
	if err := btoon.Unmarshal(mustMarshal(t, []byte{192, 0, 2, 1}), &ip); err != nil || !ip.Equal(net.IPv4(192, 0, 2, 1)) {
		t.Fatalf("net.IP: %v %v", ip, err)
	}
	var back endpoint
	if err := btoon.Unmarshal(mustMarshal(t, ep), &back); err != nil || !back.IP.Equal(ep.IP) || back.Port != 80 {
		t.Fatalf("endpoint: %+v %v", back, err)
	}
}

type unexportedInner struct{ X int }

type unexportedBinary struct{ X int }

func (unexportedBinary) MarshalBinary() ([]byte, error) { return []byte{1}, nil }
func (*unexportedBinary) UnmarshalBinary([]byte) error  { return nil }

type unexportedZero struct{ X int }

func (z unexportedZero) IsZero() bool { return z.X == 0 }

// Reflection can't set an embedded field of an unexported type that has a
// tag, nor call its methods. Unmarshal used to panic there, as
// encoding/json does (review 2026-11-13); now it reports an error, and both
// directions use the field's kind where they would call a method.
func TestTaggedEmbeddedUnexported(t *testing.T) {
	type ptrTagged struct {
		*unexportedInner `btoon:"in"`
		Y                int
	}
	doc := mustMarshal(t, map[string]any{"in": map[string]any{"X": 1}, "Y": 2})
	var p ptrTagged
	wantErrCode(t, btoon.Unmarshal(doc, &p), btoon.CodeInvalidArgument)
	// nil into the nil pointer sets nothing.
	if err := btoon.Unmarshal(mustMarshal(t, map[string]any{"in": nil, "Y": 2}), &p); err != nil || p.Y != 2 {
		t.Fatalf("got %+v, %v", p, err)
	}
	// An allocated pointer is filled, and can't be set to nil.
	p = ptrTagged{unexportedInner: &unexportedInner{}}
	if err := btoon.Unmarshal(doc, &p); err != nil || p.unexportedInner.X != 1 || p.Y != 2 {
		t.Fatalf("got %+v, %v", p, err)
	}
	wantErrCode(t, btoon.Unmarshal(mustMarshal(t, map[string]any{"in": nil}), &p), btoon.CodeInvalidArgument)
	wantHex(t, p, "82a15902a2696e81a15801")

	type binaryTagged struct {
		unexportedBinary `btoon:"in"`
		Y                int
	}
	var b binaryTagged
	wantErrCode(t, btoon.Unmarshal(mustMarshal(t, map[string]any{"in": []byte{1}}), &b), btoon.CodeTypeMismatch)
	if err := btoon.Unmarshal(mustMarshal(t, map[string]any{"in": map[string]any{"X": 3}}), &b); err != nil || b.unexportedBinary.X != 3 {
		t.Fatalf("got %+v, %v", b, err)
	}

	type zeroTagged struct {
		unexportedZero `btoon:"in,omitzero"`
		Y              int
	}
	wantHex(t, zeroTagged{}, "81a15900")
	wantHex(t, zeroTagged{unexportedZero{1}, 0}, "82a15900a2696e81a15801")
}
