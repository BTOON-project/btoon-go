package codecs

import (
	"bytes"
	"encoding/hex"
	"math/rand/v2"
	"strings"
	"testing"
)

func lz4Hex(s string) string { return hex.EncodeToString([]byte(s)) }

// lz4Seq returns the hex of n bytes counting up from start.
func lz4Seq(n int, start byte) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = start + byte(i)
	}
	return hex.EncodeToString(b)
}

// lz4Cases are blocks with the verdict of liblz4 1.11.1's
// LZ4_decompress_safe (through python-lz4 4.4.5 and Homebrew's liblz4), with
// an output buffer of size bytes, accepted only if it fills it. out is the
// output, or "reject". The cases marked spec differ from liblz4, which is
// looser or stricter than SPEC §6 there; see decodeLZ4Block.
var lz4Cases = []struct {
	name  string
	block string
	size  int
	out   string
	spec  bool // SPEC §6 decides against liblz4
}{
	// The review's probes (docs/review-2026-11-13/probes/go/probes/lz4diff),
	// which pierrec/lz4 got wrong.
	{"probe/ends-with-match", "10" + lz4Hex("a") + "0100", 5, "reject", false},
	{"probe/match-too-close-to-end", "10" + lz4Hex("a") + "0100" + "50" + lz4Hex("bbbbb"), 10, "reject", false},
	{"probe/match-then-empty-last", "d0" + lz4Hex("abcdefghijklm") + "0d00" + "00", 17, "reject", false},
	{"probe/valid-liblz4", "6f68656c6c6f2006000c50656c6c6f21", 42, lz4Hex(strings.Repeat("hello ", 7)[:41] + "!"), false},
	{"probe/e2e-ends-with-match", "20a5610100", 6, "reject", false},
	{"probe/e2e-valid-literals-only", "60a56161616161", 6, "a56161616161", false},

	// The end-of-block conditions.
	{"final-token-match-bits-ignored", "6fa56161616161", 6, "a56161616161", false},
	{"last-match-at-the-limit", "13" + lz4Hex("a") + "0100" + "50" + lz4Hex("bbbbb"), 13, lz4Hex("aaaaaaaabbbbb"), false},
	{"last-match-starts-11-before-end", "23" + lz4Hex("ab") + "0100" + "40" + lz4Hex("bbbb"), 13, "reject", false},
	{"last-match-ends-3-before-end", "15" + lz4Hex("a") + "0100" + "30" + lz4Hex("bbb"), 13, "reject", false},
	// liblz4 copies these last matches on its fast path, which skips the
	// end-of-block checks, and accepts the blocks.
	{"spec/fast-path-match-ends-at-end", "ee" + lz4Hex("dadbabdbadbddc") + "0c00" + "06", 32, "reject", true},
	{"spec/fast-path-4-final-literals", "ea" + lz4Hex("daacabbdadaadd") + "0d00" + "40" + lz4Hex("daac"), 32, "reject", true},

	// Empty output and empty input.
	{"empty/token-0", "00", 0, "", false},
	{"spec/empty/token-0f", "0f", 0, "", true}, // liblz4 accepts only 00
	{"empty/one-literal", "1061", 0, "reject", false},
	{"empty/no-input", "", 0, "reject", false},
	{"no-input", "", 1, "reject", false},

	// The output must have exactly the declared size.
	{"literals-only", "30010203", 3, "010203", false},
	{"literals-only/size-too-large", "30010203", 4, "reject", false},
	{"literals-only/size-too-small", "30010203", 2, "reject", false},

	// Offsets. Offset 0 is invalid in the block format (SPEC §6), and liblz4
	// decodes it as zeros, on its fast path and off it.
	{"spec/offset-0/at-start", "000000" + "d0" + lz4Hex("abcdefghijklm"), 17, "reject", true},
	{"spec/offset-0/mid", "d0" + lz4Hex("abcdefghijklm") + "0000" + "80" + lz4Hex("stuvwxyz"), 25, "reject", true},
	{"spec/offset-0/long", "ff05" + lz4Seq(20, 1) + "0000" + "1f" + "f031" + lz4Seq(64, 100), 134, "reject", true},
	{"offset-1", "ff05" + lz4Seq(20, 1) + "0100" + "1f" + "f031" + lz4Seq(64, 100), 134, lz4Seq(20, 1) + strings.Repeat("14", 50) + lz4Seq(64, 100), false},
	{"offset-5-overlapping", "51" + lz4Hex("abcde") + "0500" + "80" + lz4Hex("12345678"), 18, lz4Hex("abcdeabcde12345678"), false},
	{"offset-past-start", "51" + lz4Hex("abcde") + "0600" + "80" + lz4Hex("12345678"), 18, "reject", false},
	{"offset-truncated", "1f" + lz4Hex("a") + "01", 20, "reject", false},

	// Length extensions.
	{"literal-length-15", "f000" + lz4Seq(15, 1), 15, lz4Seq(15, 1), false},
	{"literal-length-270", "f0ff00" + lz4Seq(255, 0) + lz4Seq(15, 0), 270, lz4Seq(255, 0) + lz4Seq(15, 0), false},
	{"literal-length-past-input", "f001" + lz4Seq(15, 1), 16, "reject", false},
	{"match-length-279", "1f" + lz4Hex("a") + "0100" + "ff05" + "50" + lz4Hex("bbbbb"), 285, lz4Hex(strings.Repeat("a", 280) + "bbbbb"), false},
	{"match-length-extension-truncated", "1f" + lz4Hex("a") + "0100", 285, "reject", false},
}

