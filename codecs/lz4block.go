package codecs

import (
	"errors"
	"fmt"
)

// LZ4 block format constants (lz4_Block_format.md).
const (
	lz4MinMatch     = 4  // a match copies at least 4 bytes
	lz4LastLiterals = 5  // the last 5 bytes of output are literals
	lz4MFLimit      = 12 // a match starts at least 12 bytes before the end
	// lz4MaxSize is the largest output liblz4 decodes (LZ4_MAX_INPUT_SIZE).
	// btoon-core rejects larger envelopes, so this decoder does too.
	lz4MaxSize = 0x7E000000
)

var (
	errLZ4Truncated = errors.New("lz4: block is truncated")
	errLZ4Overrun   = errors.New("lz4: block writes past the declared size")
	errLZ4Offset    = errors.New("lz4: match offset is 0 or points before the start of the output")
	errLZ4EndMatch  = errors.New("lz4: a match must start at least 12 bytes before the end of the output")
	errLZ4EndLits   = errors.New("lz4: the last 5 bytes of the output must be literals")
	errLZ4LastSeq   = errors.New("lz4: block does not end with a sequence of literals only")
)

// decodeLZ4Block decompresses src, an LZ4 block (lz4_Block_format.md),
// which must expand to exactly size bytes. It enforces the block format's
// end-of-block conditions, as SPEC §6 requires:
//
//   - The block is a series of sequences. Each holds literals and then a
//     match, except the last, which holds only literals and ends exactly at
//     the end of src. The match-length bits of its token are ignored.
//   - Every match starts at least 12 bytes before the end of the output and
//     ends at least 5 bytes before it, so the last 5 bytes are literals.
//   - A match's offset is not 0, which the block format calls invalid, and
//     reaches back at most to the start of the output.
//
// liblz4's LZ4_decompress_safe reaches the same verdicts and output except
// on three kinds of block, where SPEC §6 decides. A decoder built on it,
// such as btoon-core's, needs a check of its own:
//
//   - liblz4 skips the end-of-block checks for a match it copies on its
//     fast path, which a sequence can take while at least 32 bytes of output
//     remain. It so accepts some blocks whose last match ends within the
//     last 5 bytes of the output.
//   - It decodes a match with offset 0 as zeros.
//   - For an empty output, it accepts only the block 00, not the other
//     single tokens 01 to 0f, whose match-length bits SPEC §6 ignores.
//
// No LZ4 compressor writes such blocks.
func decodeLZ4Block(src []byte, size int) ([]byte, error) {
	if len(src) == 0 {
		return nil, errLZ4Truncated
	}
	// A byte of input yields at most 255 bytes of output, so larger claims
	// are impossible; rejecting them avoids allocating for a lying header.
	if uint64(size) > uint64(len(src))*255+16 || size > lz4MaxSize {
		return nil, fmt.Errorf("lz4: %d bytes cannot expand to %d", len(src), size)
	}
	dst := make([]byte, size)
	si, di := 0, 0
	for {
		if si >= len(src) {
			// Only reachable after a match: the block ends with one.
			return nil, errLZ4LastSeq
		}
		token := src[si]
		si++

		lit := int(token >> 4)
		if lit == 15 {
			// Stopping as soon as the length exceeds the input also keeps it
			// from overflowing an int on 32-bit platforms.
			for {
				if si >= len(src) {
					return nil, errLZ4Truncated
				}
				b := src[si]
				si++
				if lit += int(b); lit > len(src) {
					return nil, errLZ4Truncated
				}
				if b != 255 {
					break
				}
			}
		}
		if lit > len(src)-si {
			return nil, errLZ4Truncated
		}
		if lit > size-di {
			return nil, errLZ4Overrun
		}
		copy(dst[di:], src[si:si+lit])
		si += lit
		di += lit
		if si == len(src) {
			break // the last sequence: literals only
		}

		// A match follows the literals.
		if di > size-lz4MFLimit {
			return nil, errLZ4EndMatch
		}
		if len(src)-si < 2 {
			return nil, errLZ4Truncated
		}
		offset := int(src[si]) | int(src[si+1])<<8
		si += 2
		if offset == 0 || offset > di {
			return nil, errLZ4Offset
		}
		n := int(token & 15)
		if n == 15 {
			for {
				if si >= len(src) {
					return nil, errLZ4Truncated
				}
				b := src[si]
				si++
				if n += int(b); n > size {
					return nil, errLZ4EndLits
				}
				if b != 255 {
					break
				}
			}
		}
		n += lz4MinMatch
		if n > size-lz4LastLiterals-di {
			return nil, errLZ4EndLits
		}
		copyMatch(dst, di, offset, n)
		di += n
	}
	if di != size {
		return nil, errSize
	}
	return dst, nil
}

// copyMatch copies n bytes to dst[di:] from offset bytes back (0 < offset
// <= di). When the match overlaps its own output, it repeats the last
// offset bytes; each copy then doubles the span copied so far.
func copyMatch(dst []byte, di, offset, n int) {
	from, end := di-offset, di+n
	if offset >= n {
		copy(dst[di:end], dst[from:from+n])
		return
	}
	for di < end {
		di += copy(dst[di:end], dst[from:di])
	}
}
