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
	isZeroerType        = reflect.TypeFor[interface{ IsZero() bool }]()
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
	// marshaler: the type implements encoding.BinaryMarshaler.
	marshaler bool
	// addrMarshaler: only *T implements encoding.BinaryMarshaler.
	addrMarshaler bool
	// addrUnmarshaler: *T implements encoding.BinaryUnmarshaler.
	addrUnmarshaler bool

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
		ti.marshaler = t.Implements(binaryMarshalerType)
		if !ti.marshaler && t.Kind() != reflect.Pointer {
			ti.addrMarshaler = reflect.PointerTo(t).Implements(binaryMarshalerType)
		}
		if t.Kind() != reflect.Pointer {
			ti.addrUnmarshaler = reflect.PointerTo(t).Implements(binaryUnmarshalType)
		}
	}
	actual, _ := typeInfoCache.LoadOrStore(t, ti)
	return actual.(*typeInfo)
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
	switch {
	case t.Kind() == reflect.Interface && t.Implements(isZeroerType):
		return func(v reflect.Value) bool {
			return v.IsNil() || v.Interface().(interface{ IsZero() bool }).IsZero()
		}
	case t.Kind() == reflect.Pointer && t.Implements(isZeroerType):
		return func(v reflect.Value) bool {
			return v.IsNil() || v.Interface().(interface{ IsZero() bool }).IsZero()
		}
	case t.Implements(isZeroerType):
		return func(v reflect.Value) bool {
			return v.Interface().(interface{ IsZero() bool }).IsZero()
		}
	case reflect.PointerTo(t).Implements(isZeroerType):
		return func(v reflect.Value) bool {
			if !v.CanAddr() {
				c := reflect.New(v.Type()).Elem()
				c.Set(v)
				v = c
			}
			return v.Addr().Interface().(interface{ IsZero() bool }).IsZero()
		}
	default:
		return reflect.Value.IsZero
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
