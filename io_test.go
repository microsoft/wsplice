package wsplice

import (
	"math/rand"
	"testing"

	"github.com/gobwas/ws"
	"github.com/stretchr/testify/require"
)

func TestShiftCipher(t *testing.T) {
	for i := 0; i < 20; i++ {
		var mask [4]byte
		for i := 0; i < len(mask); i++ {
			mask[i] = byte(rand.Int())
		}

		original := make([]byte, rand.Intn(64))
		for i := 0; i < len(original); i++ {
			original[i] = byte(rand.Int())
		}
		masked := make([]byte, len(original))
		copy(masked, original)
		ws.Cipher(masked, mask, 0)

		for n := 0; n < len(mask) && n < len(original); n++ {
			actual := make([]byte, len(original))
			copy(actual, masked)
			ws.Cipher(actual[n:], shiftCipher(mask, n), 0)
			require.Equal(t, original[n:], actual[n:])
		}
	}
}

func BenchmarkShiftCipher(b *testing.B) {
	mask := [4]byte{1, 2, 3, 4}
	for i := 0; i < b.N; i++ {
		mask = shiftCipher(mask, i)
	}
}
