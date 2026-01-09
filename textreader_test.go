package textreader

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/textreader/position"
)

// Compile-time checks to ensure TextReader implements expected interfaces.
var (
	_ io.Reader     = (*TextReader)(nil)
	_ io.RuneReader = (*TextReader)(nil)
	_ io.RuneScanner = (*TextReader)(nil)
	_ io.Seeker     = (*TextReader)(nil)
)

func newReader(text string, capacity int) *TextReader {
	if capacity <= 0 {
		return New(strings.NewReader(text))
	}
	return NewWithCapacity(strings.NewReader(text), capacity)
}

func readAllRunes(tr *TextReader) (string, error) {
	var sb strings.Builder
	for {
		r, _, err := tr.ReadRune()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return sb.String(), nil
			}
			return sb.String(), err
		}
		sb.WriteRune(r)
	}
}

func readAllBytes(tr *TextReader) ([]byte, error) {
	return io.ReadAll(tr)
}

func TestTextReader(t *testing.T) {
	data := "first line\nsecond line\nthird line\nfourth line\nfifth line 🦄\n"

	t.Run("look for the unicorn", func(t *testing.T) {
		reader := New(strings.NewReader(data))

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

		// Only one level of unread is supported
		err = reader.UnreadRune()
		require.Error(t, err)

		r, s, err := reader.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, 1, s)
		assert.Equal(t, '\n', r)
	})

	t.Run("read beyond buffer capacity", func(t *testing.T) {
		// Small buffer (4 bytes) to test direct-read path when request exceeds buffer
		tr := NewWithCapacity(
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
			// 5 bytes exceeds buffer capacity - triggers direct read
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

	t.Run("read runes and seek back", func(t *testing.T) {
		tr := New(strings.NewReader(data))

		r, _, err := tr.ReadRune()
		require.NoError(t, err)

		assert.Equal(t, 'f', r)

		{
			pos := tr.Pos()
			assert.Equal(t, 1, pos.Line())
			assert.Equal(t, 1, pos.Column())
			assert.Equal(t, 1, pos.Offset())
		}

		// Use Seek to go back one rune
		_, err = tr.Seek(-1, io.SeekCurrent)
		require.NoError(t, err)

		{
			pos := tr.Pos()
			assert.Equal(t, 1, pos.Line())
			assert.Equal(t, 0, pos.Column())
			assert.Equal(t, 0, pos.Offset())
		}

		// Seeking before start should fail
		_, err = tr.Seek(-1, io.SeekCurrent)
		require.Error(t, err)

		{
			pos := tr.Pos()
			assert.Equal(t, 1, pos.Line())
			assert.Equal(t, 0, pos.Column())
			assert.Equal(t, 0, pos.Offset())
		}

		r, _, err = tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, 'f', r)

		{
			pos := tr.Pos()
			assert.Equal(t, 1, pos.Line())
			assert.Equal(t, 1, pos.Column())
			assert.Equal(t, 1, pos.Offset())
		}
	})

	t.Run("seeker", func(t *testing.T) {
		tr := New(strings.NewReader(data))

		buf := make([]byte, 15)
		n, err := tr.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, 15, n)

		t.Run("seek to start + 17", func(t *testing.T) {
			offset, err := tr.Seek(17, io.SeekStart)
			require.NoError(t, err)
			assert.Equal(t, int64(17), offset)

			r, _, err := tr.ReadRune()
			require.NoError(t, err)
			assert.Equal(t, ' ', r)

			{
				pos := tr.Pos()
				assert.Equal(t, 2, pos.Line())
				assert.Equal(t, 7, pos.Column())
				assert.Equal(t, 18, pos.Offset())
			}

			// Use Seek to go back one rune
			_, err = tr.Seek(-1, io.SeekCurrent)
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
				_, _, err := tr.ReadRune()
				require.Error(t, err)
			}
		})

		t.Run("seek to end + 1", func(t *testing.T) {
			offset, err := tr.Seek(1, io.SeekEnd)
			require.Error(t, err)
			assert.Equal(t, int64(0), offset)

			{
				_, _, err := tr.ReadRune()
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
				// "fifth line 🦄" = 11 chars + 1 emoji = 12 runes (Column counts runes, not bytes)
				assert.Equal(t, 12, pos.Column())
				assert.Equal(t, len(data)-1, pos.Offset())
			}

			r, _, err := tr.ReadRune()
			require.NoError(t, err)
			assert.Equal(t, '\n', r)

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

			r, _, err := tr.ReadRune()
			require.NoError(t, err)
			assert.Equal(t, 'f', r)

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

			r, _, err := tr.ReadRune()
			require.NoError(t, err)
			assert.Equal(t, 'i', r)

			{
				pos := tr.Pos()
				assert.Equal(t, 1, pos.Line())
				assert.Equal(t, 2, pos.Column())
				assert.Equal(t, 2, pos.Offset())
			}
		})
	})

	t.Run("seek backwards after buffer shift", func(t *testing.T) {
		const (
			capacity = 16
			input    = "abcdefghijklmnopqrstuvwxyz"
		)

		r := NewWithCapacity(strings.NewReader(input), capacity)

		// Force buffer shift by reading beyond initial capacity
		readBytes := make([]byte, 12)
		_, err := io.ReadFull(r, readBytes)
		require.NoError(t, err, "Initial read failed")

		readBytes = make([]byte, 10)
		_, err = io.ReadFull(r, readBytes)
		require.NoError(t, err, "Second read failed")

		nextRune, _, err := r.ReadRune()
		require.NoError(t, err, "ReadRune after setup failed")
		require.Equal(t, 'w', nextRune, "Expected to be at 'w' after setup")

		// Use Seek to go back
		_, err = r.Seek(-1, io.SeekCurrent)
		require.NoError(t, err, "Seek back 1 rune failed")

		// Seeking backwards to 'r' which should still be in the shifted buffer
		newPos, err := r.Seek(-5, io.SeekCurrent)
		require.NoError(t, err, "Seek(-5, io.SeekCurrent) failed, but should be a valid seek")

		expectedPos := int64(17)
		require.Equal(t, expectedPos, newPos, "Seek returned incorrect new position")

		ru, _, err := r.ReadRune()
		require.NoError(t, err, "ReadRune after seek failed")
		assert.Equal(t, 'r', ru, "Read wrong rune after seek")
	})
}

