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
// Each document is buffered in full before it is decoded, and the options
// bound it, as btoon-core's StreamDecoder does: a document can take at most
// DecodeOptions.MaxDecompressedSize bytes (64 MiB by default) and decode to
// at most MaxValues values (2^20 by default). A document over either limit
// fails with CodeSizeLimit as soon as the Decoder has read enough of it to
// tell, so memory use stays below the byte limit whatever the source sends.
type Decoder struct {
	r    *bufio.Reader
	opts DecodeOptions
	buf  []byte
	// maxBytes is the size limit of the document being read.
	maxBytes uint64
	// err is returned by every Decode after a document went over a limit or
	// the reader failed inside one: the Decoder doesn't know where the next
	// document starts.
	err error
}

// NewDecoder returns a Decoder that reads from r. It buffers its input, so
// it may read beyond the documents it has returned.
func NewDecoder(r io.Reader) *Decoder { return &Decoder{r: bufio.NewReader(r)} }

// SetOptions sets the options used by subsequent calls to Decode. Stream
// documents are bare, so HMACKey must be nil and DisableDecompression has no
// effect; MaxDecompressedSize bounds the size of each document.
func (d *Decoder) SetOptions(o DecodeOptions) { d.opts = o }

// More reports whether there is another document in the stream.
func (d *Decoder) More() bool {
	_, err := d.r.Peek(1)
	return err == nil
}

// Decode reads the next document and stores it in the value pointed to by
// v, as Unmarshal would; v may also be a pointer to an interface value to
// get the dynamic value. At the end of the stream Decode returns io.EOF.
//
// A stream that ends inside a document fails as Decode fails on the bytes
// it holds: with CodeTruncated, unless the decoder finds an error first,
// such as a value past MaxValues (SPEC §7.1). A document over the limits
// (see Decoder) fails with CodeSizeLimit, unless the decoder finds an error
// in the bytes before; after that, and after a reader error inside a
// document, Decode returns the same error every time.
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
	if d.err != nil {
		return d.err
	}
	doc, err := d.next(&cfg)
	if err != nil {
		var be *Error
		if !errors.As(err, &be) || (be.Code != CodeTruncated && be.Code != CodeSizeLimit) {
			// io.EOF, or the reader failed. Inside a document, the Decoder
			// has lost its place in the stream.
			if err != io.EOF && len(d.buf) > 0 {
				d.err = err
			}
			return err
		}
		// The stream ended inside a document, or the document went over a
		// limit. Report what Decode reports for the same bytes if it finds
		// an error first: an invalid value, or a value counted past
		// MaxValues as the input ends (SPEC §7.1).
		if _, derr := cfg.decodeBare(d.buf, false); derr != nil {
			var de *Error
			if !errors.As(derr, &de) || de.Code != CodeTruncated {
				err = derr
			}
		}
		if be.Code == CodeSizeLimit {
			d.err = err
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
//
// It enforces the document limits as it goes, never buffering more than
// MaxDecompressedSize bytes. It also counts the items it frames: values and
// map keys, which Decode doesn't count. A map has as many keys as values, so
// a document of more than 2 × MaxValues items decodes to more than MaxValues
// values, and Decode would fail on it too.
func (d *Decoder) next(cfg *decodeConfig) ([]byte, error) {
	d.buf = d.buf[:0]
	d.maxBytes = uint64(cfg.maxDecompressed)
	items := uint64(0)
	for pending := uint64(1); pending > 0; pending-- {
		if items++; items > 2*uint64(cfg.maxValues) {
			return nil, errorAt(CodeSizeLimit, "stream document decodes to more values than max_values allows", len(d.buf))
		}
		if uint64(len(d.buf)) >= d.maxBytes {
			return nil, d.full()
		}
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

// full reports a document that needs more bytes than the limit allows:
// with CodeSizeLimit if the stream holds more, or as truncated if it ends
// there, which is what Decode reports for the bytes buffered so far.
func (d *Decoder) full() error {
	if _, err := d.r.Peek(1); err != nil {
		if err == io.EOF {
			return d.truncated()
		}
		return err
	}
	return errorAt(CodeSizeLimit, "stream document is larger than max_decompressed_size ("+utoa(d.maxBytes)+" bytes)", len(d.buf))
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
		if uint64(len(d.buf)) >= d.maxBytes {
			return d.full()
		}
		chunk := int(min(n, 64<<10, d.maxBytes-uint64(len(d.buf))))
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
