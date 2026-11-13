package btoon

import (
	"encoding"
	"math"
	"math/big"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

// assigner stores decoded dynamic values (as produced by the decoder) into
// typed Go values.
type assigner struct {
	disallowUnknown bool
	path            []pathElem
}

type pathElem struct {
	key   string
	index int // -1 for map keys and struct fields
}

func (a *assigner) push(key string, index int) { a.path = append(a.path, pathElem{key, index}) }
func (a *assigner) pop()                       { a.path = a.path[:len(a.path)-1] }

func (a *assigner) location() string {
	if len(a.path) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range a.path {
		if p.index >= 0 {
			b.WriteString("[" + strconv.Itoa(p.index) + "]")
		} else {
			b.WriteString("." + p.key)
		}
	}
	return " at " + b.String()
}

func (a *assigner) mismatch(src any, t reflect.Type, why string) error {
	msg := "cannot store " + describeValue(src) + " in a Go value of type " + t.String()
	if why != "" {
		msg += " (" + why + ")"
	}
	return newError(CodeTypeMismatch, msg+a.location())
}

// maxDescribedBits is the size of the largest integer that error messages
// show in decimal (at most 39 digits).
const maxDescribedBits = 128

// describeValue names the BTOON type of a decoded value for error messages.
func describeValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "nil"
	case bool:
		return "boolean"
	case int64:
		return "integer " + itoa(x)
	case uint64:
		return "integer " + utoa(x)
	case *big.Int:
		// A decoded bigint can have millions of digits, and converting it to
		// decimal takes time superlinear in its length (seconds for an 8 MiB
		// bigint, which fits in an 8 KB envelope). Larger values are
		// described by their size.
		if n := x.BitLen(); n > maxDescribedBits {
			if x.Sign() < 0 {
				return "negative integer of " + strconv.Itoa(n) + " bits"
			}
			return "integer of " + strconv.Itoa(n) + " bits"
		}
		return "integer " + x.String()
	case float64:
		return "float " + strconv.FormatFloat(x, 'g', -1, 64)
	case string:
		return "string"
	case []byte:
		return "binary"
	case []any:
		return "array"
	case map[string]any:
		return "map"
	case time.Time, Timestamp:
		return "timestamp"
	case Date:
		return "date"
	case DateTime:
		return "datetime"
	case []float32:
		return "float32 vector"
	case VectorF64:
		return "float64 vector"
	case Ext:
		return "extension " + itoa(int64(x.Type))
	}
	return reflect.TypeOf(v).String()
}

