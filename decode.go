package btoon

import (
	"encoding/binary"
	"math"
	"math/big"
	"slices"
	"unicode/utf8"
)

// decoder parses one bare BTOON document (SPEC §3–§7). Invariants:
//   - every read is bounds-checked, so hostile input cannot cause a panic;
//   - containers are depth-limited, so recursion is bounded by maxDepth;
//   - nothing is allocated for a container before checking that the
//     remaining input could hold its elements, and what all containers
//     allocate before their elements are decoded is bounded by the size of
//     the document (see reserve);
//   - the document decodes to at most max_values values (see value), which
//     bounds the memory the decoded value takes (SPEC §7.1).
type decoder struct {
	data      []byte
	pos       int
	strict    bool
	maxDepth  int
	timeTypes bool // decode timestamps/dates/datetimes as Timestamp/Date/DateTime
	budget    int  // slots containers may still reserve up front (see reserve)
	values    int  // values the document may still decode to (max_values)
	// maxNameBytes bounds rows × column-name bytes of a tabular array
	// (max_decompressed_size, SPEC §5.6.3).
	maxNameBytes uint64
}

// newDecoder returns a decoder for the bare document data.
func newDecoder(data []byte, c *decodeConfig) decoder {
	return decoder{
		data: data, strict: c.strict, maxDepth: c.maxDepth, timeTypes: c.timeTypes,
		budget: len(data), values: c.maxValues, maxNameBytes: uint64(c.maxDecompressed),
	}
}

// count charges n values against max_values (SPEC §7.1).
func (d *decoder) count(n uint64, offset int) error {
	if n > uint64(d.values) {
		return errTooManyValues(offset)
	}
	d.values -= int(n)
	return nil
}

func errTooManyValues(offset int) error {
	return errorAt(CodeSizeLimit, "the document decodes to more values than max_values allows", offset)
}

// reserve returns how many slots a container that announces n elements may
// allocate before decoding them: all n while the document's budget covers
// them, otherwise none, and the container grows as its elements arrive.
//
// Checking n against the remaining input isn't enough on its own: nested
// containers check against the same remaining bytes at every level, so 127
// nested array32 headers in 1 MiB reserved 2 GiB (review 2026-11-13, X1).
// The budget starts at the document's size in bytes and is never refunded.
// Every element takes at least one byte of its own (an array element or
// table cell its first byte, a map entry its key), so the elements of a
// valid document never use more than the budget, and only containers that
// announce more elements than they hold make others grow. Reservations
// therefore stay proportional to the input at any nesting depth.
//
// Each element is also a value (SPEC §7.1), so a container that announces
// more elements than max_values has left fails before it fills its slots.
// It doesn't reserve them either: a slot takes 16 bytes, and a 64 MiB
// document announcing 64 Mi elements would otherwise reserve 1 GiB.
func (d *decoder) reserve(n uint64) int {
	if n > uint64(d.budget) || n > uint64(d.values) {
		return 0
	}
	d.budget -= int(n)
	return int(n)
}

// grow returns s with room for one more of the n elements it will hold. A
// full slice doubles (up to n), so its capacity stays within about twice
// the elements decoded so far.
func grow[T any](s []T, n uint64) []T {
	if len(s) < cap(s) {
		return s
	}
	return slices.Grow(s, int(min(uint64(max(len(s), 16)), n-uint64(len(s)))))
}

func (d *decoder) remaining() int { return len(d.data) - d.pos }

func (d *decoder) errTruncated() error {
	return errorAt(CodeTruncated, "unexpected end of input", d.pos)
}

// need reports a truncation error unless n more bytes are available.
func (d *decoder) need(n uint64) error {
	if n > uint64(d.remaining()) {
		return d.errTruncated()
	}
	return nil
}

func (d *decoder) u8() (uint64, error) {
	if d.pos >= len(d.data) {
		return 0, d.errTruncated()
	}
	b := d.data[d.pos]
	d.pos++
	return uint64(b), nil
}

func (d *decoder) u16() (uint64, error) {
	if d.remaining() < 2 {
		return 0, d.errTruncated()
	}
	v := binary.BigEndian.Uint16(d.data[d.pos:])
	d.pos += 2
	return uint64(v), nil
}

func (d *decoder) u32() (uint64, error) {
	if d.remaining() < 4 {
		return 0, d.errTruncated()
	}
	v := binary.BigEndian.Uint32(d.data[d.pos:])
	d.pos += 4
	return uint64(v), nil
}

