// Command basic demonstrates the btoon package: struct encoding, dynamic
// decoding, the tabular encoding, compression, signing and error codes.
package main

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	btoon "github.com/BTOON-project/btoon-go"
)

type User struct {
	ID      int       `btoon:"id"`
	Name    string    `btoon:"name"`
	Email   string    `btoon:"email,omitempty"`
	Roles   []string  `btoon:"roles"`
	Created time.Time `btoon:"created"`
	Avatar  []byte    `btoon:"avatar,omitempty"`
}

func main() {
	fmt.Printf("btoon %s (spec %s)\n\n", btoon.Version, btoon.SpecVersion)

	// 1. Structs encode as maps with canonical (sorted) keys.
	u := User{ID: 1, Name: "Ada", Roles: []string{"admin"}, Created: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)}
	b, err := btoon.Marshal(u)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("1. User as BTOON (%d bytes): %x\n", len(b), b)

	var back User
	if err := btoon.Unmarshal(b, &back); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("   decoded into a struct: %+v\n", back)

	// 2. Decode without a target type gives the dynamic representation.
	v, err := btoon.Decode(b)
	if err != nil {
		log.Fatal(err)
	}
	m := v.(map[string]any)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("   %-8s %T\n", k, m[k])
	}

	// 3. Arrays of uniform records use the columnar encoding when smaller.
	users := make([]User, 50)
	for i := range users {
		users[i] = User{ID: i, Name: fmt.Sprintf("user%d", i), Roles: []string{"reader"}, Created: u.Created}
	}
	tabular, _ := btoon.Marshal(users)
	plain, _ := btoon.MarshalWithOptions(users, btoon.EncodeOptions{DisableTabular: true})
	fmt.Printf("\n3. 50 users: %d bytes tabular, %d bytes as a plain array\n", len(tabular), len(plain))

	// 4. Compression (zlib is built in) and an HMAC-SHA256 signature.
	key := []byte("demo key, use 32+ random bytes!!")
	sealed, err := btoon.MarshalWithOptions(users, btoon.EncodeOptions{Compress: true, HMACKey: key})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("4. compressed and signed: %d bytes (%q envelope around %q)\n", len(sealed), sealed[:4], sealed[40:44])

	var got []User
	if err := btoon.UnmarshalWithOptions(sealed, &got, btoon.DecodeOptions{HMACKey: key}); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("   verified and decoded %d users\n", len(got))

	// 5. Errors carry the machine-readable codes of SPEC §7.
	sealed[len(sealed)-1] ^= 1
	_, err = btoon.DecodeWithOptions(sealed, btoon.DecodeOptions{HMACKey: key})
	var be *btoon.Error
	if errors.As(err, &be) {
		fmt.Printf("\n5. tampered input: code=%s (%v)\n", be.Code, err)
	}
	_, err = btoon.Decode([]byte{0x92, 0x01}) // an array of 2 with one element
	fmt.Printf("   truncated input: errors.Is(err, btoon.ErrTruncated) = %v\n", errors.Is(err, btoon.ErrTruncated))
}
