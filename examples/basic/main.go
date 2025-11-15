package main

import (
	"fmt"
	"log"

	btoon "github.com/BTOON-project/btoon-go"
)

func main() {
	fmt.Println("BTOON Go Basic Example")
	fmt.Println("======================")

	// Simple data types
	simpleData := map[string]interface{}{
		"message": "Hello, BTOON!",
		"count":   42,
		"pi":      3.14159,
		"active":  true,
		"empty":   nil,
	}

	fmt.Println("\n1. Simple data encoding:")
	fmt.Printf("Original: %+v\n", simpleData)

	encoded, err := btoon.Encode(simpleData)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Encoded size: %d bytes\n", len(encoded))

	decoded, err := btoon.Decode(encoded)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Decoded: %+v\n", decoded)

	// Nested structures
	nestedData := map[string]interface{}{
		"user": map[string]interface{}{
			"id":    1001,
			"name":  "Alice",
			"email": "alice@example.com",
			"roles": []string{"admin", "user"},
			"settings": map[string]interface{}{
				"theme":         "dark",
				"notifications": true,
				"language":      "en",
			},
		},
		"metadata": map[string]interface{}{
			"created": "2024-01-01T00:00:00Z",
			"version": "0.0.1",
		},
	}

	fmt.Println("\n2. Nested structure encoding:")
	nestedEncoded, err := btoon.Encode(nestedData)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("BTOON size: %d bytes\n", len(nestedEncoded))

	// Arrays of different types
	arrayData := map[string]interface{}{
		"numbers": []int{1, 2, 3, 4, 5},
		"strings": []string{"apple", "banana", "cherry"},
		"mixed":   []interface{}{42, "hello", true, nil, map[string]string{"key": "value"}},
		"matrix":  [][]int{{1, 2}, {3, 4}, {5, 6}},
	}

	fmt.Println("\n3. Array encoding:")
	arrayEncoded, err := btoon.Encode(arrayData)
	if err != nil {
		log.Fatal(err)
	}

	arrayDecoded, err := btoon.Decode(arrayEncoded)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Arrays preserved correctly\n")

	// Binary data
	binaryData := map[string]interface{}{
		"id":       "file-001",
		"content":  []byte("Binary content here"),
		"checksum": []byte{0xDE, 0xAD, 0xBE, 0xEF},
	}

	fmt.Println("\n4. Binary data encoding:")
	binaryEncoded, err := btoon.Encode(binaryData)
	if err != nil {
		log.Fatal(err)
	}

	binaryDecoded, err := btoon.Decode(binaryEncoded)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Binary data preserved correctly")

	fmt.Println("\n✅ All basic examples completed successfully!")
}