func (d *decoder) u64() (uint64, error) {
	if d.remaining() < 8 {
		return 0, d.errTruncated()
	}
	v := binary.BigEndian.Uint64(d.data[d.pos:])
	d.pos += 8
	return v, nil
}

// document decodes the single value that must make up the whole input.
func (d *decoder) document() (any, error) {
	v, err := d.value(0)
	if err != nil {
		return nil, err
	}
	if d.strict && d.pos != len(d.data) {
		return nil, errorAt(CodeTrailingData, "trailing bytes after the value", d.pos)
	}
	return v, nil
}

func (d *decoder) value(depth int) (any, error) {
	// A value counts when it starts, before its marker is examined and
	// before any value inside it (SPEC §7.1).
	if d.values == 0 {
		return nil, errTooManyValues(d.pos)
	}
	d.values--
	if d.pos >= len(d.data) {
		return nil, d.errTruncated()
	}
	start := d.pos
	m := d.data[d.pos]
	d.pos++
	switch {
	case m <= 0x7f:
		return int64(m), nil
	case m >= 0xe0:
		return int64(int8(m)), nil
	case m <= 0x8f:
		return d.mapValue(uint64(m&0x0f), depth, start)
	case m <= 0x9f:
		return d.arrayValue(uint64(m&0x0f), depth, start)
	case m <= 0xbf:
		return d.str(uint64(m & 0x1f))
	}

	var n uint64
	var err error
	switch m {
	case 0xc0:
		return nil, nil
	case 0xc1:
		return nil, errorAt(CodeInvalidMarker, "reserved marker 0xc1", start)
	case 0xc2:
		return false, nil
	case 0xc3:
		return true, nil

	case 0xc4, 0xc5, 0xc6: // bin 8/16/32
		if n, err = d.length(m - 0xc4); err != nil {
			return nil, err
		}
		return d.bin(n)

	case 0xc7, 0xc8, 0xc9: // ext 8/16/32
		if n, err = d.length(m - 0xc7); err != nil {
			return nil, err
		}
		return d.ext(n, depth, start)

	case 0xca:
		if n, err = d.u32(); err != nil {
			return nil, err
		}
		return float64(math.Float32frombits(uint32(n))), nil
	case 0xcb:
		if n, err = d.u64(); err != nil {
			return nil, err
		}
		return math.Float64frombits(n), nil

	case 0xcc, 0xcd, 0xce, 0xcf: // uint 8/16/32/64
		if n, err = d.length(m - 0xcc); err != nil {
			return nil, err
		}
		if n > math.MaxInt64 {
			return n, nil
		}
		return int64(n), nil

	case 0xd0:
		if n, err = d.u8(); err != nil {
			return nil, err
		}
		return int64(int8(n)), nil
	case 0xd1:
		if n, err = d.u16(); err != nil {
			return nil, err
		}
		return int64(int16(n)), nil
	case 0xd2:
		if n, err = d.u32(); err != nil {
			return nil, err
		}
		return int64(int32(n)), nil
	case 0xd3:
		if n, err = d.u64(); err != nil {
			return nil, err
		}
		return int64(n), nil

	case 0xd4, 0xd5, 0xd6, 0xd7, 0xd8: // fixext 1/2/4/8/16
		return d.ext(1<<(m-0xd4), depth, start)

	case 0xd9, 0xda, 0xdb: // str 8/16/32
		if n, err = d.length(m - 0xd9); err != nil {
			return nil, err
		}
		return d.str(n)

	case 0xdc, 0xdd: // array 16/32
		if n, err = d.length(m - 0xdc + 1); err != nil {
			return nil, err
		}
		return d.arrayValue(n, depth, start)

	case 0xde, 0xdf: // map 16/32
		if n, err = d.length(m - 0xde + 1); err != nil {
			return nil, err
		}
		return d.mapValue(n, depth, start)
	}
	panic("unreachable")
}

// length reads a big-endian unsigned integer of 1, 2, 4 or 8 bytes
// (selected by size = 0, 1, 2, 3).
func (d *decoder) length(size byte) (uint64, error) {
	switch size {
	case 0:
		return d.u8()
	case 1:
		return d.u16()
	case 2:
		return d.u32()
	default:
		return d.u64()
	}
}