func (a *assigner) assign(dst reflect.Value, src any) error {
	if src == nil {
		switch dst.Kind() {
		case reflect.Interface, reflect.Pointer, reflect.Map, reflect.Slice:
			dst.SetZero()
		}
		return nil
	}

	switch dst.Kind() {
	case reflect.Pointer:
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		return a.assign(dst.Elem(), src)
	case reflect.Interface:
		// Like encoding/json, decode into the value an interface already
		// points to.
		if !dst.IsNil() {
			if e := dst.Elem(); e.Kind() == reflect.Pointer && !e.IsNil() {
				return a.assign(e.Elem(), src)
			}
		}
		sv := reflect.ValueOf(src)
		if !sv.Type().AssignableTo(dst.Type()) {
			return a.mismatch(src, dst.Type(), "")
		}
		dst.Set(sv)
		return nil
	}

	ti := typeInfoOf(dst.Type())
	switch ti.special {
	case specialTime:
		return a.assignTime(dst, src)
	case specialTimestamp, specialDate, specialDateTime:
		return a.assignTimeType(dst, ti.special, src)
	case specialBigInt:
		z := dst.Addr().Interface().(*big.Int)
		switch s := src.(type) {
		case int64:
			z.SetInt64(s)
		case uint64:
			z.SetUint64(s)
		case *big.Int:
			z.Set(s)
		default:
			return a.mismatch(src, dst.Type(), "")
		}
		return nil
	case specialExt:
		x, ok := src.(Ext)
		if !ok {
			return a.mismatch(src, dst.Type(), "")
		}
		dst.Set(reflect.ValueOf(x))
		return nil
	}

	if ti.addrUnmarshaler {
		if b, ok := src.([]byte); ok {
			u := dst.Addr().Interface().(encoding.BinaryUnmarshaler)
			if err := u.UnmarshalBinary(b); err != nil {
				return wrapError(CodeTypeMismatch, "UnmarshalBinary of "+dst.Type().String()+" failed"+a.location(), err)
			}
			return nil
		}
	}

	switch dst.Kind() {
	case reflect.Bool:
		b, ok := src.(bool)
		if !ok {
			return a.mismatch(src, dst.Type(), "")
		}
		dst.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var i int64
		switch s := src.(type) {
		case int64:
			i = s
		case uint64:
			return a.mismatch(src, dst.Type(), "overflow")
		case *big.Int:
			if !s.IsInt64() {
				return a.mismatch(src, dst.Type(), "overflow")
			}
			i = s.Int64()
		default:
			return a.mismatch(src, dst.Type(), "")
		}
		if dst.OverflowInt(i) {
			return a.mismatch(src, dst.Type(), "overflow")
		}
		dst.SetInt(i)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		var u uint64
		switch s := src.(type) {
		case int64:
			if s < 0 {
				return a.mismatch(src, dst.Type(), "negative")
			}
			u = uint64(s)
		case uint64:
			u = s
		case *big.Int:
			if !s.IsUint64() {
				if s.Sign() < 0 {
					return a.mismatch(src, dst.Type(), "negative")
				}
				return a.mismatch(src, dst.Type(), "overflow")
			}
			u = s.Uint64()
		default:
			return a.mismatch(src, dst.Type(), "")
		}
		if dst.OverflowUint(u) {
			return a.mismatch(src, dst.Type(), "overflow")
		}
		dst.SetUint(u)
	case reflect.Float32, reflect.Float64:
		f, ok := toFloat(src)
		if !ok {
			return a.mismatch(src, dst.Type(), "")
		}
		if dst.OverflowFloat(f) || (math.IsInf(f, 0) && isInteger(src)) {
			return a.mismatch(src, dst.Type(), "overflow")
		}
		dst.SetFloat(f)
	case reflect.String:
		switch s := src.(type) {
		case string:
			dst.SetString(s)
		case []byte:
			dst.SetString(string(s))
		default:
			return a.mismatch(src, dst.Type(), "")
		}
	case reflect.Slice:
		return a.assignSlice(dst, src)
	case reflect.Array:
		return a.assignArray(dst, src)
	case reflect.Map:
		return a.assignMap(dst, src)
	case reflect.Struct:
		return a.assignStruct(dst, src)
	default:
		return newError(CodeInvalidArgument, "cannot unmarshal into a Go value of type "+dst.Type().String()+a.location())
	}
	return nil
}

func isInteger(v any) bool {
	switch v.(type) {
	case int64, uint64, *big.Int:
		return true
	}
	return false
}

// toFloat converts a decoded number to float64. Integers convert with the
// usual rounding (BTOON writers in JavaScript encode whole floats as
// integers, so float fields must accept them).
func toFloat(v any) (float64, bool) {
	switch s := v.(type) {
	case float64:
		return s, true
	case int64:
		return float64(s), true
	case uint64:
		return float64(s), true
	case *big.Int:
		f, _ := new(big.Float).SetInt(s).Float64()
		return f, true
	}
	return 0, false
}

