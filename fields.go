package btoon

import (
	"cmp"
	"encoding"
	"math/big"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	timeType            = reflect.TypeFor[time.Time]()
	bigIntType          = reflect.TypeFor[big.Int]()
	bigIntPtrType       = reflect.TypeFor[*big.Int]()
	extType             = reflect.TypeFor[Ext]()
	vectorF64Type       = reflect.TypeFor[VectorF64]()
	timestampType       = reflect.TypeFor[Timestamp]()
	dateType            = reflect.TypeFor[Date]()
	dateTimeType        = reflect.TypeFor[DateTime]()
	mapStringAnyType    = reflect.TypeFor[map[string]any]()
	binaryMarshalerType = reflect.TypeFor[encoding.BinaryMarshaler]()
	binaryUnmarshalType = reflect.TypeFor[encoding.BinaryUnmarshaler]()
	textMarshalerType   = reflect.TypeFor[encoding.TextMarshaler]()
	textUnmarshalType   = reflect.TypeFor[encoding.TextUnmarshaler]()
	isZeroerType        = reflect.TypeFor[interface{ IsZero() bool }]()
)

// method is the marshaling method that Marshal uses for a type.
type method uint8

const (
	methodNone   method = iota
	methodBinary        // encoding.BinaryMarshaler: binary
	methodText          // encoding.TextMarshaler: string
)

// special identifies Go types with a dedicated BTOON mapping.
type special uint8

const (
	specialNone special = iota
	specialTime
	specialBigInt    // big.Int (by value)
	specialBigIntPtr // *big.Int
	specialExt
	specialVectorF64
	specialTimestamp
	specialDate
	specialDateTime
)

// typeInfo caches what the encoder and decoder need to know about a type.
type typeInfo struct {
	special special
	// marshal is the method Marshal encodes the type with: BinaryMarshaler
	// of the type or of a pointer to it, else, for a struct type,
	// TextMarshaler (see textMethod).
	marshal method
	// marshalAddr: only *T has the marshal method. Marshal calls it on an
	// addressable copy of a value that isn't addressable, so that the
	// encoding doesn't depend on addressability (as in encoding/json/v2).
	marshalAddr bool
	// unmarshalBinary: *T implements encoding.BinaryUnmarshaler.
	// unmarshalText: T is a struct type and *T implements
	// encoding.TextUnmarshaler (see textMethod).
	unmarshalBinary, unmarshalText bool
	// native is the kind (specialTime or specialBigInt) of the time.Time or
	// big.Int that a struct type embeds, by value or through pointers, at
	// the shallowest embedding depth that has one, and nativeIndex is its
	// index path. The struct's marshaling methods are usually promoted from
	// it; see encoder.encodeMethod.
	native      special
	nativeIndex []int

	fieldsOnce sync.Once
	fields     *structFields
}

var typeInfoCache sync.Map // reflect.Type -> *typeInfo

func typeInfoOf(t reflect.Type) *typeInfo {
	if ti, ok := typeInfoCache.Load(t); ok {
		return ti.(*typeInfo)
	}
	ti := &typeInfo{}
	switch t {
	case timeType:
		ti.special = specialTime
	case bigIntType:
		ti.special = specialBigInt
	case bigIntPtrType:
		ti.special = specialBigIntPtr
	case extType:
		ti.special = specialExt
	case vectorF64Type:
		ti.special = specialVectorF64
	case timestampType:
		ti.special = specialTimestamp
	case dateType:
		ti.special = specialDate
	case dateTimeType:
		ti.special = specialDateTime
	}
	if ti.special == specialNone && t.Kind() != reflect.Interface {
		if t.Kind() == reflect.Struct {
			ti.native, ti.nativeIndex = embeddedNative(t)
		}
		// The methods of *T include those of T.
		pt := t
		if t.Kind() != reflect.Pointer {
			pt = reflect.PointerTo(t)
		}
		switch {
		case pt.Implements(binaryMarshalerType):
			ti.marshal, ti.marshalAddr = methodBinary, !t.Implements(binaryMarshalerType)
		case textMethod(t, textMarshalerType, ti):
			ti.marshal, ti.marshalAddr = methodText, !t.Implements(textMarshalerType)
		}
		if t.Kind() != reflect.Pointer {
			ti.unmarshalBinary = pt.Implements(binaryUnmarshalType)
			ti.unmarshalText = textMethod(t, textUnmarshalType, ti)
		}
	}
	actual, _ := typeInfoCache.LoadOrStore(t, ti)
	return actual.(*typeInfo)
}

