package wsplice

import (
	"io"

	"github.com/gobwas/ws"
)

// Disposable is an interface that describes something that can optionally
// be disposed of after it's no longer needed.
type Disposable interface {
	Dispose()
}

// Dispose calls a Disposable's Dispose() method, if the provided interface
// implements Disposable.
func Dispose(v interface{}) {
	if disposable, ok := v.(Disposable); ok {
		disposable.Dispose()
	}
}

// MaskedReader wraps an io.Reader and deciphers the content.
type MaskedReader struct {
	*io.LimitedReader
	offset int
	mask   [4]byte
}

func NewMasked(r *io.LimitedReader, offset int, mask [4]byte) *MaskedReader {
	return &MaskedReader{r, offset, mask}
}

// Read implements io.Read
func (m MaskedReader) Read(p []byte) (n int, err error) {
	offset := m.offset
	n, err = m.LimitedReader.Read(p)
	ws.Cipher(p[:n], m.mask, offset)
	m.offset += n
	return
}

// shiftCipher moves the mask to account for having remove "n" prefix bits.
func shiftCipher(mask [4]byte, n int) (out [4]byte) {
	switch n & 0x3 { // mod 4
	case 0:
		return mask
	case 1:
		out[0], out[1], out[2], out[3] = mask[1], mask[2], mask[3], mask[0]
	case 2:
		out[0], out[1], out[2], out[3] = mask[2], mask[3], mask[0], mask[1]
	case 3:
		out[0], out[1], out[2], out[3] = mask[3], mask[0], mask[1], mask[2]
	}

	return out
}