func (d *decoder) str(n uint64) (string, error) {
	if err := d.need(n); err != nil {
		return "", err
	}
	b := d.data[d.pos : d.pos+int(n)]
	// Non-strict mode skips UTF-8 validation of string values only; keys
	// and column names are always checked.
	if d.strict && !utf8.Valid(b) {
		return "", errorAt(CodeInvalidUTF8, "string is not valid UTF-8", d.pos)
	}
	d.pos += int(n)
	return string(b), nil
}

func (d *decoder) bin(n uint64) ([]byte, error) {
	if err := d.need(n); err != nil {
		return nil, err
	}
	b := make([]byte, n)
	copy(b, d.data[d.pos:])
	d.pos += int(n)
	return b, nil
}

func (d *decoder) arrayValue(n uint64, depth, start int) (any, error) {
	if depth >= d.maxDepth {
		return nil, errorAt(CodeMaxDepth, "maximum nesting depth exceeded", start)
	}
	// Every element takes at least one byte.
	if n > uint64(d.remaining()) {
		return nil, errorAt(CodeTruncated, "array length exceeds the remaining input", start)
	}
	if uint64(d.reserve(n)) == n {
		a := make([]any, n)
		for i := range a {
			v, err := d.value(depth + 1)
			if err != nil {
				return nil, err
			}
			a[i] = v
		}
		return a, nil
	}
	var a []any
	for i := uint64(0); i < n; i++ {
		v, err := d.value(depth + 1)
		if err != nil {
			return nil, err
		}
		a = append(grow(a, n), v)
	}
	return a, nil
}

func (d *decoder) mapValue(n uint64, depth, start int) (any, error) {
	if depth >= d.maxDepth {
		return nil, errorAt(CodeMaxDepth, "maximum nesting depth exceeded", start)
	}
	// Every key/value pair takes at least two bytes.
	if n > uint64(d.remaining())/2 {
		return nil, errorAt(CodeTruncated, "map length exceeds the remaining input", start)
	}
	m := make(map[string]any, d.reserve(n))
	for i := uint64(0); i < n; i++ {
		keyPos := d.pos
		k, err := d.key()
		if err != nil {
			return nil, err
		}
		v, err := d.value(depth + 1)
		if err != nil {
			return nil, err
		}
		if _, dup := m[k]; dup && d.strict {
			return nil, errorAt(CodeDuplicateKey, "duplicate map key "+quote(k), keyPos)
		}
		m[k] = v // non-strict: the last value wins
	}
	return m, nil
}

func (d *decoder) key() (string, error) {
	if d.pos >= len(d.data) {
		return "", d.errTruncated()
	}
	m := d.data[d.pos]
	var n uint64
	var err error
	switch {
	case m >= 0xa0 && m <= 0xbf:
		d.pos++
		n = uint64(m & 0x1f)
	case m >= 0xd9 && m <= 0xdb:
		d.pos++
		if n, err = d.length(m - 0xd9); err != nil {
			return "", err
		}
	default:
		return "", errorAt(CodeNonStringKey, "map key is not a string", d.pos)
	}
	if err := d.need(n); err != nil {
		return "", err
	}
	b := d.data[d.pos : d.pos+int(n)]
	// Keys must be UTF-8 in non-strict mode too. Keys are compared by their
	// bytes, and replacing invalid sequences could merge two keys that
	// differ on the wire (SPEC §7).
	if !utf8.Valid(b) {
		return "", errorAt(CodeInvalidUTF8, "map key is not valid UTF-8", d.pos)
	}
	d.pos += int(n)
	return string(b), nil
}

