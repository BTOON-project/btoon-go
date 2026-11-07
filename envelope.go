package btoon

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"strconv"
)

// Compression envelope (SPEC §6) and signed envelope (SPEC §8) layouts.
const (
	compressionMagic   = "BTON"
	compressionVersion = 1
	compressionHeader  = 16
	// Encoders skip compression for smaller documents (SPEC §6).
	minCompressSize = 256

	signedMagic     = "BTSG"
	signedVersion   = 1
	signedAlgorithm = 1 // HMAC-SHA256
	signedHeader    = 40
)

func hasPrefix(b []byte, magic string) bool {
	return len(b) >= len(magic) && string(b[:len(magic)]) == magic
}

// compressDocument wraps raw in a compression envelope when that is
// worthwhile: the document is at least 256 bytes and the envelope is at
// least 5% smaller than the document. Otherwise raw is returned as is.
func compressDocument(raw []byte, algo Compression, level int) ([]byte, error) {
	codec := lookupCodec(algo)
	if codec == nil {
		return nil, newError(CodeUnsupportedCompression, "no codec registered for "+algo.String()+" (see RegisterCodec)")
	}
	if len(raw) < minCompressSize {
		return raw, nil
	}
	if uint64(len(raw)) > math.MaxUint32 {
		return nil, newError(CodeSizeLimit, "document too large for a compression envelope")
	}
	payload, err := codec.Compress(raw, level)
	if err != nil {
		return nil, wrapError(CodeInvalidArgument, algo.String()+" compression failed", err)
	}
	envLen := compressionHeader + len(payload)
	// Emit the envelope only if it saves at least 5% (SPEC §6).
	if 20*(int64(len(raw))-int64(envLen)) < int64(len(raw)) {
		return raw, nil
	}
	out := make([]byte, 0, envLen)
	out = append(out, compressionMagic...)
	out = append(out, compressionVersion, byte(algo), 0, 0)
	out = binary.BigEndian.AppendUint32(out, uint32(len(payload)))
	out = binary.BigEndian.AppendUint32(out, uint32(len(raw)))
	return append(out, payload...), nil
}

// signDocument wraps payload in a signed envelope.
func signDocument(payload, key []byte) []byte {
	out := make([]byte, 0, signedHeader+len(payload))
	out = append(out, signedMagic...)
	out = append(out, signedVersion, signedAlgorithm, 0, 0)
	mac := hmac.New(sha256.New, key)
	mac.Write(out[:8])
	mac.Write(payload)
	out = mac.Sum(out)
	return append(out, payload...)
}

// openDocument removes the signed and compression envelopes from data and
// returns the bare document. It verifies the MAC before looking at the
// payload and checks the compression header before decompressing.
func openDocument(data []byte, o *decodeConfig) ([]byte, error) {
	if o.hmacKey != nil {
		if !hasPrefix(data, signedMagic) {
			return nil, newError(CodeAuthenticationFailed, "input is not a signed envelope, but an HMAC key was given")
		}
		if len(data) < signedHeader {
			return nil, newError(CodeInvalidEnvelope, "signed envelope is truncated")
		}
		if data[4] != signedVersion || data[5] != signedAlgorithm || data[6] != 0 || data[7] != 0 {
			return nil, newError(CodeInvalidEnvelope, "unsupported signed envelope version or algorithm")
		}
		mac := hmac.New(sha256.New, o.hmacKey)
		mac.Write(data[:8])
		mac.Write(data[signedHeader:])
		if !hmac.Equal(mac.Sum(nil), data[8:signedHeader]) {
			return nil, newError(CodeAuthenticationFailed, "HMAC verification failed")
		}
		data = data[signedHeader:]
	} else if len(data) >= signedHeader && hasPrefix(data, signedMagic) {
		return nil, newError(CodeAuthenticationFailed, "input is signed, but no HMAC key was given")
	}

	if len(data) < compressionHeader || !hasPrefix(data, compressionMagic) {
		return data, nil
	}
	if o.noDecompress {
		return nil, newError(CodeInvalidEnvelope, "input is compressed, but decompression is disabled")
	}
	version, algo := data[4], Compression(data[5])
	reserved := binary.BigEndian.Uint16(data[6:])
	csize := binary.BigEndian.Uint32(data[8:])
	usize := binary.BigEndian.Uint32(data[12:])
	switch {
	case version != compressionVersion:
		return nil, newError(CodeInvalidEnvelope, "unsupported compression envelope version "+strconv.Itoa(int(version)))
	case reserved != 0:
		return nil, newError(CodeInvalidEnvelope, "compression envelope reserved bytes are not zero")
	case uint64(csize) != uint64(len(data)-compressionHeader):
		return nil, newError(CodeInvalidEnvelope, "compressed_size does not match the envelope length")
	case uint64(usize) > uint64(o.maxDecompressed):
		return nil, newError(CodeSizeLimit, "uncompressed_size "+utoa(uint64(usize))+" exceeds the limit of "+strconv.Itoa(o.maxDecompressed)+" bytes")
	}
	codec := lookupCodec(algo)
	if codec == nil {
		return nil, newError(CodeUnsupportedCompression, "no codec registered for "+algo.String())
	}
	out, err := codec.Decompress(data[compressionHeader:], int(usize))
	if err != nil {
		var be *Error
		if errors.As(err, &be) && be.Code != CodeInvalidArgument {
			return nil, be
		}
		return nil, wrapError(CodeInvalidEnvelope, algo.String()+" payload is corrupt", err)
	}
	if len(out) != int(usize) {
		return nil, newError(CodeInvalidEnvelope, algo.String()+" payload does not decompress to uncompressed_size")
	}
	return out, nil
}
