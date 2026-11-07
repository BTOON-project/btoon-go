package btoon

import (
	"math"
	"time"
)

// Extension type ids assigned by SPEC §5.
const (
	extTimestamp = -1
	extDate      = -2
	extDateTime  = -3
	extBigInt    = -4
	extVectorF32 = -5
	extVectorF64 = -6
	extTabular   = -10
)

// isAssignedExt reports whether id is an extension type that BTOON
// interprets (and that therefore cannot be written as a generic Ext).
func isAssignedExt(id int8) bool {
	return (id >= extVectorF64 && id <= extTimestamp) || id == extTabular
}

// Ext is an extension value that BTOON does not interpret (SPEC §5.7): an
// application type id in 0..127, or one of the ids that SPEC reserves for
// future versions (-9..-7 and -128..-11). Decode returns Ext for such
// extensions and Marshal writes Ext with the smallest header.
//
// Marshal rejects an Ext whose Type is one that BTOON assigns (-1..-6 and
// -10); use the corresponding Go types instead.
type Ext struct {
	Type int8
	Data []byte
}

// VectorF64 is a packed float64 vector (extension -6, SPEC §5.5). A plain
// []float64 encodes as an array of floats; convert it to VectorF64 to get
// the compact vector encoding. Decode returns float64 vectors as VectorF64.
//
// Float32 vectors need no wrapper: any []float32 (or [N]float32) encodes as
// a float32 vector, and Decode returns them as []float32.
type VectorF64 []float64

// Timestamp is a BTOON timestamp (extension -1, SPEC §5.1) with exact control
// over its encoding, including the optional UTC offset that only the
// 14-byte form carries.
//
// time.Time is usually more convenient: Marshal encodes it as a timestamp
// and Decode returns timestamps as time.Time. Timestamp is useful to attach
// an explicit offset, to represent instants outside time.Time's range
// (Decode returns those as Timestamp), and with DecodeOptions.PreserveTimeTypes.
type Timestamp struct {
	// Seconds since the Unix epoch, describing the instant in UTC.
	Seconds int64
	// Nanoseconds within the second; must be below 1e9.
	Nanoseconds uint32
	// HasOffset reports whether OffsetMinutes is part of the value.
	HasOffset bool
	// OffsetMinutes is presentation metadata: local time is UTC plus this
	// many minutes. Its magnitude must not exceed 1080 (18 hours).
	OffsetMinutes int16
}

// NewTimestamp returns the instant of t as a Timestamp without an offset.
func NewTimestamp(t time.Time) Timestamp {
	return Timestamp{Seconds: t.Unix(), Nanoseconds: uint32(t.Nanosecond())}
}

// Time returns ts as a time.Time: in UTC, or in a fixed zone with the
// offset when HasOffset is set. The result is not meaningful for instants
// that time.Time cannot represent (seconds above about 2.9e11 years).
func (ts Timestamp) Time() time.Time {
	t := time.Unix(ts.Seconds, int64(ts.Nanoseconds))
	if ts.HasOffset {
		return t.In(fixedZone(ts.OffsetMinutes))
	}
	return t.UTC()
}

// Date is a BTOON date (extension -2, SPEC §5.2): milliseconds since the
// Unix epoch, UTC. Decode returns dates as time.Time unless
// DecodeOptions.PreserveTimeTypes is set.
type Date int64

// NewDate returns t truncated to milliseconds. The result is undefined if
// t is more than about 292 million years from 1970.
func NewDate(t time.Time) Date { return Date(t.UnixMilli()) }

// Time returns d as a time.Time in UTC.
func (d Date) Time() time.Time { return time.UnixMilli(int64(d)).UTC() }

// DateTime is a BTOON datetime (extension -3, SPEC §5.3): nanoseconds since
// the Unix epoch, UTC, covering 1677-09-21 to 2262-04-11. Decode returns
// datetimes as time.Time unless DecodeOptions.PreserveTimeTypes is set.
type DateTime int64

// NewDateTime returns t as nanoseconds since the epoch. The result is
// undefined if t is outside the years 1678 to 2261.
func NewDateTime(t time.Time) DateTime { return DateTime(t.UnixNano()) }

// Time returns dt as a time.Time in UTC.
func (dt DateTime) Time() time.Time { return time.Unix(0, int64(dt)).UTC() }

// maxTimeSeconds is the largest Unix second that time.Unix can represent
// without overflowing its internal representation (seconds since year 1).
const maxTimeSeconds = math.MaxInt64 - 62135596800

func fixedZone(offsetMinutes int16) *time.Location {
	return time.FixedZone("", int(offsetMinutes)*60)
}

// splitUnix splits v (in units of 1/unit seconds) into seconds and a
// non-negative remainder, using floor division.
func splitUnix(v, unit int64) (sec, frac int64) {
	sec, frac = v/unit, v%unit
	if frac < 0 {
		sec--
		frac += unit
	}
	return sec, frac
}

// joinUnix returns sec*unit + frac (0 <= frac < unit) if it fits in an
// int64.
func joinUnix(sec, unit, frac int64) (int64, bool) {
	if sec >= 0 {
		if sec > (math.MaxInt64-frac)/unit {
			return 0, false
		}
		return sec*unit + frac, true
	}
	// sec*unit + frac == (sec+1)*unit - (unit-frac), which avoids
	// overflowing in the intermediate product.
	s1 := sec + 1
	if s1 < math.MinInt64/unit {
		return 0, false
	}
	r, d := s1*unit, unit-frac
	if r < math.MinInt64+d {
		return 0, false
	}
	return r - d, true
}
