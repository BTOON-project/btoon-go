package btoon

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBasicEncoding(t *testing.T) {
	tests := []struct {
		name  string
		value interface{}
	}{
		{"nil", nil},
		{"bool true", true},
		{"bool false", false},
		{"int", 42},
		{"float", 3.14},
		{"string", "Hello, BTOON!"},
		{"bytes", []byte{0xDE, 0xAD, 0xBE, 0xEF}},
		{"array", []interface{}{1, 2, 3}},
		{"map", map[string]interface{}{"key": "value"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := Encode(tt.value)
			require.NoError(t, err)
			assert.NotEmpty(t, encoded)

			decoded, err := Decode(encoded)
			require.NoError(t, err)
			assert.Equal(t, tt.value, decoded)
		})
	}
}

func TestCompression(t *testing.T) {
	// Large data that compresses well
	data := make([]interface{}, 1000)
	for i := range data {
		data[i] = map[string]interface{}{
			"id":    i,
			"name":  "test",
			"value": 42,
		}
	}

	uncompressed, err := Encode(data)
	require.NoError(t, err)

	compressed, err := Encode(data, EncodeOptions{
		Compress:  true,
		Algorithm: CompressionZlib,
		Level:     6,
	})
	require.NoError(t, err)

	// Compressed should be smaller
	assert.Less(t, len(compressed), len(uncompressed))

	// Should decode correctly
	decoded, err := Decode(compressed, DecodeOptions{Decompress: true})
	require.NoError(t, err)
	assert.Equal(t, data, decoded)
}

func TestStreaming(t *testing.T) {
	encoder := NewStreamEncoder()
	defer encoder.Close()

	// Encode multiple objects
	for i := 0; i < 10; i++ {
		err := encoder.Encode(map[string]interface{}{
			"index": i,
			"data":  "test",
		})
		require.NoError(t, err)
	}

	// Get encoded data
	data, err := encoder.Flush()
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Decode stream
	decoder := NewStreamDecoder(data)
	defer decoder.Close()

	count := 0
	for {
		obj, err := decoder.Decode()
		if err != nil {
			break
		}
		assert.NotNil(t, obj)
		count++
	}
	assert.Equal(t, 10, count)
}

func BenchmarkEncode(b *testing.B) {
	data := map[string]interface{}{
		"id":   123,
		"name": "benchmark",
		"values": []interface{}{1, 2, 3, 4, 5},
		"nested": map[string]interface{}{
			"key": "value",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Encode(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecode(b *testing.B) {
	data := map[string]interface{}{
		"id":   123,
		"name": "benchmark",
		"values": []interface{}{1, 2, 3, 4, 5},
	}

	encoded, _ := Encode(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Decode(encoded)
		if err != nil {
			b.Fatal(err)
		}
	}
}
