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
