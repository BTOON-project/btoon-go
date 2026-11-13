package btoon

// SwapCodecForTest installs codec for c (removing it if codec is nil) and
// returns a function that restores the previous registration.
func SwapCodecForTest(c Compression, codec Codec) (restore func()) {
	codecsMu.Lock()
	old, had := codecs[c]
	if codec == nil {
		delete(codecs, c)
	} else {
		codecs[c] = codec
	}
	codecsMu.Unlock()
	return func() {
		codecsMu.Lock()
		defer codecsMu.Unlock()
		if had {
			codecs[c] = old
		} else {
			delete(codecs, c)
		}
	}
}

// DecodeWithoutBudgetForTest decodes a bare document as if the document's
// reservation budget were already spent, so that every container takes the
// path for headers that announce more elements than the budget covers.
func DecodeWithoutBudgetForTest(doc []byte) (any, error) {
	d := newDecoder(doc, true, DefaultMaxDepth, false)
	d.budget = 0
	return d.document()
}
