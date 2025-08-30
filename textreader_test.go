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

// Compile-time checks to ensure TextReader implements expected interfaces.
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

		// Read through all runes and verify they match the expected sequence.
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
			// After reading all data, position should be at line 6 (after the final newline),
			// column 0 (beginning of the next line), with an offset equal to the total data length.
			pos := reader.Pos()
			assert.Equal(t, 6, pos.Line())
			assert.Equal(t, 0, pos.Column())
			assert.Equal(t, len(data), pos.Offset())
		}

		assert.True(t, hasUnicorn)

		// Attempting to read past EOF should return an EOF error.
		_, _, err := reader.ReadRune()
		require.Equal(t, io.EOF, err)

		// The first unread should succeed (single-level unread is allowed).
		err = reader.UnreadRune()
		require.NoError(t, err)

		// A second consecutive unread should fail (only one level of unread is supported).
		err = reader.UnreadRune()
		require.Error(t, err)

		// After a successful unread, we should be able to read the last character again (which is a newline).
		r, s, err := reader.ReadRune()
		require.NoError(t, err)
		assert.Equal(t, 1, s)
		assert.Equal(t, '\n', r)
	})

	t.Run("read beyond buffer capacity", func(t *testing.T) {
		// Create a reader with a very small buffer (4 bytes) to test buffer management
		// and direct-read scenarios when the requested data exceeds the buffer capacity.
		tr := textreader.NewWithCapacity(
			strings.NewReader(data),
			4,
		)
		{
			// Read 3 bytes - this should fit within the buffer.
			buf := make([]byte, 3)
			n, err := tr.Read(buf)
			require.NoError(t, err)
			assert.Equal(t, 3, n)
			assert.Equal(t, []byte(data[0:3]), buf)

			{
				// Position should be at line 1, column 3 (after "fir"), offset 3.
				pos := tr.Pos()
				assert.Equal(t, 1, pos.Line())
				assert.Equal(t, 3, pos.Column())
				assert.Equal(t, 3, pos.Offset())
			}
		}

		{
			// Read 4 bytes - this equals the buffer capacity and should work through buffering.
			buf := make([]byte, 4)
			n, err := tr.Read(buf)
			require.NoError(t, err)
			assert.Equal(t, 4, n)
			assert.Equal(t, []byte(data[3:3+4]), buf)
		}

		{
			// Read 5 bytes - this exceeds the buffer capacity and should trigger the direct read path.
			buf := make([]byte, 5)
			n, err := tr.Read(buf)
			require.NoError(t, err)
			assert.Equal(t, 5, n)
			assert.Equal(t, []byte(data[7:7+5]), buf)

			{
				// Position should be at line 2, column 1 (after "second "), offset 12.
				// This verifies that position tracking works correctly even with direct reads.
				pos := tr.Pos()
				assert.Equal(t, 2, pos.Line())
				assert.Equal(t, 1, pos.Column())
				assert.Equal(t, 12, pos.Offset())
			}
		}
		{
			// Read 15 bytes - much larger than the buffer, should use the direct read path.
			offset := tr.Pos().Offset()

			buf := make([]byte, 15)
			n, err := tr.Read(buf)
			require.NoError(t, err)
			assert.Equal(t, 15, n)
			assert.Equal(t, []byte(data[offset:offset+15]), buf)
		}
		{
			// Read the remaining data with an oversized buffer to ensure it handles the final read correctly.
			offset := tr.Pos().Offset()

			buf := make([]byte, 100)
			n, err := tr.Read(buf)

			require.NoError(t, err)
			assert.Equal(t, 35, n) // Only 35 bytes remaining in the data.
			assert.Equal(t, []byte(data[offset:]), buf[:n])

			{
				// Final position should be at line 6, column 0 (after the final newline), offset 62.
				pos := tr.Pos()
				assert.Equal(t, 6, pos.Line())
				assert.Equal(t, 0, pos.Column())
				assert.Equal(t, 62, pos.Offset())
			}
		}
	})

	t.Run("read and unread bytes", func(t *testing.T) {
		tr := textreader.New(strings.NewReader(data))

		// Read the first byte.
		b, err := tr.ReadByte()
		require.NoError(t, err)

		assert.Equal(t, byte('f'), b)

		{
			// Position should advance to line 1, column 1, offset 1.
			pos := tr.Pos()
			assert.Equal(t, 1, pos.Line())
			assert.Equal(t, 1, pos.Column())
			assert.Equal(t, 1, pos.Offset())
		}

		// Unread the byte - this should succeed.
		err = tr.UnreadByte()
		require.NoError(t, err)

		{
			// Position should revert to line 1, column 0, offset 0.
			pos := tr.Pos()
			assert.Equal(t, 1, pos.Line())
			assert.Equal(t, 0, pos.Column())
			assert.Equal(t, 0, pos.Offset())
		}

		// A second consecutive unread should fail due to the single-level unread limitation.
		err = tr.UnreadByte()
		require.Error(t, err)

		{
			// Position should remain unchanged after the failed unread.
			pos := tr.Pos()
			assert.Equal(t, 1, pos.Line())
			assert.Equal(t, 0, pos.Column())
			assert.Equal(t, 0, pos.Offset())
		}

		// Read the byte again after the successful unread.
		b, err = tr.ReadByte()
		require.NoError(t, err)
		assert.Equal(t, byte('f'), b)

		{
			// Position should advance again to line 1, column 1, offset 1.
			pos := tr.Pos()
			assert.Equal(t, 1, pos.Line())
			assert.Equal(t, 1, pos.Column())
			assert.Equal(t, 1, pos.Offset())
		}
	})

	t.Run("seeker", func(t *testing.T) {
		tr := textreader.New(strings.NewReader(data))

		// Read some initial data to establish a position.
		buf := make([]byte, 15)
		n, err := tr.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, 15, n)

		t.Run("seek to start + 17", func(t *testing.T) {
			// Seek to an absolute position 17 from the start.
			offset, err := tr.Seek(17, io.SeekStart)
			require.NoError(t, err)
			assert.Equal(t, int64(17), offset)

			// Read the byte at position 17 - it should be a space character.
			b, err := tr.ReadByte()
			require.NoError(t, err)
			assert.Equal(t, byte(' '), b)

			{
				// Position should now be at line 2, column 7 (after "second "), offset 18.
				pos := tr.Pos()
				assert.Equal(t, 2, pos.Line())
				assert.Equal(t, 7, pos.Column())
				assert.Equal(t, 18, pos.Offset())
			}

			// Unread should work and revert the position.
			err = tr.UnreadByte()
			require.NoError(t, err)

			{
				// Position should revert to line 2, column 6, offset 17.
				pos := tr.Pos()
				assert.Equal(t, 2, pos.Line())
				assert.Equal(t, 6, pos.Column())
				assert.Equal(t, 17, pos.Offset())
			}
		})

		t.Run("seek to end", func(t *testing.T) {
			// Seek to the end of the data (offset 0 from the end).
			offset, err := tr.Seek(0, io.SeekEnd)
			require.NoError(t, err)
			t.Logf("offset: %d", offset)

			{
				// The position's offset should match the seek result.
				pos := tr.Pos()
				assert.Equal(t, int(offset), pos.Offset())
			}

			{
				// Reading at the end should return an EOF error.
				_, err := tr.ReadByte()
				require.Error(t, err)
			}
		})

		t.Run("seek to end + 1", func(t *testing.T) {
			// Attempt to seek beyond the end of the data - this should fail.
			offset, err := tr.Seek(1, io.SeekEnd)
			require.Error(t, err)
			assert.Equal(t, int64(0), offset) // On error, offset should be 0.

			{
				// Reading should still fail since the seek was unsuccessful.
				_, err := tr.ReadByte()
				require.Error(t, err)
			}
		})

		t.Run("seek to end - 1", func(t *testing.T) {
			// Seek to one byte before the end.
			offset, err := tr.Seek(-1, io.SeekEnd)
			require.NoError(t, err)
			assert.Equal(t, len(data)-1, int(offset))

			{
				// Position should be at line 5, column 15 (the last char before the final newline), offset len(data)-1.
				pos := tr.Pos()
				assert.Equal(t, 5, pos.Line())
				assert.Equal(t, 15, pos.Column())
				assert.Equal(t, len(data)-1, pos.Offset())
			}

			// Read the last character - it should be a newline.
			b, err := tr.ReadByte()
			require.NoError(t, err)
			assert.Equal(t, byte('\n'), b)

			{
				// After reading the newline, the position advances to line 6, column 0, offset len(data).
				pos := tr.Pos()
				assert.Equal(t, 6, pos.Line())
				assert.Equal(t, 0, pos.Column())
				assert.Equal(t, len(data), pos.Offset())
			}
		})

		t.Run("seek to start", func(t *testing.T) {
			// Seek back to the beginning of the stream.
			offset, err := tr.Seek(0, io.SeekStart)
			require.NoError(t, err)
			assert.Equal(t, int64(0), offset)

			{
				// Position should be reset to the initial state.
				pos := tr.Pos()
				assert.Equal(t, 1, pos.Line())
				assert.Equal(t, 0, pos.Column())
				assert.Equal(t, 0, pos.Offset())
			}

			// Reading should now yield the first byte of the data.
			b, err := tr.ReadByte()
			require.NoError(t, err)
			assert.Equal(t, byte('f'), b)

			{
				// Position should advance as expected from the start.
				pos := tr.Pos()
				assert.Equal(t, 1, pos.Line())
				assert.Equal(t, 1, pos.Column())
				assert.Equal(t, 1, pos.Offset())
			}
		})

		t.Run("seek to start - 1", func(t *testing.T) {
			// Attempt to seek to a negative absolute position, which is invalid.
			offset, err := tr.Seek(-1, io.SeekStart)
			require.Error(t, err)
			assert.Equal(t, int64(0), offset)

			{
				// The position should not change after a failed seek.
				pos := tr.Pos()
				assert.Equal(t, 1, pos.Line())
				assert.Equal(t, 1, pos.Column())
				assert.Equal(t, 1, pos.Offset())
			}
		})

		t.Run("seek beyond input", func(t *testing.T) {
			// Attempt to seek far beyond the available data.
			offset, err := tr.Seek(200, io.SeekEnd)
			require.Error(t, err)
			assert.Equal(t, int64(0), offset)

			{
				// The reader's position should remain unchanged after the failed seek operation.
				// It should still be where it was before the seek was attempted.
				pos := tr.Pos()
				assert.Equal(t, 1, pos.Line())
				assert.Equal(t, 1, pos.Column())
				assert.Equal(t, 1, pos.Offset())
			}

			// Reading should continue from the last valid position.
			b, err := tr.ReadByte()
			require.NoError(t, err)
			assert.Equal(t, byte('i'), b) // The second byte of "first".

			{
				// Position should advance normally after the read.
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

		r := textreader.NewWithCapacity(strings.NewReader(input), capacity)

		// Read 12 bytes, then 10 more to force buffer shift
		readBytes := make([]byte, 12)
		_, err := io.ReadFull(r, readBytes)
		require.NoError(t, err, "Initial read failed")

		readBytes = make([]byte, 10)
		_, err = io.ReadFull(r, readBytes)
		require.NoError(t, err, "Second read failed")

		// Verify position is at 'w'
		nextByte, err := r.ReadByte()
		require.NoError(t, err, "ReadByte after setup failed")
		require.Equal(t, byte('w'), nextByte, "Expected to be at 'w' after setup")
		require.NoError(t, r.UnreadByte(), "UnreadByte failed")

		// This seek should succeed - we're seeking backwards to 'r' which is in the buffer
		newPos, err := r.Seek(-5, io.SeekCurrent)
		require.NoError(t, err, "Seek(-5, io.SeekCurrent) failed, but should be a valid seek")

		expectedPos := int64(17)
		require.Equal(t, expectedPos, newPos, "Seek returned incorrect new position")

		b, err := r.ReadByte()
		require.NoError(t, err, "ReadByte after seek failed")
		assert.Equal(t, byte('r'), b, "Read wrong byte after seek")
	})
}
