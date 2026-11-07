package btoon

import (
	"encoding"
	"encoding/binary"
	"math"
	"math/big"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// maxIndirections bounds chains of pointers and interfaces without an
// intervening container, which can only be infinite through a cycle such as
// x = &x.
const maxIndirections = 1000

type encoder struct {
	buf        []byte
	maxDepth   int
	noTabular  bool
	keepOffset bool
}

// entry is one key/value pair of a map-like Go value.
type entry struct {
	key string
	val reflect.Value
}

func (e *encoder) encode(v reflect.Value, depth int) error {
	v, err := indirect(v)
	if err != nil {
		return err
	}
	if !v.IsValid() {
		e.buf = append(e.buf, 0xc0)
		return nil
	}
	return e.encodeConcrete(v, depth)
}

// indirect follows pointers and interfaces. It returns the zero Value for a
// nil pointer or interface, and stops at *big.Int.
func indirect(v reflect.Value) (reflect.Value, error) {
	for hops := 0; v.IsValid(); hops++ {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return reflect.Value{}, nil
			}
			if v.Type() == bigIntPtrType {
				return v, nil
			}
		case reflect.Interface:
			if v.IsNil() {
				return reflect.Value{}, nil
			}
		default:
			return v, nil
		}
		if hops == maxIndirections {
			return v, newError(CodeMaxDepth, "too many pointer indirections (cyclic pointer?)")
		}
		v = v.Elem()
	}
	return v, nil
}

// encodeConcrete encodes a value that is not a nil pointer or interface.
func (e *encoder) encodeConcrete(v reflect.Value, depth int) error {
	ti := typeInfoOf(v.Type())
	switch ti.special {
	case specialBigIntPtr:
		return e.writeBigInt(v.Interface().(*big.Int))
	case specialBigInt:
		if v.CanAddr() {
			return e.writeBigInt(v.Addr().Interface().(*big.Int))
		}
		x := v.Interface().(big.Int)
		return e.writeBigInt(&x)
	case specialTime:
		// Going through the address avoids boxing a copy of the value.
		if v.CanAddr() {
			return e.writeTime(*v.Addr().Interface().(*time.Time))
		}
		return e.writeTime(v.Interface().(time.Time))
	case specialTimestamp:
		var ts Timestamp
		if v.CanAddr() {
			ts = *v.Addr().Interface().(*Timestamp)
		} else {
			ts = v.Interface().(Timestamp)
		}
		return e.writeTimestamp(ts.Seconds, ts.Nanoseconds, ts.HasOffset, ts.OffsetMinutes)
	case specialDate:
		e.buf = append(e.buf, 0xd7, byte(0x100+extDate))
		e.buf = binary.BigEndian.AppendUint64(e.buf, uint64(v.Int()))
		return nil
	case specialDateTime:
		e.buf = append(e.buf, 0xd7, byte(0x100+extDateTime))
		e.buf = binary.BigEndian.AppendUint64(e.buf, uint64(v.Int()))
		return nil
	case specialExt:
		x := v.Interface().(Ext)
		if isAssignedExt(x.Type) {
			return newError(CodeInvalidArgument, "btoon.Ext cannot use type id "+itoa(int64(x.Type))+", which BTOON assigns (use the corresponding Go type)")
		}
		return e.writeExt(x.Type, x.Data)
	case specialVectorF64:
		if v.IsNil() {
			e.buf = append(e.buf, 0xc0)
			return nil
		}
		return e.writeVectorF64(v.Interface().(VectorF64))
	}

	if ti.marshaler || (ti.addrMarshaler && v.CanAddr()) {
		m := v
		if !ti.marshaler {
			m = v.Addr()
		}
		b, err := m.Interface().(encoding.BinaryMarshaler).MarshalBinary()
		if err != nil {
			return wrapError(CodeInvalidArgument, "MarshalBinary of "+v.Type().String()+" failed", err)
		}
		return e.writeBinary(b)
	}

	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			e.buf = append(e.buf, 0xc3)
		} else {
			e.buf = append(e.buf, 0xc2)
		}
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		e.writeInt(v.Int())
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		e.writeUint(v.Uint())
		return nil
	case reflect.Float32, reflect.Float64:
		e.writeFloat(v.Float())
		return nil
	case reflect.String:
		return e.writeString(v.String())
	case reflect.Slice:
		if v.IsNil() {
			e.buf = append(e.buf, 0xc0)
			return nil
		}
		switch v.Type().Elem().Kind() {
		case reflect.Uint8:
			return e.writeBinary(v.Bytes())
		case reflect.Float32:
			return e.writeVectorF32(v)
		}
		return e.encodeArray(v, depth)
	case reflect.Array:
		switch v.Type().Elem().Kind() {
		case reflect.Uint8:
			return e.writeBinary(addressable(v).Bytes())
		case reflect.Float32:
			return e.writeVectorF32(v)
		}
		return e.encodeArray(v, depth)
	case reflect.Map:
		if v.IsNil() {
			e.buf = append(e.buf, 0xc0)
			return nil
		}
		ents, err := mapEntries(v)
		if err != nil {
			return err
		}
		return e.writeMap(ents, depth)
	case reflect.Struct:
		return e.writeMap(structEntries(v), depth)
	}
	return newError(CodeInvalidArgument, "unsupported Go type "+v.Type().String())
}