// ext decodes an extension whose payload is n bytes long; d.pos is at the
// type byte.
func (d *decoder) ext(n uint64, depth, start int) (any, error) {
	t, err := d.u8()
	if err != nil {
		return nil, err
	}
	if err := d.need(n); err != nil {
		return nil, err
	}
	base := d.pos
	p := d.data[base : base+int(n)]
	typ := int8(t)
	bad := func(msg string) error { return errorAt(CodeInvalidExtension, msg, start) }

	var v any
	switch typ {
	case extTimestamp:
		var sec int64
		var nsec uint32
		var off int16
		hasOff := false
		switch len(p) {
		case 4:
			sec = int64(binary.BigEndian.Uint32(p))
		case 8:
			data64 := binary.BigEndian.Uint64(p)
			nsec = uint32(data64 >> 34)
			sec = int64(data64 & (1<<34 - 1))
		case 12, 14:
			nsec = binary.BigEndian.Uint32(p)
			sec = int64(binary.BigEndian.Uint64(p[4:]))
			if len(p) == 14 {
				hasOff = true
				off = int16(binary.BigEndian.Uint16(p[12:]))
				if off < -1080 || off > 1080 {
					return nil, bad("timestamp offset out of range")
				}
			}
		default:
			return nil, bad("timestamp payload must be 4, 8, 12 or 14 bytes")
		}
		if nsec >= 1e9 {
			return nil, bad("timestamp nanoseconds out of range")
		}
		v = d.timestamp(sec, nsec, hasOff, off)

	case extDate, extDateTime:
		if len(p) != 8 {
			if typ == extDate {
				return nil, bad("date payload must be 8 bytes")
			}
			return nil, bad("datetime payload must be 8 bytes")
		}
		x := int64(binary.BigEndian.Uint64(p))
		switch {
		case typ == extDate && d.timeTypes:
			v = Date(x)
		case typ == extDate:
			v = Date(x).Time()
		case d.timeTypes:
			v = DateTime(x)
		default:
			v = DateTime(x).Time()
		}

	case extBigInt:
		if len(p) == 0 {
			return nil, bad("bigint payload is empty")
		}
		v = bigIntFromBytes(p)

	case extVectorF32:
		if len(p)%4 != 0 {
			return nil, bad("float32 vector length is not a multiple of 4")
		}
		f := make([]float32, len(p)/4)
		for i := range f {
			f[i] = math.Float32frombits(binary.LittleEndian.Uint32(p[4*i:]))
		}
		v = f

	case extVectorF64:
		if len(p)%8 != 0 {
			return nil, bad("float64 vector length is not a multiple of 8")
		}
		f := make(VectorF64, len(p)/8)
		for i := range f {
			f[i] = math.Float64frombits(binary.LittleEndian.Uint64(p[8*i:]))
		}
		v = f

	case extTabular:
		v, err = d.tabular(base, base+len(p), depth, start)
		if err != nil {
			return nil, err
		}

	default:
		v = Ext{Type: typ, Data: append([]byte{}, p...)}
	}
	d.pos = base + len(p)
	return v, nil
}

func (d *decoder) timestamp(sec int64, nsec uint32, hasOff bool, off int16) any {
	ts := Timestamp{Seconds: sec, Nanoseconds: nsec, HasOffset: hasOff, OffsetMinutes: off}
	if d.timeTypes || sec > maxTimeSeconds {
		return ts
	}
	return ts.Time()
}

// bigIntFromBytes interprets p (non-empty) as a big-endian two's-complement
// integer.
func bigIntFromBytes(p []byte) *big.Int {
	x := new(big.Int).SetBytes(p)
	if p[0]&0x80 != 0 {
		x.Sub(x, new(big.Int).Lsh(big.NewInt(1), uint(8*len(p))))
	}
	return x
}