func TestLZ4BlockVerdicts(t *testing.T) {
	for _, c := range lz4Cases {
		t.Run(c.name, func(t *testing.T) {
			block, err := hex.DecodeString(c.block)
			if err != nil {
				t.Fatal(err)
			}
			out, err := LZ4.Decompress(block, c.size)
			if c.out == "reject" {
				if err == nil {
					t.Fatalf("accepted, decoding to %x", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if got := hex.EncodeToString(out); got != c.out {
				t.Fatalf("decoded to %s, want %s", got, c.out)
			}
		})
	}
}

// TestLZ4RoundTrip checks that the decoder accepts every block that
// pierrec/lz4's compressors write, at every level and around the sizes where
// the end-of-block conditions matter.
func TestLZ4RoundTrip(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	inputs := map[string]func(n int) []byte{
		"random": func(n int) []byte {
			b := make([]byte, n)
			for i := range b {
				b[i] = byte(r.Uint32())
			}
			return b
		},
		"zeros": func(n int) []byte { return make([]byte, n) },
		"text":  func(n int) []byte { return []byte(strings.Repeat("BTOON is MessagePack with tables. ", n/34+1)[:n]) },
		"runs": func(n int) []byte {
			b := make([]byte, n)
			for i := range b {
				if i > 0 && r.IntN(4) != 0 {
					b[i] = b[i-1-r.IntN(min(i, 16))]
				} else {
					b[i] = byte(r.IntN(4))
				}
			}
			return b
		},
	}
	sizes := []int{100_000, 1 << 20}
	for n := 0; n <= 600; n++ {
		sizes = append(sizes, n)
	}
	for name, gen := range inputs {
		for _, n := range sizes {
			data := gen(n)
			for _, level := range []int{0, 1, 9} {
				if n > 600 && level == 1 {
					continue
				}
				block, err := LZ4.Compress(data, level)
				if err != nil {
					t.Fatal(err)
				}
				out, err := LZ4.Decompress(block, n)
				if err != nil {
					t.Fatalf("%s, %d bytes, level %d: %v (block %x)", name, n, level, err, block[:min(len(block), 64)])
				}
				if !bytes.Equal(out, data) {
					t.Fatalf("%s, %d bytes, level %d: round trip changed the data", name, n, level)
				}
			}
		}
	}
}

// lz4EndOfBlockOK checks a block against SPEC §6 by walking its sequences,
// without decoding: a second, plainer statement of the rules that
// decodeLZ4Block enforces. Verdicts don't depend on the bytes the matches
// copy, so the walk decides them completely.
func lz4EndOfBlockOK(src []byte, size int) bool {
	pos, i := 0, 0 // output and input positions
	length := func(n int) (int, bool) {
		for {
			if i >= len(src) {
				return 0, false
			}
			b := src[i]
			i++
			n += int(b)
			if n > len(src)+size {
				return 0, false // can't fit; also bounds n
			}
			if b != 255 {
				return n, true
			}
		}
	}
	if len(src) == 0 {
		return false
	}
	for {
		if i >= len(src) {
			return false // the block ends with a match
		}
		token := src[i]
		i++
		lit := int(token >> 4)
		if lit == 15 {
			var ok bool
			if lit, ok = length(lit); !ok {
				return false
			}
		}
		if lit > len(src)-i {
			return false
		}
		i += lit
		pos += lit
		if i == len(src) {
			return pos == size // the last sequence: literals only
		}
		if len(src)-i < 2 {
			return false
		}
		offset := int(src[i]) | int(src[i+1])<<8
		i += 2
		n := int(token & 15)
		if n == 15 {
			var ok bool
			if n, ok = length(n); !ok {
				return false
			}
		}
		n += 4
		switch {
		case offset == 0 || offset > pos: // invalid, or before the start of the output
			return false
		case pos+12 > size: // starts less than 12 bytes before the end
			return false
		case pos+n+5 > size: // ends less than 5 bytes before the end
			return false
		}
		pos += n
	}
}

func FuzzLZ4Block(f *testing.F) {
	for _, c := range lz4Cases {
		block, _ := hex.DecodeString(c.block)
		f.Add(block, uint16(c.size))
	}
	compressed, _ := LZ4.Compress([]byte(strings.Repeat("hello lz4 block ", 40)), 0)
	f.Add(compressed, uint16(640))
	f.Fuzz(func(t *testing.T, block []byte, size uint16) {
		out, err := LZ4.Decompress(block, int(size))
		ok := lz4EndOfBlockOK(block, int(size))
		if (err == nil) != ok {
			t.Fatalf("decoder: %v, walk: %v (block %x, size %d)", err, ok, block, size)
		}
		if err == nil && len(out) != int(size) {
			t.Fatalf("decoded %d bytes, want %d", len(out), size)
		}
	})
}

func FuzzLZ4RoundTrip(f *testing.F) {
	f.Add([]byte("hello hello hello hello hello hello hello!"), uint8(0))
	f.Add(bytes.Repeat([]byte{0}, 300), uint8(9))
	f.Fuzz(func(t *testing.T, data []byte, level uint8) {
		block, err := LZ4.Compress(data, int(level%10))
		if err != nil {
			t.Fatal(err)
		}
		out, err := LZ4.Decompress(block, len(data))
		if err != nil {
			t.Fatalf("level %d: %v", level%10, err)
		}
		if !bytes.Equal(out, data) {
			t.Fatal("round trip changed the data")
		}
	})
}
