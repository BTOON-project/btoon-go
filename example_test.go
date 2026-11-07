package btoon_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"

	btoon "github.com/BTOON-project/btoon-go"
)

func ExampleMarshal() {
	type User struct {
		ID    int    `btoon:"id"`
		Name  string `btoon:"name"`
		Email string `btoon:"email,omitempty"`
	}
	b, err := btoon.Marshal(User{ID: 1, Name: "Ada"})
	if err != nil {
		panic(err)
	}
	fmt.Printf("%x\n", b)
	// Output: 82a2696401a46e616d65a3416461
}

func ExampleUnmarshal() {
	type User struct {
		ID   int    `btoon:"id"`
		Name string `btoon:"name"`
	}
	data := []byte{0x82, 0xa2, 'i', 'd', 0x01, 0xa4, 'n', 'a', 'm', 'e', 0xa3, 'A', 'd', 'a'}
	var u User
	if err := btoon.Unmarshal(data, &u); err != nil {
		panic(err)
	}
	fmt.Printf("%+v\n", u)
	// Output: {ID:1 Name:Ada}
}

func ExampleDecode() {
	b, _ := btoon.Marshal(map[string]any{
		"n":    42,
		"big":  uint64(1 << 63),
		"when": time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		"tags": []string{"a", "b"},
	})
	v, err := btoon.Decode(b)
	if err != nil {
		panic(err)
	}
	m := v.(map[string]any)
	fmt.Printf("%T %T %T %T\n", m["n"], m["big"], m["when"], m["tags"])
	// Output: int64 uint64 time.Time []interface {}
}

// Arrays of uniform records use the columnar (tabular) encoding when it is
// smaller, writing each key once.
func ExampleMarshal_tabular() {
	type Point struct {
		X int `btoon:"x"`
		Y int `btoon:"y"`
	}
	points := make([]Point, 10)
	for i := range points {
		points[i] = Point{i, i * i}
	}
	tabular, _ := btoon.Marshal(points)
	plain, _ := btoon.MarshalWithOptions(points, btoon.EncodeOptions{DisableTabular: true})
	fmt.Println(len(tabular), "bytes tabular,", len(plain), "bytes plain")
	// Output: 55 bytes tabular, 71 bytes plain
}

func ExampleMarshalWithOptions() {
	key := []byte("an example key of 32 bytes......")
	rows := make([]map[string]any, 50)
	for i := range rows {
		rows[i] = map[string]any{"id": i, "status": "active"}
	}
	b, err := btoon.MarshalWithOptions(rows, btoon.EncodeOptions{Compress: true, HMACKey: key})
	if err != nil {
		panic(err)
	}
	fmt.Printf("%q %q\n", b[:4], b[40:44])

	_, err = btoon.Decode(b)
	fmt.Println(errors.Is(err, btoon.ErrAuthenticationFailed))

	v, err := btoon.DecodeWithOptions(b, btoon.DecodeOptions{HMACKey: key})
	fmt.Println(len(v.([]any)), err)
	// Output:
	// "BTSG" "BTON"
	// true
	// 50 <nil>
}

func ExampleNewDecoder() {
	var stream bytes.Buffer
	enc := btoon.NewEncoder(&stream)
	for i := 1; i <= 3; i++ {
		enc.Encode(map[string]int{"seq": i})
	}
	dec := btoon.NewDecoder(&stream)
	for {
		var msg struct {
			Seq int `btoon:"seq"`
		}
		if err := dec.Decode(&msg); err == io.EOF {
			break
		} else if err != nil {
			panic(err)
		}
		fmt.Println(msg.Seq)
	}
	// Output:
	// 1
	// 2
	// 3
}

func ExampleError() {
	_, err := btoon.Decode([]byte{0x82, 0xa1, 'a', 0x01, 0xa1, 'a', 0x02})
	var e *btoon.Error
	if errors.As(err, &e) {
		fmt.Println(e.Code)
	}
	fmt.Println(err)
	// Output:
	// duplicate_key
	// btoon: duplicate_key: duplicate map key "a" (at byte 4)
}