// textMethod reports whether Marshal or Unmarshal use the text method
// iface (encoding.TextMarshaler or TextUnmarshaler) of type t. They do only
// for a struct type, whose fields don't always hold its value (big.Float,
// big.Rat). A type of another kind is encoded by its kind, as before 0.3,
// so that net.IP stays binary and an integer enum stays an integer.
//
// For the same reason, they don't use a text method that a struct may get
// from an embedded field: a struct that embeds net.IP is still a map of its
// fields, as encoding it as the IP's text would drop the others. The
// exception is an embedded big.Int, which Marshal then writes as an
// integer (see encoder.encodeMethod).
func textMethod(t, iface reflect.Type, ti *typeInfo) bool {
	if t.Kind() != reflect.Struct || !reflect.PointerTo(t).Implements(iface) {
		return false
	}
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.Anonymous || !hasMethod(sf.Type, iface) {
			continue
		}
		if iface == textMarshalerType && ti.native == specialBigInt && i == ti.nativeIndex[0] {
			continue
		}
		return false
	}
	return true
}

// hasMethod reports whether a field of type t gives its struct the methods
// of iface, with a value or a pointer receiver.
func hasMethod(t, iface reflect.Type) bool {
	if t.Implements(iface) {
		return true
	}
	return t.Kind() != reflect.Pointer && t.Kind() != reflect.Interface && reflect.PointerTo(t).Implements(iface)
}

// embeddedNative finds the time.Time or big.Int that struct type t embeds
// at the shallowest depth, through embedded structs and pointers to them,
// and returns its kind and index path. Like a promoted method, it must be
// the only one at that depth.
func embeddedNative(t reflect.Type) (special, []int) {
	type node struct {
		typ   reflect.Type
		index []int
	}
	level := []node{{t, nil}}
	seen := map[reflect.Type]bool{t: true}
	for len(level) > 0 {
		var found, next []node
		for _, n := range level {
			for i := 0; i < n.typ.NumField(); i++ {
				sf := n.typ.Field(i)
				if !sf.Anonymous {
					continue
				}
				ft := sf.Type
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				index := append(slices.Clip(n.index), i)
				switch {
				case ft == timeType || ft == bigIntType:
					found = append(found, node{ft, index})
				case ft.Kind() == reflect.Struct && !seen[ft]:
					seen[ft] = true
					next = append(next, node{ft, index})
				}
			}
		}
		switch {
		case len(found) == 1 && found[0].typ == timeType:
			return specialTime, found[0].index
		case len(found) == 1:
			return specialBigInt, found[0].index
		case len(found) > 1:
			return specialNone, nil // ambiguous, like the methods
		}
		level = next
	}
	return specialNone, nil
}

// structFieldsOf returns the BTOON fields of struct type t.
func structFieldsOf(t reflect.Type) *structFields {
	ti := typeInfoOf(t)
	ti.fieldsOnce.Do(func() { ti.fields = typeFields(t) })
	return ti.fields
}

// field is one encodable struct field, possibly promoted from an embedded
// struct.
type field struct {
	name      string
	tagged    bool  // name came from a tag
	index     []int // index path for reflect.Value.FieldByIndex
	typ       reflect.Type
	omitEmpty bool
	omitZero  bool
	isZero    func(reflect.Value) bool
}

type structFields struct {
	// sorted lists the fields in canonical key order (bytewise by name).
	sorted []field
	// byName maps a key to its index in sorted.
	byName map[string]int
}