// tabular decodes the tabular payload data[start:end] (SPEC §5.6). The
// array is at depth, its rows at depth+1 and the cells at depth+2.
func (d *decoder) tabular(start, end, depth, extPos int) (any, error) {
	if depth+1 >= d.maxDepth {
		return nil, errorAt(CodeMaxDepth, "maximum nesting depth exceeded", extPos)
	}
	p := d.data[:end]
	pos := start
	bad := func(msg string) error { return errorAt(CodeInvalidTabular, msg, pos) }
	left := func() uint64 { return uint64(end - pos) }
	u32 := func() (uint64, bool) {
		if left() < 4 {
			return 0, false
		}
		v := binary.BigEndian.Uint32(p[pos:])
		pos += 4
		return uint64(v), true
	}

	if left() < 12 {
		return nil, bad("tabular header truncated")
	}
	version, _ := u32()
	numCols, _ := u32()
	numRows, _ := u32()
	if version != 1 {
		return nil, bad("unsupported tabular version " + utoa(version))
	}
	if numCols == 0 && numRows > 0 {
		return nil, bad("tabular rows without columns")
	}
	// Each column needs at least 9 bytes: name length, type code, column length.
	if numCols > left()/9 {
		return nil, bad("tabular column count exceeds the payload")
	}

	// Names are checked front to back (SPEC §5.6.3): each must be valid
	// UTF-8, in non-strict mode too, and differ from every earlier name. The
	// first name that fails determines the error.
	names := make([]string, numCols)
	var seen map[string]struct{}
	if numCols > 1 {
		seen = make(map[string]struct{}, numCols)
	}
	var nameBytes uint64
	for i := range names {
		l, ok := u32()
		if !ok || l > left() {
			return nil, bad("tabular column name runs past the payload")
		}
		nb := p[pos : pos+int(l)]
		if !utf8.Valid(nb) {
			return nil, errorAt(CodeInvalidUTF8, "column name is not valid UTF-8", pos)
		}
		pos += int(l)
		names[i] = string(nb)
		nameBytes += l
		if seen != nil {
			if _, dup := seen[names[i]]; dup {
				return nil, bad("duplicate column name " + quote(names[i]))
			}
			seen[names[i]] = struct{}{}
		}
	}
	if numCols > left() {
		return nil, bad("tabular type codes run past the payload")
	}
	pos += int(numCols) // type codes are informational (SPEC §5.6.1)

	// Then, in this order (SPEC §5.6.3): every cell is at least one byte, so
	// N×C cells must fit in what is left;
	numCells := numRows * numCols
	if numCells > left() {
		return nil, bad("tabular row count exceeds the payload")
	}
	// each row map holds every column name, so rows × name bytes is bounded
	// as a plain array of maps would be (both factors are below 2^32);
	if numRows*nameBytes > d.maxNameBytes {
		return nil, errorAt(CodeSizeLimit, "tabular column names copied into every row exceed max_decompressed_size", pos)
	}
	// and the N row maps count against max_values. Each cell counts as it
	// starts.
	if err := d.count(numRows, pos); err != nil {
		return nil, err
	}
	// Each column is decoded by its own decoder, bounded by the column's
	// length and sharing the document's reservation and value budgets.
	openColumn := func() (col decoder, colEnd int, err error) {
		colLen, ok := u32()
		if !ok || colLen > left() {
			return decoder{}, 0, bad("tabular column runs past the payload")
		}
		colEnd = pos + int(colLen)
		col = *d
		col.data, col.pos = d.data[:colEnd], pos
		return col, colEnd, nil
	}
	closeColumn := func(col *decoder, colEnd int) error {
		if col.pos != colEnd {
			return errorAt(CodeInvalidTabular, "tabular column length does not match its values", col.pos)
		}
		d.budget, d.values = col.budget, col.values
		pos = colEnd
		return nil
	}
	cellError := func(err error) error {
		if e, ok := err.(*Error); ok && e.Code == CodeTruncated {
			return errorAt(CodeInvalidTabular, "tabular column holds fewer values than rows", e.Offset)
		}
		return err
	}

	var out []any
	if uint64(d.reserve(numCells)) == numCells {
		// The budget covers the cells: create the rows up front, sized for
		// their columns, and store each cell in its row.
		rows := make([]map[string]any, numRows)
		for i := range rows {
			rows[i] = make(map[string]any, numCols)
		}
		for _, name := range names {
			col, colEnd, err := openColumn()
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				v, err := col.value(depth + 2)
				if err != nil {
					return nil, cellError(err)
				}
				row[name] = v
			}
			if err := closeColumn(&col, colEnd); err != nil {
				return nil, err
			}
		}
		out = make([]any, numRows)
		for i, row := range rows {
			out[i] = row
		}
	} else {
		// The header announces more cells than the budget allows, which a
		// valid document never does: collect the cells as they are decoded,
		// and build the rows once all of them are in.
		var cells []any
		for range names {
			col, colEnd, err := openColumn()
			if err != nil {
				return nil, err
			}
			for i := uint64(0); i < numRows; i++ {
				v, err := col.value(depth + 2)
				if err != nil {
					return nil, cellError(err)
				}
				cells = append(grow(cells, numCells), v)
			}
			if err := closeColumn(&col, colEnd); err != nil {
				return nil, err
			}
		}
		out = make([]any, numRows)
		for i := range out {
			row := make(map[string]any, numCols)
			for c, name := range names {
				row[name] = cells[uint64(c)*numRows+uint64(i)]
			}
			out[i] = row
		}
	}
	if pos != end {
		return nil, bad("trailing bytes in tabular payload")
	}
	return out, nil
}