func TestTextReaderEdgeCases(t *testing.T) {

	t.Run("EmptyInput", func(t *testing.T) {
		tr := newReader("", 10)

		r, size, err := tr.ReadRune()
		assert.ErrorIs(t, err, io.EOF)
		assert.Equal(t, rune(0), r)
		assert.Equal(t, 0, size)

		buf := make([]byte, 10)
		n, err := tr.Read(buf)
		assert.ErrorIs(t, err, io.EOF)
		assert.Equal(t, 0, n)

		_, err = tr.Seek(0, io.SeekCurrent)
		assert.NoError(t, err)

		_, err = tr.Seek(1, io.SeekStart)
		assert.ErrorIs(t, err, ErrSeekOutOfBuffer)

		assert.ErrorIs(t, tr.UnreadRune(), bufio.ErrInvalidUnreadRune)
	})

	t.Run("SmallCapacity_ReadRune", func(t *testing.T) {
		input := "你好世界"
		tr := newReader(input, utf8.UTFMax)
		var result strings.Builder

		for i, expRune := range input {
			r, size, err := tr.ReadRune()
			require.NoError(t, err, "Failed at rune index %d", i)
			assert.Equal(t, expRune, r)
			assert.Equal(t, utf8.RuneLen(expRune), size)
			result.WriteRune(r)
		}

		assert.Equal(t, input, result.String())

		_, _, err := tr.ReadRune()
		assert.ErrorIs(t, err, io.EOF)
	})

	t.Run("ReadRune_InvalidUTF8", func(t *testing.T) {
		invalidData := "a你好\xffdef"
		tr := newReader(invalidData, 10)

		out, err := readAllRunes(tr)
		require.NoError(t, err)

		expected := "a你好" + string(utf8.RuneError) + "def"
		assert.Equal(t, expected, out)

		tr = newReader(invalidData, 10)

		r, _, err := tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, 'a', r)
		r, _, err = tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, '你', r)
		r, _, err = tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, '好', r)

		// Invalid UTF-8 byte returns RuneError with size=1 and err=nil
		r, size, err := tr.ReadRune()
		assert.NoError(t, err, "Reading an invalid UTF-8 byte should not return an error")
		assert.Equal(t, utf8.RuneError, r, "The returned rune should be the standard RuneError")
		assert.Equal(t, 1, size, "The reader should advance by 1 byte to skip the invalid sequence")

		r, _, err = tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, 'd', r)
	})

	t.Run("ReadRune_Boundary", func(t *testing.T) {
		// Position '世' (3 bytes) to span the buffer boundary
		text := strings.Repeat("a", 9) + "世" + strings.Repeat("b", 10)
		tr := newReader(text, 10)

		for i := 0; i < 9; i++ {
			r, _, err := tr.ReadRune()
			require.NoError(t, err)
			assert.Equal(t, 'a', r)
		}

		r, size, err := tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, '世', r)
		assert.Equal(t, 3, size)

		for i := 0; i < 10; i++ {
			r, _, err := tr.ReadRune()
			require.NoError(t, err)
			assert.Equal(t, 'b', r)
		}

		_, _, err = tr.ReadRune()
		assert.ErrorIs(t, err, io.EOF)
	})

	t.Run("UnreadRune", func(t *testing.T) {
		text := "a界b"
		tr := newReader(text, 10)

		r, size, err := tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, 'a', r)
		assert.Equal(t, 1, size)
		assert.Equal(t, 1, tr.Pos().Offset())

		r, size, err = tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, '界', r)
		assert.Equal(t, 3, size)
		assert.Equal(t, 1+3, tr.Pos().Offset())

		err = tr.UnreadRune()
		require.NoError(t, err)
		assert.Equal(t, 1, tr.Pos().Offset())

		r, size, err = tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, '界', r)
		assert.Equal(t, 3, size)
		assert.Equal(t, 1+3, tr.Pos().Offset())

		err = tr.UnreadRune()
		require.NoError(t, err)
		err = tr.UnreadRune()
		assert.ErrorIs(t, err, bufio.ErrInvalidUnreadRune)
		assert.Equal(t, 1, tr.Pos().Offset())

		r, _, _ = tr.ReadRune()
		assert.Equal(t, '界', r)
		r, _, _ = tr.ReadRune()
		assert.Equal(t, 'b', r)

		_, _, err = tr.ReadRune()
		assert.ErrorIs(t, err, io.EOF)

		err = tr.UnreadRune()
		require.NoError(t, err)
		assert.Equal(t, 1+3, tr.Pos().Offset())

		r, size, err = tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, 'b', r)
		assert.Equal(t, 1, size)
		assert.Equal(t, 1+3+1, tr.Pos().Offset())
	})

	t.Run("SeekBackAfterReadRune", func(t *testing.T) {
		text := "abc"
		tr := newReader(text, 10)

		r, _, err := tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, 'a', r)
		assert.Equal(t, 1, tr.Pos().Offset())

		r, _, err = tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, 'b', r)
		assert.Equal(t, 2, tr.Pos().Offset())

		// Use Seek to go back one rune
		_, err = tr.Seek(-1, io.SeekCurrent)
		require.NoError(t, err)
		assert.Equal(t, 1, tr.Pos().Offset())

		r, _, err = tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, 'b', r)
		assert.Equal(t, 2, tr.Pos().Offset())

		r, _, _ = tr.ReadRune()
		assert.Equal(t, 'c', r)

		_, _, err = tr.ReadRune()
		assert.ErrorIs(t, err, io.EOF)

		// Seek back from end
		_, err = tr.Seek(-1, io.SeekCurrent)
		require.NoError(t, err)
		assert.Equal(t, 2, tr.Pos().Offset())

		r, _, err = tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, 'c', r)
		assert.Equal(t, 3, tr.Pos().Offset())
	})

	t.Run("MixedReadAndSeek", func(t *testing.T) {
		text := "a你好b"
		tr := newReader(text, 10)

		// Read 'a'
		r, _, err := tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, 'a', r)
		assert.Equal(t, 1, tr.Pos().Offset())

		// Read '你'
		r, _, err = tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, '你', r)
		assert.Equal(t, 1+3, tr.Pos().Offset())

		err = tr.UnreadRune()
		require.NoError(t, err)
		assert.Equal(t, 1, tr.Pos().Offset())

		// Read '你' again
		r, _, err = tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, '你', r)
		assert.Equal(t, 4, tr.Pos().Offset())

		// Read '好'
		r, _, err = tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, '好', r)
		assert.Equal(t, 4+3, tr.Pos().Offset())

		// Read 'b'
		r, _, err = tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, 'b', r)
		assert.Equal(t, 4+3+1, tr.Pos().Offset())

		// Use Seek to go back one rune
		_, err = tr.Seek(-1, io.SeekCurrent)
		require.NoError(t, err)
		assert.Equal(t, 4+3, tr.Pos().Offset())

		// UnreadRune after Seek should fail
		err = tr.UnreadRune()
		assert.ErrorIs(t, err, bufio.ErrInvalidUnreadRune)
	})

	t.Run("Read_LargerThanCapacity", func(t *testing.T) {
		text := strings.Repeat("x", 100)
		capacity := 10
		tr := newReader(text, capacity)

		buf := make([]byte, 20)
		n, err := tr.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, 20, n)
		assert.Equal(t, strings.Repeat("x", 20), string(buf))
		assert.Equal(t, 20, tr.Pos().Offset())

		// Verify internal buffer was reset after direct read
		assert.Equal(t, 0, tr.w)
		assert.Equal(t, 0, tr.r)
		assert.Equal(t, -1, tr.lastRuneSize)

		n, err = tr.Read(buf[:5])
		require.NoError(t, err)
		assert.Equal(t, 5, n)
		assert.Equal(t, "xxxxx", string(buf[:5]))
		assert.Equal(t, 25, tr.Pos().Offset())

		remaining, err := readAllBytes(tr)
		require.NoError(t, err)
		assert.Equal(t, 75, len(remaining))
		assert.Equal(t, strings.Repeat("x", 75), string(remaining))
		assert.Equal(t, 100, tr.Pos().Offset())

		_, err = tr.Read(buf)
		assert.ErrorIs(t, err, io.EOF)
	})

	t.Run("Read_SmallerThanCapacity", func(t *testing.T) {
		text := strings.Repeat("y", 50)
		capacity := 20
		tr := newReader(text, capacity)

		buf := make([]byte, 15)
		n, err := tr.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, 15, n)
		assert.Equal(t, strings.Repeat("y", 15), string(buf))
		assert.Equal(t, 15, tr.Pos().Offset())

		assert.Equal(t, capacity, tr.w)
		assert.Equal(t, 15, tr.r)

		n, err = tr.Read(buf[:10])
		require.NoError(t, err)
		assert.Equal(t, 10, n)
		assert.Equal(t, strings.Repeat("y", 10), string(buf[:10]))
		assert.Equal(t, 25, tr.Pos().Offset())

		remaining, err := readAllBytes(tr)
		require.NoError(t, err)
		assert.Equal(t, 25, len(remaining))
		assert.Equal(t, strings.Repeat("y", 25), string(remaining))
		assert.Equal(t, 50, tr.Pos().Offset())

		_, err = tr.Read(buf)
		assert.ErrorIs(t, err, io.EOF)
	})

	t.Run("Read_ZeroBytes", func(t *testing.T) {
		text := "abc"
		tr := newReader(text, 10)
		buf := make([]byte, 0)

		n, err := tr.Read(buf)
		assert.NoError(t, err)
		assert.Equal(t, 0, n)
	})

	t.Run("PositionTracking", func(t *testing.T) {
		text := "line 1\nline 2 is longer\nline 3\n"
		tr := newReader(text, 10)

		r, _, err := tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, 'l', r)
		pos := tr.Pos()
		assert.Equal(t, 1, pos.Offset())
		assert.Equal(t, 1, pos.Line())
		assert.Equal(t, 1, pos.Column())

		_, err = tr.Seek(6, io.SeekStart)
		require.NoError(t, err)
		pos = tr.Pos()
		assert.Equal(t, 6, pos.Offset())
		assert.Equal(t, 1, pos.Line())
		assert.Equal(t, 6, pos.Column())

		r, _, err = tr.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, '\n', r)
		pos = tr.Pos()
		assert.Equal(t, 7, pos.Offset())
		assert.Equal(t, 2, pos.Line())
		assert.Equal(t, 0, pos.Column())

		buf := make([]byte, 10)
		n, err := tr.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, 10, n)
		pos = tr.Pos()
		assert.Equal(t, 17, pos.Offset())
		assert.Equal(t, 2, pos.Line())
		assert.Equal(t, 10, pos.Column())

		rest, err := readAllRunes(tr)
		require.NoError(t, err)
		assert.Equal(t, "longer\nline 3\n", rest)

		pos = tr.Pos()
		assert.Equal(t, len(text), pos.Offset())
		assert.Equal(t, 4, pos.Line())
		assert.Equal(t, 0, pos.Column())
	})

	t.Run("Seek_Errors", func(t *testing.T) {
		tr := newReader("0123456789", 5)

		_, err := tr.Seek(0, 99)
		assert.ErrorContains(t, err, "invalid whence")

		_, err = tr.Seek(-1, io.SeekStart)
		assert.ErrorContains(t, err, "negative position")

		_, _, err = tr.ReadRune()
		require.NoError(t, err)

		_, err = tr.Seek(-2, io.SeekCurrent)
		assert.ErrorContains(t, err, "negative position")
	})

	t.Run("FillAtLeast_Errors", func(t *testing.T) {
		capacity := 10
		tr := newReader("some data", capacity)

		ok, err := tr.fillAtLeast(capacity + 1)
		assert.ErrorIs(t, err, ErrBufferTooSmall)
		assert.False(t, ok)

		ok, err = tr.fillAtLeast(-1)
		assert.Error(t, err)
		assert.False(t, ok)
	})
}

