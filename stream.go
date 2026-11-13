package btoon

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"slices"
)

// An Encoder writes a stream of concatenated bare BTOON documents.
//
// A stream cannot carry compression or signed envelopes: their magic bytes
// are also valid document prefixes (0x42 is the integer 66), so a reader
// could not tell where a document ends. Encode fails if the options ask for
// either.
type Encoder struct {
	w    io.Writer
	opts EncodeOptions
}

// NewEncoder returns an Encoder that writes to w.
func NewEncoder(w io.Writer) *Encoder { return &Encoder{w: w} }

// SetOptions sets the options used by subsequent calls to Encode.
func (e *Encoder) SetOptions(o EncodeOptions) { e.opts = o }

// Encode writes the canonical encoding of v to the stream.
func (e *Encoder) Encode(v any) error {
	if e.opts.Compress || e.opts.HMACKey != nil {
		return newError(CodeInvalidArgument, "envelopes cannot be used in a document stream")
	}
	b, err := encodeBare(v, &e.opts)
	if err != nil {
		return err
	}
	_, err = e.w.Write(b)
	return err
}

// A Decoder reads a stream of concatenated bare BTOON documents, as written
// by Encoder.
//
// Each document is buffered in full before it is decoded, so memory use is
// proportional to the largest document. When reading from an untrusted
// source, bound the input (for example with io.LimitReader).
type Decoder struct {
	r    *bufio.Reader
	opts DecodeOptions
	buf  []byte
}

// NewDecoder returns a Decoder that reads from r. It buffers its input, so
// it may read beyond the documents it has returned.
func NewDecoder(r io.Reader) *Decoder { return &Decoder{r: bufio.NewReader(r)} }

// SetOptions sets the options used by subsequent calls to Decode.
// HMACKey must be nil, and the envelope options have no effect, because
// stream documents are bare.
func (d *Decoder) SetOptions(o DecodeOptions) { d.opts = o }

// More reports whether there is another document in the stream.
func (d *Decoder) More() bool {
	_, err := d.r.Peek(1)
	return err == nil
}

// Decode reads the next document and stores it in the value pointed to by
// v, as Unmarshal would; v may also be a pointer to an interface value to
// get the dynamic value. At the end of the stream Decode returns io.EOF. A
// stream that ends inside a document fails as Decode fails on the bytes it
// holds: with CodeTruncated, unless the decoder finds an error first, such
// as a value past MaxValues (SPEC §7.1).
func (d *Decoder) Decode(v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return newError(CodeInvalidArgument, "Decode needs a non-nil pointer, not "+describeTarget(v))
	}
	if d.opts.HMACKey != nil {
		return newError(CodeInvalidArgument, "signed envelopes cannot be used in a document stream")
	}
	cfg, err := d.opts.config()
	if err != nil {
		return err
	}
	doc, err := d.next()
	if err != nil {
		var be *Error
		if !errors.As(err, &be) || be.Code != CodeTruncated {
			return err
		}
		// The stream ended inside a document. Report what Decode reports
		// for the same bytes: the decoder may find an error before the end,
		// or count a value past MaxValues as the input ends (SPEC §7.1).
		if _, derr := cfg.decodeBare(d.buf, false); derr != nil {
			return derr
		}
		return err
	}
	src, err := cfg.decodeBare(doc, false)
	if err != nil {
		return err
	}
	a := assigner{disallowUnknown: d.opts.DisallowUnknownFields}
	return a.assign(rv.Elem(), src)
}

// next reads the bytes of the next document. It follows only the framing
// of the MessagePack layer (headers and lengths, with extensions as opaque
// payloads) and leaves all validation to the decoder, so that a stream
// reports the same errors as Decode would for the same bytes.
func (d *Decoder) next() ([]byte, error) {
	d.buf = d.buf[:0]
	for pending := uint64(1); pending > 0; pending-- {
		m, err := d.r.ReadByte()
		if err != nil {
			if err == io.EOF {
				if len(d.buf) == 0 {
					return nil, io.EOF
				}
				return nil, d.truncated()
			}
			return nil, err
		}
		d.buf = append(d.buf, m)

		var skip uint64 // payload bytes following the header
		switch {
		case m <= 0x7f || m >= 0xe0:
		case m <= 0x8f:
			pending += 2 * uint64(m&0x0f)
		case m <= 0x9f:
			pending += uint64(m & 0x0f)
		case m <= 0xbf:
			skip = uint64(m & 0x1f)
		default:
			var n uint64
			switch m {
			case 0xc4, 0xd9:
				n, err = d.readLength(1)
				skip = n
			case 0xc5, 0xda:
				n, err = d.readLength(2)
				skip = n
			case 0xc6, 0xdb:
				n, err = d.readLength(4)
				skip = n
			case 0xc7:
				n, err = d.readLength(1)
				skip = n + 1 // type byte
			case 0xc8:
				n, err = d.readLength(2)
				skip = n + 1
			case 0xc9:
				n, err = d.readLength(4)
				skip = n + 1
			case 0xcc, 0xd0:
				skip = 1
			case 0xcd, 0xd1:
				skip = 2
			case 0xca, 0xce, 0xd2:
				skip = 4
			case 0xcb, 0xcf, 0xd3:
				skip = 8
			case 0xd4, 0xd5, 0xd6, 0xd7, 0xd8:
				skip = 1 + 1<<(m-0xd4)
			case 0xdc:
				n, err = d.readLength(2)
				pending += n
			case 0xdd:
				n, err = d.readLength(4)
				pending += n
			case 0xde:
				n, err = d.readLength(2)
				pending += 2 * n
			case 0xdf:
				n, err = d.readLength(4)
				pending += 2 * n
			}
			// 0xc0-0xc3 have no payload; 0xc1 is left for the decoder to
			// reject.
		}
		if err != nil {
			return nil, err
		}
		if err := d.readN(skip); err != nil {
			return nil, err
		}
		if pending > 1<<62 {
			return nil, newError(CodeSizeLimit, "document announces too many elements")
		}
	}
	return d.buf, nil
}

func (d *Decoder) truncated() error {
	return errorAt(CodeTruncated, "stream ends inside a document", len(d.buf))
}

// readLength reads a big-endian length of size bytes into the buffer.
func (d *Decoder) readLength(size int) (uint64, error) {
	start := len(d.buf)
	if err := d.readN(uint64(size)); err != nil {
		return 0, err
	}
	b := d.buf[start:]
	switch size {
	case 1:
		return uint64(b[0]), nil
	case 2:
		return uint64(binary.BigEndian.Uint16(b)), nil
	default:
		return uint64(binary.BigEndian.Uint32(b)), nil
	}
}

// readN appends the next n bytes of the stream to the buffer, growing it
// only as data actually arrives.
func (d *Decoder) readN(n uint64) error {
	for n > 0 {
		chunk := int(min(n, 64<<10))
		start := len(d.buf)
		d.buf = slices.Grow(d.buf, chunk)[:start+chunk]
		if _, err := io.ReadFull(d.r, d.buf[start:]); err != nil {
			d.buf = d.buf[:start]
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return d.truncated()
			}
			return err
		}
		n -= uint64(chunk)
	}
	return nil
}
