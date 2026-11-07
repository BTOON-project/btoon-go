package btoon_test

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	btoon "github.com/BTOON-project/btoon-go"
)

// TestConcurrentUse exercises the shared caches (type information, struct
// fields, codec registry) from many goroutines; run it with -race.
func TestConcurrentUse(t *testing.T) {
	type Inner struct {
		V []float32 `btoon:"v"`
	}
	type Rec struct {
		ID    int            `btoon:"id"`
		Name  string         `btoon:"name"`
		When  time.Time      `btoon:"when"`
		Inner *Inner         `btoon:"inner,omitempty"`
		Meta  map[string]any `btoon:"meta"`
	}
	recs := make([]Rec, 20)
	for i := range recs {
		recs[i] = Rec{ID: i, Name: fmt.Sprint("n", i), When: time.Unix(int64(i), 0).UTC(), Inner: &Inner{V: []float32{float32(i)}}, Meta: map[string]any{"i": int64(i)}}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				b, err := btoon.MarshalWithOptions(recs, btoon.EncodeOptions{Compress: g%2 == 0})
				if err != nil {
					errs <- err
					return
				}
				var back []Rec
				if err := btoon.Unmarshal(b, &back); err != nil {
					errs <- err
					return
				}
				if !reflect.DeepEqual(back, recs) {
					errs <- fmt.Errorf("goroutine %d: round trip differs", g)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