func (a *assigner) assignSlice(dst reflect.Value, src any) error {
	t := dst.Type()
	sv := reflect.ValueOf(src)
	// Same underlying type: store without copying ([]any, []byte,
	// []float32, VectorF64 into []float64, ...).
	if sv.Kind() == reflect.Slice && sv.Type().Elem() == t.Elem() {
		dst.Set(sv.Convert(t))
		return nil
	}
	ek := t.Elem().Kind()
	switch s := src.(type) {
	case []any:
		out := reflect.MakeSlice(t, len(s), len(s))
		for i, x := range s {
			a.push("", i)
			err := a.assign(out.Index(i), x)
			a.pop()
			if err != nil {
				return err
			}
		}
		dst.Set(out)
		return nil
	case []byte: // into a slice of a named byte type
		if ek == reflect.Uint8 {
			out := reflect.MakeSlice(t, len(s), len(s))
			for i, b := range s {
				out.Index(i).SetUint(uint64(b))
			}
			dst.Set(out)
			return nil
		}
	case string:
		if ek == reflect.Uint8 {
			out := reflect.MakeSlice(t, len(s), len(s))
			for i := 0; i < len(s); i++ {
				out.Index(i).SetUint(uint64(s[i]))
			}
			dst.Set(out)
			return nil
		}
	case []float32:
		if ek == reflect.Float32 || ek == reflect.Float64 {
			out := reflect.MakeSlice(t, len(s), len(s))
			for i, x := range s {
				out.Index(i).SetFloat(float64(x))
			}
			dst.Set(out)
			return nil
		}
	case VectorF64:
		if ek == reflect.Float32 || ek == reflect.Float64 {
			out := reflect.MakeSlice(t, len(s), len(s))
			for i, x := range s {
				if out.Index(i).OverflowFloat(x) {
					return a.mismatch(src, t, "element overflows "+t.Elem().String())
				}
				out.Index(i).SetFloat(x)
			}
			dst.Set(out)
			return nil
		}
	}
	return a.mismatch(src, t, "")
}

func (a *assigner) assignArray(dst reflect.Value, src any) error {
	t := dst.Type()
	n := dst.Len()
	ek := t.Elem().Kind()
	switch s := src.(type) {
	case []any:
		// encoding/json semantics: extra elements are dropped, missing ones
		// are zeroed.
		for i := 0; i < n; i++ {
			if i >= len(s) {
				dst.Index(i).SetZero()
				continue
			}
			a.push("", i)
			err := a.assign(dst.Index(i), s[i])
			a.pop()
			if err != nil {
				return err
			}
		}
		return nil
	case []byte:
		if ek == reflect.Uint8 {
			if len(s) != n {
				return a.mismatch(src, t, "binary of length "+strconv.Itoa(len(s)))
			}
			for i, b := range s {
				dst.Index(i).SetUint(uint64(b))
			}
			return nil
		}
	case []float32:
		if (ek == reflect.Float32 || ek == reflect.Float64) && len(s) == n {
			for i, x := range s {
				dst.Index(i).SetFloat(float64(x))
			}
			return nil
		}
	case VectorF64:
		if (ek == reflect.Float32 || ek == reflect.Float64) && len(s) == n {
			for i, x := range s {
				if dst.Index(i).OverflowFloat(x) {
					return a.mismatch(src, t, "element overflows "+t.Elem().String())
				}
				dst.Index(i).SetFloat(x)
			}
			return nil
		}
	}
	return a.mismatch(src, t, "")
}

func (a *assigner) assignMap(dst reflect.Value, src any) error {
	m, ok := src.(map[string]any)
	if !ok {
		return a.mismatch(src, dst.Type(), "")
	}
	t := dst.Type()
	kt := t.Key()
	switch {
	case kt.Kind() == reflect.String:
	case kt.Kind() == reflect.Interface && reflect.TypeFor[string]().AssignableTo(kt):
	default:
		return newError(CodeInvalidArgument, "cannot unmarshal into map key type "+kt.String()+" (BTOON map keys are strings)"+a.location())
	}
	if dst.IsNil() {
		if t == mapStringAnyType {
			dst.Set(reflect.ValueOf(m))
			return nil
		}
		dst.Set(reflect.MakeMapWithSize(t, len(m)))
	}
	et := t.Elem()
	for k, v := range m {
		ev := reflect.New(et).Elem()
		a.push(k, -1)
		err := a.assign(ev, v)
		a.pop()
		if err != nil {
			return err
		}
		kv := reflect.ValueOf(k)
		if kt.Kind() == reflect.String {
			kv = kv.Convert(kt)
		}
		dst.SetMapIndex(kv, ev)
	}
	return nil
}

