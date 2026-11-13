package btoon_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	btoon "github.com/BTOON-project/btoon-go"
)

type benchRecord struct {
	ID      int64     `json:"id"`
	Name    string    `json:"name"`
	Email   string    `json:"email"`
	Active  bool      `json:"active"`
	Score   float64   `json:"score"`
	Tags    []string  `json:"tags"`
	Created time.Time `json:"created"`
}

func benchRecords(n int) []benchRecord {
	recs := make([]benchRecord, n)
	for i := range recs {
		recs[i] = benchRecord{
			ID: int64(i), Name: fmt.Sprintf("user%d", i), Email: fmt.Sprintf("user%d@example.com", i),
			Active: i%3 == 0, Score: float64(i) * 1.5, Tags: []string{"a", "b"},
			Created: time.Unix(1700000000+int64(i), 0).UTC(),
		}
	}
	return recs
}

func BenchmarkMarshalRecords(b *testing.B) {
	recs := benchRecords(1000)
	out, _ := btoon.Marshal(recs)
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := btoon.Marshal(recs); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnmarshalRecords(b *testing.B) {
	data, _ := btoon.Marshal(benchRecords(1000))
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var recs []benchRecord
		if err := btoon.Unmarshal(data, &recs); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeRecords(b *testing.B) {
	data, _ := btoon.Marshal(benchRecords(1000))
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := btoon.Decode(data); err != nil {
			b.Fatal(err)
		}
	}
}

// The same records as plain maps, without the tabular encoding.
func BenchmarkDecodeRecordsPlain(b *testing.B) {
	data, _ := btoon.MarshalWithOptions(benchRecords(1000), btoon.EncodeOptions{DisableTabular: true})
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := btoon.Decode(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalRecordsZlib(b *testing.B) {
	recs := benchRecords(1000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := btoon.MarshalWithOptions(recs, btoon.EncodeOptions{Compress: true}); err != nil {
			b.Fatal(err)
		}
	}
}

// encoding/json on the same records, for comparison.
func BenchmarkJSONMarshalRecords(b *testing.B) {
	recs := benchRecords(1000)
	out, _ := json.Marshal(recs)
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := json.Marshal(recs); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJSONUnmarshalRecords(b *testing.B) {
	data, _ := json.Marshal(benchRecords(1000))
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var recs []benchRecord
		if err := json.Unmarshal(data, &recs); err != nil {
			b.Fatal(err)
		}
	}
}

func TestBenchmarkSizes(t *testing.T) {
	recs := benchRecords(1000)
	bt, _ := btoon.Marshal(recs)
	plain, _ := btoon.MarshalWithOptions(recs, btoon.EncodeOptions{DisableTabular: true})
	z, _ := btoon.MarshalWithOptions(recs, btoon.EncodeOptions{Compress: true})
	js, _ := json.Marshal(recs)
	t.Logf("1000 records: BTOON %d bytes (tabular), %d plain, %d zlib; JSON %d", len(bt), len(plain), len(z), len(js))
	if len(bt) >= len(plain) || len(z) >= len(bt) {
		t.Fatal("unexpected sizes")
	}
}