// TestSeekBackMultibyteUTF8 demonstrates that Seek correctly handles
// multi-byte UTF-8 characters by counting runes properly during rewind.
func TestSeekBackMultibyteUTF8(t *testing.T) {
	// "café" = 4 runes, 5 bytes (the 'é' is 2 bytes: 0xC3 0xA9)
	text := "café"
	tr := newReader(text, 64)

	// Read all bytes
	buf := make([]byte, 5)
	n, err := tr.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, text, string(buf))

	// Verify position after reading
	pos := tr.Pos()
	assert.Equal(t, 5, pos.Offset(), "Offset should be 5 bytes")
	assert.Equal(t, 4, pos.Column(), "Column should be 4 runes (c-a-f-é)")

	// Seek back 2 bytes (the full 'é' character)
	_, err = tr.Seek(-2, io.SeekCurrent)
	require.NoError(t, err)

	pos = tr.Pos()
	assert.Equal(t, 3, pos.Offset(), "Offset should be 3 bytes")
	assert.Equal(t, 3, pos.Column(), "Column should be 3 runes (c-a-f)")

	// Read the 'é' character back
	r, size, err := tr.ReadRune()
	require.NoError(t, err)
	assert.Equal(t, 'é', r, "Should read 'é'")
	assert.Equal(t, 2, size, "é is 2 bytes")

	pos = tr.Pos()
	assert.Equal(t, 5, pos.Offset())
	assert.Equal(t, 4, pos.Column(), "Column should be 4 runes")
}

func TestPositionScanRewind(t *testing.T) {
	pos := position.New()

	text1 := "hello\nworld"
	pos.Scan([]byte(text1))
	assert.Equal(t, len(text1), pos.Offset())
	assert.Equal(t, 2, pos.Line())
	assert.Equal(t, 5, pos.Column())

	err := pos.Rewind(3, 3)
	assert.NoError(t, err)
	assert.Equal(t, len(text1)-3, pos.Offset())
	assert.Equal(t, 2, pos.Line())
	assert.Equal(t, 2, pos.Column())

	err = pos.Rewind(6, 6)
	assert.NoError(t, err)
	assert.Equal(t, len(text1)-3-6, pos.Offset())
	assert.Equal(t, 1, pos.Line())
	assert.Equal(t, 2, pos.Column())

	err = pos.Rewind(10, 10)
	assert.Error(t, err, "Rewinding past beginning should return error")
}
