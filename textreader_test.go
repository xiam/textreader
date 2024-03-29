package textreader_test

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/textreader"
)

func TestTestReader(t *testing.T) {
	data := "first line\nsecond line\nthird line\nfourth line\nfifth line 🦄\n"

	t.Run("ReadRune", func(t *testing.T) {
		buf := []rune{}

		tr := textreader.NewReader(strings.NewReader(data))

		startPos := tr.Position()
		assert.Equal(t, uint64(1), startPos.Line)
		assert.Equal(t, uint64(0), startPos.Column)
		assert.Equal(t, uint64(0), startPos.Offset)

		hasUnicorn := false

		for {
			r, s, err := tr.ReadRune()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			assert.Equal(t, s, len(string(r)))

			if r == '🦄' {
				hasUnicorn = true
			}

			buf = append(buf, r)
		}

		assert.Equal(t, []rune(data), buf)
		assert.True(t, hasUnicorn)

		r, size, err := tr.ReadRune()
		assert.Equal(t, io.EOF, err)
		assert.Equal(t, rune(0), r)
		assert.Equal(t, 0, size)

		endPos := tr.Position()
		assert.Equal(t, uint64(6), endPos.Line)
		assert.Equal(t, uint64(0), endPos.Column)
		assert.Equal(t, uint64(len(data)), endPos.Offset)

		assert.Equal(t, "6:0", endPos.String())
	})

	t.Run("Read", func(t *testing.T) {
		buf := make([]byte, 5)

		tr := textreader.NewReader(strings.NewReader(data))

		n, err := tr.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, 5, n)
		assert.Equal(t, []byte("first"), buf)

		endPos := tr.Position()
		assert.Equal(t, uint64(1), endPos.Line)
		assert.Equal(t, uint64(5), endPos.Column)
		assert.Equal(t, uint64(5), endPos.Offset)
	})

	t.Run("UnreadRune", func(t *testing.T) {
		tr := textreader.NewReader(strings.NewReader(data))

		r, _, err := tr.ReadRune()
		require.NoError(t, err)

		assert.Equal(t, 'f', r)
		assert.Equal(t, "1:1", tr.Position().String())

		err = tr.UnreadRune()
		require.NoError(t, err)

		assert.Equal(t, "1:0", tr.Position().String())

		err = tr.UnreadRune()
		require.Error(t, err)
	})
}
