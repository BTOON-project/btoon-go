# BTOON for Go

[![Go Reference](https://pkg.go.dev/badge/github.com/BTOON-project/btoon-go.svg)](https://pkg.go.dev/github.com/BTOON-project/btoon-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/BTOON-project/btoon-go)](https://goreportcard.com/report/github.com/BTOON-project/btoon-go)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)

High-performance binary serialization format for Go applications using native C++ implementation via CGO.

## Features

- 🚀 **High Performance** - Native C++ implementation with Go bindings
- 📦 **Compact Binary Format** - Smaller than JSON, faster than gob
- 🗜️ **Built-in Compression** - ZLIB, LZ4, ZSTD, Brotli, Snappy support  
- 📊 **Tabular Data Optimization** - Columnar storage for structured data
- 🔄 **Schema Evolution** - Forward and backward compatibility
- ⚡ **Zero-Copy APIs** - Minimal memory overhead for large data
- 🛡️ **Type Safety** - Preserves Go types accurately

## Installation

```bash
go get github.com/BTOON-project/btoon-go
```

### Requirements

- Go 1.19 or higher
- CGO enabled
- C++ compiler (gcc/clang)
- CMake (for building btoon-core)

## Quick Start

```go
package main

import (
    "fmt"
    "log"
    
    "github.com/BTOON-project/btoon-go"
)

func main() {
    // Encode data
    data := map[string]interface{}{
        "name": "BTOON",
        "version": "0.0.1",
        "features": []string{"fast", "compact", "typed"},
        "metrics": map[string]interface{}{
            "speed": 9000,
            "size": 0.5,
        },
    }
    
    encoded, err := btoon.Encode(data)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Encoded size: %d bytes\n", len(encoded))
    
    // Decode data
    decoded, err := btoon.Decode(encoded)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Decoded: %+v\n", decoded)
}
```

## Advanced Features

### Compression

```go
// Enable compression
compressed, err := btoon.Encode(data, btoon.EncodeOptions{
    Compress:  true,
    Algorithm: btoon.CompressionZSTD, // ZLIB, LZ4, ZSTD, Brotli, Snappy
    Level:     3,
})
```

### Tabular Data

```go
// Automatically detect and optimize tabular data
records := []map[string]interface{}{
    {"id": 1, "name": "Alice", "age": 30},
    {"id": 2, "name": "Bob", "age": 25},
    {"id": 3, "name": "Charlie", "age": 35},
}

tabular, err := btoon.Encode(records, btoon.EncodeOptions{
    AutoTabular: true, // Automatically uses columnar encoding
})
```

### Streaming

```go
// Stream encoding
encoder := btoon.NewStreamEncoder()
defer encoder.Close()

for i := 0; i < 1000; i++ {
    err := encoder.Encode(map[string]interface{}{
        "index": i,
        "data": fmt.Sprintf("item_%d", i),
    })
    if err != nil {
        log.Fatal(err)
    }
}

data, err := encoder.Flush()

// Stream decoding
decoder := btoon.NewStreamDecoder(data)
defer decoder.Close()

for {
    obj, err := decoder.Decode()
    if err != nil {
        break
    }
    fmt.Printf("Decoded: %+v\n", obj)
}
```

### Type-Safe Encoding

```go
type User struct {
    ID        int      `btoon:"id"`
    Name      string   `btoon:"name"`
    Email     string   `btoon:"email"`
    Active    bool     `btoon:"active"`
    CreatedAt time.Time `btoon:"created_at"`
}

user := User{
    ID:        1,
    Name:      "John Doe",
    Email:     "john@example.com",
    Active:    true,
    CreatedAt: time.Now(),
}

// Encode struct
encoded, err := btoon.Encode(user)

// Decode to struct
var decoded User
err = btoon.DecodeTo(encoded, &decoded)
```

## API Reference

### Core Functions

#### `Encode(v interface{}, opts ...EncodeOptions) ([]byte, error)`
Encode Go value to BTOON format.

#### `Decode(data []byte, opts ...DecodeOptions) (interface{}, error)`
Decode BTOON data to Go value.

#### `DecodeTo(data []byte, v interface{}) error`
Decode BTOON data into a specific Go type.

### Options

#### `EncodeOptions`
```go
type EncodeOptions struct {
    Compress     bool
    Algorithm    CompressionAlgorithm
    Level        int
    AutoTabular  bool
}
```

#### `DecodeOptions`
```go
type DecodeOptions struct {
    Decompress bool
}
```

### Streaming

#### `NewStreamEncoder() *StreamEncoder`
Create a new streaming encoder.

#### `NewStreamDecoder(data []byte) *StreamDecoder`
Create a new streaming decoder.

## Performance

BTOON provides significant performance improvements:

| Operation | JSON | Gob | BTOON | Improvement |
|-----------|------|-----|-------|-------------|
| Encode 1MB | 25ms | 18ms | 5ms | 5x faster |
| Decode 1MB | 30ms | 15ms | 4ms | 7.5x faster |
| Size | 1024KB | 680KB | 412KB | 60% smaller |

### Benchmarks

Run benchmarks:
```bash
make bench
```

Sample results:
```
BenchmarkEncode-8         300000      4521 ns/op     512 B/op      8 allocs/op
BenchmarkDecode-8         500000      3234 ns/op     256 B/op      5 allocs/op
BenchmarkCompress-8       100000     15632 ns/op    1024 B/op     12 allocs/op
```

## Examples

See the [`examples/`](examples/) directory:
- [`basic/`](examples/basic/) - Basic encoding/decoding
- [`compression/`](examples/compression/) - Compression features
- [`streaming/`](examples/streaming/) - Stream processing
- [`tabular/`](examples/tabular/) - Tabular data handling

## Building from Source

```bash
# Clone repository
git clone --recursive https://github.com/BTOON-project/btoon-go.git
cd btoon-go

# Build C++ core
make core

# Build Go library
make build

# Run tests
make test

# Run examples
make examples
```

## Development

```bash
# Format code
make fmt

# Run linters
make lint

# Run all checks
make check

# Generate coverage
make coverage
```

## CGO Notes

This library uses CGO to interface with the C++ implementation. To disable CGO (will result in reduced functionality):

```bash
CGO_ENABLED=0 go build
```

For cross-compilation:
```bash
# Linux
GOOS=linux GOARCH=amd64 make build

# Windows
GOOS=windows GOARCH=amd64 make build

# macOS
GOOS=darwin GOARCH=arm64 make build
```

## Contributing

Contributions are welcome! Please read our [Contributing Guide](CONTRIBUTING.md) for details.

## License

MIT License - see [LICENSE](LICENSE) file for details.

## Links

- [Website](https://btoon.net)
- [GitHub](https://github.com/BTOON-project/btoon-go)
- [Documentation](https://pkg.go.dev/github.com/BTOON-project/btoon-go)
- [C++ Core](https://github.com/BTOON-project/btoon-core)

---

Part of the BTOON project - High-performance binary serialization for modern applications.