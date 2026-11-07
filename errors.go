package btoon

import "strconv"

// Error codes. The codes from CodeTruncated to CodeAuthenticationFailed are
// defined by SPEC §7 and are shared by every BTOON implementation; the
// conformance vectors compare them. CodeInvalidArgument and CodeTypeMismatch
// are specific to this package.
const (
	// CodeTruncated: the input ends before a value is complete, or a length
	// runs past the end of the input.
	CodeTruncated = "truncated"
	// CodeInvalidMarker: the reserved marker byte 0xc1.
	CodeInvalidMarker = "invalid_marker"
	// CodeTrailingData: bytes remain after the top-level value (strict mode).
	CodeTrailingData = "trailing_data"
	// CodeInvalidUTF8: a string, map key or column name is not well-formed
	// UTF-8. Marshal also reports it for Go strings that are not valid UTF-8.
	CodeInvalidUTF8 = "invalid_utf8"
	// CodeNonStringKey: a map key is not a string.
	CodeNonStringKey = "non_string_key"
	// CodeDuplicateKey: a map contains the same key twice (strict mode).
	CodeDuplicateKey = "duplicate_key"
	// CodeMaxDepth: nesting deeper than the configured maximum, including
	// cyclic Go data structures passed to Marshal.
	CodeMaxDepth = "max_depth"
	// CodeInvalidExtension: a malformed BTOON extension payload (SPEC §5).
	CodeInvalidExtension = "invalid_extension"
	// CodeInvalidTabular: a malformed tabular payload (SPEC §5.6.3).
	CodeInvalidTabular = "invalid_tabular"
	// CodeInvalidEnvelope: a malformed compression or signed envelope, or a
	// compressed payload that is corrupt or does not decompress to exactly
	// the declared size.
	CodeInvalidEnvelope = "invalid_envelope"
	// CodeSizeLimit: a size above a configured or format limit.
	CodeSizeLimit = "size_limit"
	// CodeUnsupportedCompression: the compression algorithm has no
	// registered Codec.
	CodeUnsupportedCompression = "unsupported_compression"
	// CodeAuthenticationFailed: HMAC verification failed, a signature is
	// required but missing, or a signed document was given to a decoder
	// without a key.
	CodeAuthenticationFailed = "authentication_failed"

	// CodeInvalidArgument: a Go value or option that cannot be used, such as
	// an unsupported type passed to Marshal or a non-pointer passed to
	// Unmarshal.
	CodeInvalidArgument = "invalid_argument"
	// CodeTypeMismatch: Unmarshal found a value that cannot be stored in the
	// target Go type (for example a string for an int field, or an integer
	// that overflows it).
	CodeTypeMismatch = "type_mismatch"
)

// Error is the error type returned by every function in this package.
//
// Code is one of the Code constants and is meant for programs; Message is
// meant for humans. Use errors.As to obtain the *Error, or errors.Is with one
// of the Err sentinels to test for a code:
//
//	if errors.Is(err, btoon.ErrAuthenticationFailed) { ... }
type Error struct {
	// Code is the machine-readable error code (SPEC §7 or a Code constant).
	Code string
	// Message describes the problem.
	Message string
	// Offset is the byte offset in the bare document (after any envelopes
	// have been removed) at which the problem was detected, or -1.
	Offset int
	// Err is the underlying cause, if any (for example a codec error).
	Err error
}

func (e *Error) Error() string {
	s := "btoon: " + e.Code
	if e.Message != "" {
		s += ": " + e.Message
	}
	if e.Offset >= 0 {
		s += " (at byte " + strconv.Itoa(e.Offset) + ")"
	}
	if e.Err != nil {
		s += ": " + e.Err.Error()
	}
	return s
}

// Unwrap returns the underlying cause.
func (e *Error) Unwrap() error { return e.Err }

// Is reports whether target is an *Error with the same Code. It makes
// errors.Is(err, btoon.ErrTruncated) and similar checks work.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// Sentinel errors, one per code, for use with errors.Is. They are never
// returned directly.
var (
	ErrTruncated              = &Error{Code: CodeTruncated, Offset: -1}
	ErrInvalidMarker          = &Error{Code: CodeInvalidMarker, Offset: -1}
	ErrTrailingData           = &Error{Code: CodeTrailingData, Offset: -1}
	ErrInvalidUTF8            = &Error{Code: CodeInvalidUTF8, Offset: -1}
	ErrNonStringKey           = &Error{Code: CodeNonStringKey, Offset: -1}
	ErrDuplicateKey           = &Error{Code: CodeDuplicateKey, Offset: -1}
	ErrMaxDepth               = &Error{Code: CodeMaxDepth, Offset: -1}
	ErrInvalidExtension       = &Error{Code: CodeInvalidExtension, Offset: -1}
	ErrInvalidTabular         = &Error{Code: CodeInvalidTabular, Offset: -1}
	ErrInvalidEnvelope        = &Error{Code: CodeInvalidEnvelope, Offset: -1}
	ErrSizeLimit              = &Error{Code: CodeSizeLimit, Offset: -1}
	ErrUnsupportedCompression = &Error{Code: CodeUnsupportedCompression, Offset: -1}
	ErrAuthenticationFailed   = &Error{Code: CodeAuthenticationFailed, Offset: -1}
	ErrInvalidArgument        = &Error{Code: CodeInvalidArgument, Offset: -1}
	ErrTypeMismatch           = &Error{Code: CodeTypeMismatch, Offset: -1}
)

func newError(code, msg string) *Error {
	return &Error{Code: code, Message: msg, Offset: -1}
}

func errorAt(code, msg string, offset int) *Error {
	return &Error{Code: code, Message: msg, Offset: offset}
}

func wrapError(code, msg string, err error) *Error {
	return &Error{Code: code, Message: msg, Offset: -1, Err: err}
}