// typeFields returns the fields of struct type t, following the rules of
// encoding/json: exported fields only, embedded structs flattened
// breadth-first, conflicts resolved by depth and then by the presence of a
// tag, and ambiguous names dropped. The "btoon" tag takes precedence over the
// "json" tag; "-" skips a field.
func typeFields(t reflect.Type) *structFields {
	current := []field{}
	next := []field{{typ: t}}

	// Count of queued names for the current level and the next.
	var count, nextCount map[reflect.Type]int
	visited := map[reflect.Type]bool{}

	var fields []field
	for len(next) > 0 {
		current, next = next, current[:0]
		count, nextCount = nextCount, map[reflect.Type]int{}

		for _, f := range current {
			if visited[f.typ] {
				continue
			}
			visited[f.typ] = true

			for i := 0; i < f.typ.NumField(); i++ {
				sf := f.typ.Field(i)
				if sf.Anonymous {
					ft := sf.Type
					if ft.Kind() == reflect.Pointer {
						ft = ft.Elem()
					}
					if !sf.IsExported() && ft.Kind() != reflect.Struct {
						// Embedded fields of unexported non-struct types.
						continue
					}
					// Embedded unexported structs may still have exported fields.
				} else if !sf.IsExported() {
					continue
				}

				tag, ok := sf.Tag.Lookup("btoon")
				if !ok {
					tag = sf.Tag.Get("json")
				}
				if tag == "-" {
					continue
				}
				name, opts, _ := strings.Cut(tag, ",")

				index := make([]int, len(f.index)+1)
				copy(index, f.index)
				index[len(f.index)] = i

				ft := sf.Type
				if ft.Name() == "" && ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}

				if name != "" || !sf.Anonymous || ft.Kind() != reflect.Struct {
					tagged := name != ""
					if name == "" {
						name = sf.Name
					}
					fld := field{
						name:      name,
						tagged:    tagged,
						index:     index,
						typ:       sf.Type,
						omitEmpty: hasOption(opts, "omitempty"),
						omitZero:  hasOption(opts, "omitzero"),
					}
					if fld.omitZero {
						fld.isZero = zeroFunc(sf.Type)
					}
					fields = append(fields, fld)
					if count[f.typ] > 1 {
						// Multiple embedded instances of this type at the same
						// depth: add a duplicate so the field is annihilated.
						fields = append(fields, fields[len(fields)-1])
					}
					continue
				}

				// An embedded struct to explore in the next round.
				nextCount[ft]++
				if nextCount[ft] == 1 {
					next = append(next, field{name: ft.Name(), index: index, typ: ft})
				}
			}
		}
	}

	slices.SortFunc(fields, func(a, b field) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		if c := cmp.Compare(len(a.index), len(b.index)); c != 0 {
			return c
		}
		if a.tagged != b.tagged {
			if a.tagged {
				return -1
			}
			return 1
		}
		return slices.Compare(a.index, b.index)
	})

	// Keep the dominant field for each name (Go's embedding rules, with
	// tagged fields promoted), dropping names that remain ambiguous.
	out := fields[:0]
	for advance, i := 0, 0; i < len(fields); i += advance {
		fi := fields[i]
		for advance = 1; i+advance < len(fields); advance++ {
			if fields[i+advance].name != fi.name {
				break
			}
		}
		if advance == 1 {
			out = append(out, fi)
			continue
		}
		dom := fields[i : i+advance]
		if len(dom[0].index) == len(dom[1].index) && dom[0].tagged == dom[1].tagged {
			continue // ambiguous
		}
		out = append(out, dom[0])
	}

	// out is sorted by name, which is the canonical key order (Go string
	// comparison is bytewise, i.e. UTF-8 byte order).
	sf := &structFields{sorted: slices.Clip(out), byName: make(map[string]int, len(out))}
	for i, f := range sf.sorted {
		sf.byName[f.name] = i
	}
	return sf
}

func hasOption(opts, name string) bool {
	for opts != "" {
		var o string
		o, opts, _ = strings.Cut(opts, ",")
		if o == name {
			return true
		}
	}
	return false
}

// zeroFunc implements the omitzero option: a type's IsZero method if it has
// one, otherwise reflect's notion of the zero value.
func zeroFunc(t reflect.Type) func(reflect.Value) bool {
	var isZero func(reflect.Value) bool
	switch {
	case t.Kind() == reflect.Interface && t.Implements(isZeroerType):
		// As in encoding/json, an interface holding a nil pointer is zero:
		// calling IsZero on it could panic.
		isZero = func(v reflect.Value) bool {
			return v.IsNil() || (v.Elem().Kind() == reflect.Pointer && v.Elem().IsNil()) ||
				v.Interface().(interface{ IsZero() bool }).IsZero()
		}
	case t.Kind() == reflect.Pointer && t.Implements(isZeroerType):
		isZero = func(v reflect.Value) bool {
			return v.IsNil() || v.Interface().(interface{ IsZero() bool }).IsZero()
		}
	case t.Implements(isZeroerType):
		isZero = func(v reflect.Value) bool {
			return v.Interface().(interface{ IsZero() bool }).IsZero()
		}
	case reflect.PointerTo(t).Implements(isZeroerType):
		isZero = func(v reflect.Value) bool {
			return addressable(v).Addr().Interface().(interface{ IsZero() bool }).IsZero()
		}
	default:
		return reflect.Value.IsZero
	}
	return func(v reflect.Value) bool {
		// Reflection can't call the methods of an embedded field of an
		// unexported type that has a tag (see canCallMethods).
		if !v.CanInterface() {
			return v.IsZero()
		}
		return isZero(v)
	}
}

// isEmptyValue implements the omitempty option with encoding/json semantics.
func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Interface, reflect.Pointer:
		return v.IsZero()
	}
	return false
}

// fieldByIndex is like reflect.Value.FieldByIndex but reports false instead
// of panicking when it meets a nil embedded pointer.
func fieldByIndex(v reflect.Value, index []int) (reflect.Value, bool) {
	for i, x := range index {
		if i > 0 && v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return reflect.Value{}, false
			}
			v = v.Elem()
		}
		v = v.Field(x)
	}
	return v, true
}
