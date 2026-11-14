package btoon_test

import (
	"testing"

	"github.com/BTOON-project/btoon-go/internal/conformance"
)

// The conformance vectors are a verbatim copy of btoon-core's
// tests/vectors/btoon-vectors.json (run `make sync-vectors`). SPEC §10
// describes the format, and package conformance runs them. This package's
// tests don't register the optional codecs, so the vectors that need one
// are skipped here; the tests of package codecs run them all.

func loadVectors(tb testing.TB) *conformance.File {
	tb.Helper()
	return conformance.Load(tb, "testdata/btoon-vectors.json")
}

func TestConformanceVectors(t *testing.T) {
	conformance.Run(t, loadVectors(t), false)
}