// asMap reports whether v (not a nil pointer or interface) encodes as a map,
// and if so returns its entries in canonical key order.
func asMap(v reflect.Value) ([]entry, bool, error) {
	switch v.Kind() {
	case reflect.Map:
		if v.IsNil() {
			return nil, false, nil
		}
		ti := typeInfoOf(v.Type())
		if ti.marshaler || ti.addrMarshaler && v.CanAddr() {
			return nil, false, nil
		}
		ents, err := mapEntries(v)
		return ents, err == nil, err
	case reflect.Struct:
		ti := typeInfoOf(v.Type())
		if ti.special != specialNone || ti.marshaler || ti.addrMarshaler && v.CanAddr() {
			return nil, false, nil
		}
		return structEntries(v), true, nil
	}
	return nil, false, nil
}

// mapEntries returns the entries of a non-nil Go map, sorted by key.
func mapEntries(v reflect.Value) ([]entry, error) {
	var ents []entry
	if v.Type() == mapStringAnyType {
		m := v.Interface().(map[string]any)
		ents = make([]entry, 0, len(m))
		for k, x := range m {
			ents = append(ents, entry{k, reflect.ValueOf(x)})
		}
	} else {
		kt := v.Type().Key()
		dynamicKeys := false
		switch kt.Kind() {
		case reflect.String:
		case reflect.Interface:
			dynamicKeys = true
		default:
			return nil, newError(CodeInvalidArgument, "unsupported map key type "+kt.String()+" (BTOON map keys are strings)")
		}
		ents = make([]entry, 0, v.Len())
		it := v.MapRange()
		for it.Next() {
			k := it.Key()
			if dynamicKeys {
				if k.IsNil() || k.Elem().Kind() != reflect.String {
					return nil, newError(CodeNonStringKey, "map key of type "+describeKey(k)+" is not a string")
				}
				k = k.Elem()
			}
			ents = append(ents, entry{k.String(), it.Value()})
		}
		if dynamicKeys {
			slices.SortFunc(ents, compareEntries)
			for i := 1; i < len(ents); i++ {
				if ents[i].key == ents[i-1].key {
					return nil, newError(CodeDuplicateKey, "map has two keys that are both the string "+quote(ents[i].key))
				}
			}
			return ents, nil
		}
	}
	slices.SortFunc(ents, compareEntries)
	return ents, nil
}

func compareEntries(a, b entry) int { return strings.Compare(a.key, b.key) }

func describeKey(k reflect.Value) string {
	if k.IsNil() {
		return "nil"
	}
	return k.Elem().Type().String()
}