func (a *assigner) assignStruct(dst reflect.Value, src any) error {
	m, ok := src.(map[string]any)
	if !ok {
		return a.mismatch(src, dst.Type(), "")
	}
	sf := structFieldsOf(dst.Type())
	matched := 0
	for i := range sf.sorted {
		f := &sf.sorted[i]
		v, ok := m[f.name]
		if !ok {
			continue
		}
		matched++
		fv, err := fieldByIndexAlloc(dst, f.index)
		if err != nil {
			return newError(CodeInvalidArgument, err.Error()+a.location())
		}
		a.push(f.name, -1)
		err = a.assign(fv, v)
		a.pop()
		if err != nil {
			return err
		}
	}
	if a.disallowUnknown && matched < len(m) {
		var unknown []string
		for k := range m {
			if _, ok := sf.byName[k]; !ok {
				unknown = append(unknown, k)
			}
		}
		slices.Sort(unknown)
		return newError(CodeTypeMismatch, "unknown field "+quote(unknown[0])+" for Go type "+dst.Type().String()+a.location())
	}
	return nil
}

// fieldByIndexAlloc is like reflect.Value.FieldByIndex but allocates nil
// embedded struct pointers.
func fieldByIndexAlloc(v reflect.Value, index []int) (reflect.Value, error) {
	for i, x := range index {
		if i > 0 && v.Kind() == reflect.Pointer {
			if v.IsNil() {
				if !v.CanSet() {
					return reflect.Value{}, &typeError{"cannot set embedded pointer to unexported struct type " + v.Type().Elem().String()}
				}
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		v = v.Field(x)
	}
	return v, nil
}

type typeError struct{ msg string }

func (e *typeError) Error() string { return e.msg }

// --- time values ---------------------------------------------------------------------

// instant is the common form of the decoded time values.
type instant struct {
	sec       int64
	nsec      uint32
	hasOffset bool
	offset    int16
}

func instantOf(v any) (instant, bool) {
	switch s := v.(type) {
	case time.Time:
		in := instant{sec: s.Unix(), nsec: uint32(s.Nanosecond())}
		if s.Location() != time.UTC {
			if _, off := s.Zone(); off%60 == 0 && off/60 >= -1080 && off/60 <= 1080 {
				in.hasOffset, in.offset = true, int16(off/60)
			}
		}
		return in, true
	case Timestamp:
		return instant{s.Seconds, s.Nanoseconds, s.HasOffset, s.OffsetMinutes}, true
	case Date:
		sec, ms := splitUnix(int64(s), 1e3)
		return instant{sec: sec, nsec: uint32(ms * 1e6)}, true
	case DateTime:
		sec, ns := splitUnix(int64(s), 1e9)
		return instant{sec: sec, nsec: uint32(ns)}, true
	}
	return instant{}, false
}

func (a *assigner) assignTime(dst reflect.Value, src any) error {
	if t, ok := src.(time.Time); ok {
		dst.Set(reflect.ValueOf(t))
		return nil
	}
	in, ok := instantOf(src)
	if !ok {
		return a.mismatch(src, dst.Type(), "")
	}
	if in.sec > maxTimeSeconds {
		return a.mismatch(src, dst.Type(), "outside the range of time.Time")
	}
	ts := Timestamp{in.sec, in.nsec, in.hasOffset, in.offset}
	dst.Set(reflect.ValueOf(ts.Time()))
	return nil
}

func (a *assigner) assignTimeType(dst reflect.Value, kind special, src any) error {
	in, ok := instantOf(src)
	if !ok {
		return a.mismatch(src, dst.Type(), "")
	}
	switch kind {
	case specialTimestamp:
		dst.Set(reflect.ValueOf(Timestamp{in.sec, in.nsec, in.hasOffset, in.offset}))
	case specialDate:
		if in.nsec%1e6 != 0 {
			return a.mismatch(src, dst.Type(), "not a whole number of milliseconds")
		}
		ms, ok := joinUnix(in.sec, 1e3, int64(in.nsec/1e6))
		if !ok {
			return a.mismatch(src, dst.Type(), "out of range")
		}
		dst.SetInt(ms)
	case specialDateTime:
		ns, ok := joinUnix(in.sec, 1e9, int64(in.nsec))
		if !ok {
			return a.mismatch(src, dst.Type(), "out of range")
		}
		dst.SetInt(ns)
	}
	return nil
}
