package textreader_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/textreader"
)

var (
	_ io.Reader     = (*textreader.TextReader)(nil)
	_ io.ByteReader = (*textreader.TextReader)(nil)
	_ io.RuneReader = (*textreader.TextReader)(nil)
	_ io.Seeker     = (*textreader.TextReader)(nil)
)

func TestTextReader(t *testing.T) {
	data := "first line\nsecond line\nthird line\nfourth line\nfifth line 🦄\n"

	t.Run("look for the unicorn", func(t *testing.T) {
		reader := textreader.New(strings.NewReader(data))

		runes := []rune(data)

		hasUnicorn := false

		for i := 0; ; i++ {
			r, _, err := reader.ReadRune()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}

				require.NoError(t, err)
			}

			if r == '🦄' {
				hasUnicorn = true
			}

			require.NoError(t, err)
			assert.Equal(t, runes[i], r)
		}

		{
			pos := reader.Pos()
			assert.Equal(t, 6, pos.Line())
			assert.Equal(t, 0, pos.Column())
			assert.Equal(t, len(data), pos.Offset())
		}

		assert.True(t, hasUnicorn)

		_, _, err := reader.ReadRune()
		require.Equal(t, io.EOF, err)

		err = reader.UnreadRune()
		require.NoError(t, err)

		err = reader.UnreadRune()
		require.Error(t, err)

		r, s, err := reader.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, 1, s)
		assert.Equal(t, '\n', r)
	})

	t.Run("read beyond buffer capacity", func(t *testing.T) {
		tr := textreader.NewWithCapacity(
			strings.NewReader(data),
			4,
		)
		{
			buf := make([]byte, 3)
			n, err := tr.Read(buf)
			require.NoError(t, err)
			assert.Equal(t, 3, n)
			assert.Equal(t, []byte(data[0:3]), buf)

			{
				pos := tr.Pos()
				assert.Equal(t, 1, pos.Line())
				assert.Equal(t, 3, pos.Column())
				assert.Equal(t, 3, pos.Offset())
			}
		}

		{
			buf := make([]byte, 4)
			n, err := tr.Read(buf)
			require.NoError(t, err)
			assert.Equal(t, 4, n)
			assert.Equal(t, []byte(data[3:3+4]), buf)
		}

		{
			buf := make([]byte, 5)
			n, err := tr.Read(buf)
			require.NoError(t, err)
			assert.Equal(t, 5, n)
			assert.Equal(t, []byte(data[7:7+5]), buf)

			{
				pos := tr.Pos()
				assert.Equal(t, 2, pos.Line())
				assert.Equal(t, 1, pos.Column())
				assert.Equal(t, 12, pos.Offset())
			}
		}
		{
			offset := tr.Pos().Offset()

			buf := make([]byte, 15)
			n, err := tr.Read(buf)
			require.NoError(t, err)
			assert.Equal(t, 15, n)
			assert.Equal(t, []byte(data[offset:offset+15]), buf)
		}
		{
			offset := tr.Pos().Offset()

			buf := make([]byte, 100)
			n, err := tr.Read(buf)

			require.NoError(t, err)
			assert.Equal(t, 35, n)
			assert.Equal(t, []byte(data[offset:]), buf[:n])

			{
				pos := tr.Pos()
				assert.Equal(t, 6, pos.Line())
				assert.Equal(t, 0, pos.Column())
				assert.Equal(t, 62, pos.Offset())
			}
		}
	})

	t.Run("read and unread bytes", func(t *testing.T) {
		tr := textreader.New(strings.NewReader(data))

		b, err := tr.ReadByte()
		require.NoError(t, err)

		assert.Equal(t, byte('f'), b)

		{
			pos := tr.Pos()
			assert.Equal(t, 1, pos.Line())
			assert.Equal(t, 1, pos.Column())
			assert.Equal(t, 1, pos.Offset())
		}

		err = tr.UnreadByte()
		require.NoError(t, err)

		{
			pos := tr.Pos()
			assert.Equal(t, 1, pos.Line())
			assert.Equal(t, 0, pos.Column())
			assert.Equal(t, 0, pos.Offset())
		}

		err = tr.UnreadByte()
		require.Error(t, err)

		{
			pos := tr.Pos()
			assert.Equal(t, 1, pos.Line())
			assert.Equal(t, 0, pos.Column())
			assert.Equal(t, 0, pos.Offset())
		}

		b, err = tr.ReadByte()
		require.NoError(t, err)
		assert.Equal(t, byte('f'), b)

		{
			pos := tr.Pos()
			assert.Equal(t, 1, pos.Line())
			assert.Equal(t, 1, pos.Column())
			assert.Equal(t, 1, pos.Offset())
		}
	})

	t.Run("seeker", func(t *testing.T) {
		tr := textreader.New(strings.NewReader(data))

		buf := make([]byte, 15)

		n, err := tr.Read(buf)
		require.NoError(t, err)

		assert.Equal(t, 15, n)

		t.Run("seek to start + 17", func(t *testing.T) {
			offset, err := tr.Seek(17, io.SeekStart)
			require.NoError(t, err)
			assert.Equal(t, int64(17), offset)

			b, err := tr.ReadByte()
			require.NoError(t, err)
			assert.Equal(t, byte(' '), b)

			{
				pos := tr.Pos()
				assert.Equal(t, 2, pos.Line())
				assert.Equal(t, 7, pos.Column())
				assert.Equal(t, 18, pos.Offset())
			}

			err = tr.UnreadByte()
			require.NoError(t, err)

			{
				pos := tr.Pos()
				assert.Equal(t, 2, pos.Line())
				assert.Equal(t, 6, pos.Column())
				assert.Equal(t, 17, pos.Offset())
			}
		})

		t.Run("seek to end", func(t *testing.T) {
			offset, err := tr.Seek(0, io.SeekEnd)
			require.NoError(t, err)

			t.Logf("offset: %d", offset)

			{
				pos := tr.Pos()
				assert.Equal(t, int(offset), pos.Offset())
			}

			{
				_, err := tr.ReadByte()
				require.Error(t, err)
			}
		})

		t.Run("seek to end + 1", func(t *testing.T) {
			offset, err := tr.Seek(1, io.SeekEnd)
			require.Error(t, err)

			assert.Equal(t, int64(0), offset)

			{
				_, err := tr.ReadByte()
				require.Error(t, err)
			}
		})

		t.Run("seek to end - 1", func(t *testing.T) {
			offset, err := tr.Seek(-1, io.SeekEnd)
			require.NoError(t, err)

			assert.Equal(t, len(data)-1, int(offset))

			{
				pos := tr.Pos()
				assert.Equal(t, 5, pos.Line())
				assert.Equal(t, 15, pos.Column())
				assert.Equal(t, len(data)-1, pos.Offset())
			}

			b, err := tr.ReadByte()
			require.NoError(t, err)
			assert.Equal(t, byte('\n'), b)

			{
				pos := tr.Pos()
				assert.Equal(t, 6, pos.Line())
				assert.Equal(t, 0, pos.Column())
				assert.Equal(t, len(data), pos.Offset())
			}
		})

		t.Run("seek to start", func(t *testing.T) {
			offset, err := tr.Seek(0, io.SeekStart)
			require.NoError(t, err)
			assert.Equal(t, int64(0), offset)

			{
				pos := tr.Pos()
				assert.Equal(t, 1, pos.Line())
				assert.Equal(t, 0, pos.Column())
				assert.Equal(t, 0, pos.Offset())
			}

			b, err := tr.ReadByte()
			require.NoError(t, err)
			assert.Equal(t, byte('f'), b)

			{
				pos := tr.Pos()
				assert.Equal(t, 1, pos.Line())
				assert.Equal(t, 1, pos.Column())
				assert.Equal(t, 1, pos.Offset())
			}
		})

		t.Run("seek to start - 1", func(t *testing.T) {
			offset, err := tr.Seek(-1, io.SeekStart)
			require.Error(t, err)
			assert.Equal(t, int64(0), offset)

			{
				pos := tr.Pos()
				assert.Equal(t, 1, pos.Line())
				assert.Equal(t, 1, pos.Column())
				assert.Equal(t, 1, pos.Offset())
			}
		})

		t.Run("seek beyond input", func(t *testing.T) {
			offset, err := tr.Seek(200, io.SeekEnd)
			require.Error(t, err)
			assert.Equal(t, int64(0), offset)

			{
				pos := tr.Pos()
				assert.Equal(t, 1, pos.Line())
				assert.Equal(t, 1, pos.Column())
				assert.Equal(t, 1, pos.Offset())
			}

			b, err := tr.ReadByte()
			require.NoError(t, err)
			assert.Equal(t, byte('i'), b)

			{
				pos := tr.Pos()
				assert.Equal(t, 1, pos.Line())
				assert.Equal(t, 2, pos.Column())
				assert.Equal(t, 2, pos.Offset())
			}
		})
	})
}