// structEntries returns the fields of struct v in canonical key order,
// applying omitempty/omitzero and skipping fields behind nil embedded
// pointers.
func structEntries(v reflect.Value) []entry {
	sf := structFieldsOf(v.Type())
	ents := make([]entry, 0, len(sf.sorted))
	for i := range sf.sorted {
		f := &sf.sorted[i]
		fv, ok := fieldByIndex(v, f.index)
		if !ok {
			continue
		}
		if f.omitEmpty && isEmptyValue(fv) {
			continue
		}
		if f.omitZero && f.isZero(fv) {
			continue
		}
		ents = append(ents, entry{f.name, fv})
	}
	return ents
}

func (e *encoder) writeMap(ents []entry, depth int) error {
	if depth >= e.maxDepth {
		return newError(CodeMaxDepth, "maximum nesting depth exceeded")
	}
	if err := e.writeMapHeader(len(ents)); err != nil {
		return err
	}
	for _, en := range ents {
		if err := e.writeString(en.key); err != nil {
			return err
		}
		if err := e.encode(en.val, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// encodeArray encodes a slice or array, using the tabular encoding when
// SPEC §5.6.2 says so.
func (e *encoder) encodeArray(v reflect.Value, depth int) error {
	if depth >= e.maxDepth {
		return newError(CodeMaxDepth, "maximum nesting depth exceeded")
	}
	n := v.Len()
	var rows [][]entry
	if !e.noTabular && n >= 2 {
		var uniform bool
		var err error
		rows, uniform, err = collectRows(v)
		if err != nil {
			return err
		}
		if uniform && tabularPays(rows[0], n) {
			return e.writeTabular(rows, depth)
		}
	}
	if err := e.writeArrayHeader(n); err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		var err error
		if i < len(rows) {
			err = e.writeMap(rows[i], depth+1)
		} else {
			err = e.encode(v.Index(i), depth+1)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// collectRows gathers the entries of the leading map-like elements of v.
// uniform reports whether every element is a map, the first map is not
// empty, and all maps have the same keys (the qualification test of SPEC
// §5.6.2). The returned rows can be reused for plain encoding either way.
func collectRows(v reflect.Value) (rows [][]entry, uniform bool, err error) {
	n := v.Len()
	uniform = true
	for i := 0; i < n; i++ {
		el, err := indirect(v.Index(i))
		if err != nil {
			return nil, false, err
		}
		if !el.IsValid() {
			return rows, false, nil
		}
		ents, ok, err := asMap(el)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return rows, false, nil
		}
		if i == 0 && len(ents) == 0 {
			uniform = false
		} else if i > 0 && uniform && !sameKeys(rows[0], ents) {
			uniform = false
		}
		rows = append(rows, ents)
	}
	return rows, uniform, nil
}

func sameKeys(a, b []entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].key != b[i].key {
			return false
		}
	}
	return true
}

func strHeaderSize(n uint64) uint64 {
	switch {
	case n <= 31:
		return 1
	case n <= 0xff:
		return 2
	case n <= 0xffff:
		return 3
	}
	return 5
}

func mapHeaderSize(n uint64) uint64 {
	switch {
	case n <= 15:
		return 1
	case n <= 0xffff:
		return 3
	}
	return 5
}

// tabularPays applies the size rule of SPEC §5.6.2:
//
//	N × (maphdr(C) + Σ (strhdr(len k) + len k))  >  18 + Σ (9 + len k)
func tabularPays(keys []entry, n int) bool {
	perRow := mapHeaderSize(uint64(len(keys)))
	overhead := uint64(18)
	for _, k := range keys {
		l := uint64(len(k.key))
		perRow += strHeaderSize(l) + l
		overhead += 9 + l
	}
	// N*perRow > overhead  <=>  perRow > floor(overhead/N), without overflow.
	return perRow > overhead/uint64(n)
}

// writeTabular writes rows (which share the same keys) as a tabular
// extension (SPEC §5.6.1).
func (e *encoder) writeTabular(rows [][]entry, depth int) error {
	// The array itself is at depth, its row maps at depth+1.
	if depth+1 >= e.maxDepth {
		return newError(CodeMaxDepth, "maximum nesting depth exceeded")
	}
	keys := rows[0]
	n, c := len(rows), len(keys)
	if uint64(n) > math.MaxUint32 || uint64(c) > math.MaxUint32 {
		return newError(CodeSizeLimit, "tabular array too large")
	}

	// Reserve the largest ext header (ext 32, 6 bytes) and shrink it once the
	// payload length is known.
	headerPos := len(e.buf)
	e.buf = append(e.buf, 0, 0, 0, 0, 0, 0)
	payloadPos := len(e.buf)
	e.buf = binary.BigEndian.AppendUint32(e.buf, 1) // version
	e.buf = binary.BigEndian.AppendUint32(e.buf, uint32(c))
	e.buf = binary.BigEndian.AppendUint32(e.buf, uint32(n))
	for _, k := range keys {
		if !utf8.ValidString(k.key) {
			return newError(CodeInvalidUTF8, "map key is not valid UTF-8: "+quote(k.key))
		}
		if uint64(len(k.key)) > math.MaxUint32 {
			return newError(CodeSizeLimit, "column name too long")
		}
		e.buf = binary.BigEndian.AppendUint32(e.buf, uint32(len(k.key)))
		e.buf = append(e.buf, k.key...)
	}
	codesPos := len(e.buf)
	e.buf = append(e.buf, make([]byte, c)...)

	for j := 0; j < c; j++ {
		lenPos := len(e.buf)
		e.buf = append(e.buf, 0, 0, 0, 0)
		for i := 0; i < n; i++ {
			cellPos := len(e.buf)
			if err := e.encode(rows[i][j].val, depth+2); err != nil {
				return err
			}
			if i == 0 {
				e.buf[codesPos+j] = typeCode(e.buf[cellPos:])
			}
		}
		colLen := len(e.buf) - lenPos - 4
		if uint64(colLen) > math.MaxUint32 {
			return newError(CodeSizeLimit, "tabular column too large")
		}
		binary.BigEndian.PutUint32(e.buf[lenPos:], uint32(colLen))
	}

	payloadLen := len(e.buf) - payloadPos
	if uint64(payloadLen) > math.MaxUint32 {
		return newError(CodeSizeLimit, "tabular payload too large")
	}
	hdr := appendExtHeader(make([]byte, 0, 6), extTabular, payloadLen)
	shift := 6 - len(hdr)
	copy(e.buf[headerPos+len(hdr):], e.buf[payloadPos:])
	copy(e.buf[headerPos:], hdr)
	e.buf = e.buf[:len(e.buf)-shift]
	return nil
}

// typeCode returns the tabular type code (SPEC §5.6.1) of the logical type of
// the canonical encoding enc. Deriving it from the encoding guarantees that
// it agrees with the value actually written.
func typeCode(enc []byte) byte {
	m := enc[0]
	switch {
	case m <= 0x7f || m >= 0xe0:
		return 2
	case m <= 0x8f:
		return 8
	case m <= 0x9f:
		return 7
	case m <= 0xbf:
		return 5
	}
	switch m {
	case 0xc0:
		return 0
	case 0xc2, 0xc3:
		return 1
	case 0xc4, 0xc5, 0xc6:
		return 6
	case 0xca, 0xcb:
		return 4
	case 0xcc, 0xcd, 0xce, 0xd0, 0xd1, 0xd2, 0xd3:
		return 2
	case 0xcf:
		if enc[1]&0x80 != 0 {
			return 3 // in [2^63, 2^64-1]
		}
		return 2
	case 0xd9, 0xda, 0xdb:
		return 5
	case 0xdc, 0xdd:
		return 7
	case 0xde, 0xdf:
		return 8
	}
	// Extensions: a nested tabular array is an array; everything else
	// (timestamps, bigints outside 64 bits, vectors, ...) is code 9.
	var t byte
	switch m {
	case 0xd4, 0xd5, 0xd6, 0xd7, 0xd8:
		t = enc[1]
	case 0xc7:
		t = enc[2]
	case 0xc8:
		t = enc[3]
	case 0xc9:
		t = enc[5]
	}
	if int8(t) == extTabular {
		return 7
	}
	return 9
}

// --- Primitives ----------------------------------------------------------------

func (e *encoder) writeInt(v int64) {
	if v >= 0 {
		e.writeUint(uint64(v))
		return
	}
	switch {
	case v >= -32:
		e.buf = append(e.buf, byte(v))
	case v >= math.MinInt8:
		e.buf = append(e.buf, 0xd0, byte(v))
	case v >= math.MinInt16:
		e.buf = append(e.buf, 0xd1)
		e.buf = binary.BigEndian.AppendUint16(e.buf, uint16(v))
	case v >= math.MinInt32:
		e.buf = append(e.buf, 0xd2)
		e.buf = binary.BigEndian.AppendUint32(e.buf, uint32(v))
	default:
		e.buf = append(e.buf, 0xd3)
		e.buf = binary.BigEndian.AppendUint64(e.buf, uint64(v))
	}
}

func (e *encoder) writeUint(v uint64) {
	switch {
	case v <= 0x7f:
		e.buf = append(e.buf, byte(v))
	case v <= math.MaxUint8:
		e.buf = append(e.buf, 0xcc, byte(v))
	case v <= math.MaxUint16:
		e.buf = append(e.buf, 0xcd)
		e.buf = binary.BigEndian.AppendUint16(e.buf, uint16(v))
	case v <= math.MaxUint32:
		e.buf = append(e.buf, 0xce)
		e.buf = binary.BigEndian.AppendUint32(e.buf, uint32(v))
	default:
		e.buf = append(e.buf, 0xcf)
		e.buf = binary.BigEndian.AppendUint64(e.buf, v)
	}
}

// canonicalNaN is the only NaN encoding BTOON encoders may emit (SPEC §4.2).
// Go's math.NaN() is 0x7ff8000000000001, so NaNs must be rewritten.
const canonicalNaN = 0x7ff8000000000000

func (e *encoder) writeFloat(f float64) {
	bits := math.Float64bits(f)
	if f != f {
		bits = canonicalNaN
	}
	e.buf = append(e.buf, 0xcb)
	e.buf = binary.BigEndian.AppendUint64(e.buf, bits)
}

func (e *encoder) writeString(s string) error {
	if !utf8.ValidString(s) {
		return newError(CodeInvalidUTF8, "string is not valid UTF-8: "+quote(s))
	}
	n := uint64(len(s))
	switch {
	case n <= 31:
		e.buf = append(e.buf, 0xa0|byte(n))
	case n <= math.MaxUint8:
		e.buf = append(e.buf, 0xd9, byte(n))
	case n <= math.MaxUint16:
		e.buf = append(e.buf, 0xda)
		e.buf = binary.BigEndian.AppendUint16(e.buf, uint16(n))
	case n <= math.MaxUint32:
		e.buf = append(e.buf, 0xdb)
		e.buf = binary.BigEndian.AppendUint32(e.buf, uint32(n))
	default:
		return newError(CodeSizeLimit, "string longer than 2^32-1 bytes")
	}
	e.buf = append(e.buf, s...)
	return nil
}

func (e *encoder) writeBinary(b []byte) error {
	n := uint64(len(b))
	switch {
	case n <= math.MaxUint8:
		e.buf = append(e.buf, 0xc4, byte(n))
	case n <= math.MaxUint16:
		e.buf = append(e.buf, 0xc5)
		e.buf = binary.BigEndian.AppendUint16(e.buf, uint16(n))
	case n <= math.MaxUint32:
		e.buf = append(e.buf, 0xc6)
		e.buf = binary.BigEndian.AppendUint32(e.buf, uint32(n))
	default:
		return newError(CodeSizeLimit, "binary longer than 2^32-1 bytes")
	}
	e.buf = append(e.buf, b...)
	return nil
}

func (e *encoder) writeArrayHeader(n int) error {
	u := uint64(n)
	switch {
	case u <= 15:
		e.buf = append(e.buf, 0x90|byte(u))
	case u <= math.MaxUint16:
		e.buf = append(e.buf, 0xdc)
		e.buf = binary.BigEndian.AppendUint16(e.buf, uint16(u))
	case u <= math.MaxUint32:
		e.buf = append(e.buf, 0xdd)
		e.buf = binary.BigEndian.AppendUint32(e.buf, uint32(u))
	default:
		return newError(CodeSizeLimit, "array longer than 2^32-1 elements")
	}
	return nil
}

func (e *encoder) writeMapHeader(n int) error {
	u := uint64(n)
	switch {
	case u <= 15:
		e.buf = append(e.buf, 0x80|byte(u))
	case u <= math.MaxUint16:
		e.buf = append(e.buf, 0xde)
		e.buf = binary.BigEndian.AppendUint16(e.buf, uint16(u))
	case u <= math.MaxUint32:
		e.buf = append(e.buf, 0xdf)
		e.buf = binary.BigEndian.AppendUint32(e.buf, uint32(u))
	default:
		return newError(CodeSizeLimit, "map larger than 2^32-1 entries")
	}
	return nil
}

// appendExtHeader appends the smallest header for an extension payload of n
// bytes (SPEC §4.3). n must not exceed 2^32-1.
func appendExtHeader(b []byte, typ int8, n int) []byte {
	t := byte(typ)
	switch n {
	case 1:
		return append(b, 0xd4, t)
	case 2:
		return append(b, 0xd5, t)
	case 4:
		return append(b, 0xd6, t)
	case 8:
		return append(b, 0xd7, t)
	case 16:
		return append(b, 0xd8, t)
	}
	switch u := uint64(n); {
	case u <= math.MaxUint8:
		return append(b, 0xc7, byte(u), t)
	case u <= math.MaxUint16:
		b = append(b, 0xc8)
		b = binary.BigEndian.AppendUint16(b, uint16(u))
		return append(b, t)
	default:
		b = append(b, 0xc9)
		b = binary.BigEndian.AppendUint32(b, uint32(u))
		return append(b, t)
	}
}

func (e *encoder) writeExt(typ int8, data []byte) error {
	if uint64(len(data)) > math.MaxUint32 {
		return newError(CodeSizeLimit, "extension payload longer than 2^32-1 bytes")
	}
	e.buf = appendExtHeader(e.buf, typ, len(data))
	e.buf = append(e.buf, data...)
	return nil
}

// --- BTOON extension types --------------------------------------------------------

func (e *encoder) writeTime(t time.Time) error {
	sec, nsec := t.Unix(), uint32(t.Nanosecond())
	if e.keepOffset && t.Location() != time.UTC {
		_, off := t.Zone()
		if off%60 != 0 {
			return newError(CodeInvalidArgument, "UTC offset of "+t.String()+" is not a whole number of minutes")
		}
		if off/60 < -1080 || off/60 > 1080 {
			return newError(CodeInvalidArgument, "UTC offset of "+t.String()+" exceeds 18 hours")
		}
		return e.writeTimestamp(sec, nsec, true, int16(off/60))
	}
	return e.writeTimestamp(sec, nsec, false, 0)
}

// writeTimestamp selects the timestamp form as SPEC §5.1 requires.
func (e *encoder) writeTimestamp(sec int64, nsec uint32, hasOffset bool, offset int16) error {
	if nsec >= 1e9 {
		return newError(CodeInvalidArgument, "timestamp nanoseconds must be below 1e9")
	}
	const t = byte(0x100 + extTimestamp)
	switch {
	case hasOffset:
		if offset < -1080 || offset > 1080 {
			return newError(CodeInvalidArgument, "timestamp offset must be within ±1080 minutes")
		}
		e.buf = append(e.buf, 0xc7, 14, t)
		e.buf = binary.BigEndian.AppendUint32(e.buf, nsec)
		e.buf = binary.BigEndian.AppendUint64(e.buf, uint64(sec))
		e.buf = binary.BigEndian.AppendUint16(e.buf, uint16(offset))
	case sec>>34 == 0: // arithmetic shift: negative seconds fail this
		data64 := uint64(nsec)<<34 | uint64(sec)
		if data64>>32 == 0 {
			e.buf = append(e.buf, 0xd6, t)
			e.buf = binary.BigEndian.AppendUint32(e.buf, uint32(data64))
		} else {
			e.buf = append(e.buf, 0xd7, t)
			e.buf = binary.BigEndian.AppendUint64(e.buf, data64)
		}
	default:
		e.buf = append(e.buf, 0xc7, 12, t)
		e.buf = binary.BigEndian.AppendUint32(e.buf, nsec)
		e.buf = binary.BigEndian.AppendUint64(e.buf, uint64(sec))
	}
	return nil
}

// writeBigInt writes x canonically: as a MessagePack integer when it fits in
// [-2^63, 2^64-1], otherwise as a minimal two's-complement bigint (SPEC §5.4).
func (e *encoder) writeBigInt(x *big.Int) error {
	if x.IsInt64() {
		e.writeInt(x.Int64())
		return nil
	}
	if x.IsUint64() {
		e.writeUint(x.Uint64())
		return nil
	}
	return e.writeExt(extBigInt, bigIntBytes(x))
}

// bigIntBytes returns the minimal big-endian two's-complement encoding of x.
func bigIntBytes(x *big.Int) []byte {
	if x.Sign() >= 0 {
		b := x.Bytes()
		if len(b) == 0 || b[0]&0x80 != 0 {
			b = append([]byte{0}, b...)
		}
		return b
	}
	// For x < 0 with m = -x-1, the n-byte two's complement of x is the
	// bitwise complement of m, and the minimal n has room for m plus a sign
	// bit.
	m := new(big.Int).Neg(x)
	m.Sub(m, big.NewInt(1))
	n := (m.BitLen() + 8) / 8
	b := m.FillBytes(make([]byte, n))
	for i := range b {
		b[i] = ^b[i]
	}
	return b
}

var float32SliceType = reflect.TypeFor[[]float32]()

// writeVectorF32 writes a slice or array whose element kind is float32 as a
// float32 vector. The bits are copied as they are (SPEC §5.5 forbids
// canonicalizing NaN payloads inside vectors).
func (e *encoder) writeVectorF32(v reflect.Value) error {
	n := v.Len()
	if uint64(n)*4 > math.MaxUint32 {
		return newError(CodeSizeLimit, "float32 vector too large")
	}
	e.buf = appendExtHeader(e.buf, extVectorF32, 4*n)
	if v.Kind() == reflect.Array {
		v = addressable(v).Slice(0, n)
	}
	if v.Type().ConvertibleTo(float32SliceType) {
		for _, x := range v.Convert(float32SliceType).Interface().([]float32) {
			e.buf = binary.LittleEndian.AppendUint32(e.buf, math.Float32bits(x))
		}
		return nil
	}
	// A named element type (e.g. []Celsius): reflect only exposes the
	// elements as float64. The round trip through float64 is exact for
	// every value except signaling NaNs, which hardware may quiet.
	for i := 0; i < n; i++ {
		e.buf = binary.LittleEndian.AppendUint32(e.buf, math.Float32bits(float32(v.Index(i).Float())))
	}
	return nil
}

// addressable returns v, or an addressable copy of v.
func addressable(v reflect.Value) reflect.Value {
	if v.CanAddr() {
		return v
	}
	c := reflect.New(v.Type()).Elem()
	c.Set(v)
	return c
}

func (e *encoder) writeVectorF64(f VectorF64) error {
	if uint64(len(f))*8 > math.MaxUint32 {
		return newError(CodeSizeLimit, "float64 vector too large")
	}
	e.buf = appendExtHeader(e.buf, extVectorF64, 8*len(f))
	for _, x := range f {
		e.buf = binary.LittleEndian.AppendUint64(e.buf, math.Float64bits(x))
	}
	return nil
}
