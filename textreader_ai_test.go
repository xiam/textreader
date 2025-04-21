package textreader

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/xiam/textreader/position"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// --- Test Suite Setup ---

type TextReaderAITestSuite struct {
	suite.Suite
	assert  *assert.Assertions
	require *require.Assertions
}

func (s *TextReaderAITestSuite) SetupTest() {
	s.assert = assert.New(s.T())
	s.require = require.New(s.T())
}

// --- Helper Functions ---

func (s *TextReaderAITestSuite) newReader(text string, capacity int) *TextReader {
	if capacity <= 0 {
		return New(strings.NewReader(text))
	}
	return NewWithCapacity(strings.NewReader(text), capacity)
}

func (s *TextReaderAITestSuite) readAllRunes(tr *TextReader) (string, error) {
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

func (s *TextReaderAITestSuite) readAllBytes(tr *TextReader) ([]byte, error) {
	var data []byte
	buf := make([]byte, 17) // Small buffer to force multiple reads
	for {
		n, err := tr.Read(buf)
		if n > 0 {
			data = append(data, buf[:n]...)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return data, nil
			}
			return data, err
		}
	}
}

// --- Test Cases ---

func (s *TextReaderAITestSuite) TestEmptyInput() {
	tr := s.newReader("", 10)
	r, size, err := tr.ReadRune()
	s.assert.ErrorIs(err, io.EOF)
	s.assert.Equal(rune(0), r)
	s.assert.Equal(0, size)

	b, err := tr.ReadByte()
	s.assert.ErrorIs(err, io.EOF)
	s.assert.Equal(byte(0), b)

	buf := make([]byte, 10)
	n, err := tr.Read(buf)
	s.T().Logf("Read %d bytes, err: %v", n, err)
	s.assert.ErrorIs(err, io.EOF)
	s.assert.Equal(0, n)

	_, err = tr.Seek(0, io.SeekCurrent)
	s.assert.NoError(err) // Seeking in empty buffer is okay if offset is 0

	_, err = tr.Seek(1, io.SeekStart)
	s.assert.ErrorIs(err, ErrSeekOutOfBuffer) // Cannot seek beyond EOF

	s.assert.ErrorIs(tr.UnreadByte(), bufio.ErrInvalidUnreadByte)
	s.assert.ErrorIs(tr.UnreadRune(), bufio.ErrInvalidUnreadRune)
}

func (s *TextReaderAITestSuite) TestSmallCapacity_ReadRune() {
	input := "你好世界"
	// Capacity smaller than a multi-byte rune
	tr := s.newReader(input, utf8.UTFMax) // Capacity 4
	result := ""

	for i, expRune := range []rune(input) {
		s.T().Logf("Reading rune %d: '%c'", i, expRune)

		r, size, err := tr.ReadRune()
		s.require.NoError(err, "Failed at rune index %d", i)
		s.assert.Equal(expRune, r)
		s.assert.Equal(utf8.RuneLen(expRune), size)
		s.assert.Equal(expRune, r)

		result += string(r)

		s.T().Logf("After reading '%c': r=%d, w=%d, cap=%d, buf=%v", r, tr.r, tr.w, tr.capacity, tr.buf)
	}

	s.assert.Equal(input, result)

	_, _, err := tr.ReadRune()
	s.assert.ErrorIs(err, io.EOF)
}

func (s *TextReaderAITestSuite) TestSmallCapacity_ReadByte() {
	text := "Hello, 世界"        // Contains ASCII and multi-byte
	tr := s.newReader(text, 5) // Small capacity
	result := []byte{}
	for i := 0; i < len(text); i++ {
		b, err := tr.ReadByte()
		s.require.NoError(err, "Failed at byte index %d", i)
		s.assert.Equal(text[i], b)
		result = append(result, b)
		// s.T().Logf("After reading byte '%c': r=%d, w=%d, cap=%d, buf=%v", b, tr.r, tr.w, tr.capacity, tr.buf)
	}
	s.assert.Equal(text, string(result))
	_, err := tr.ReadByte()
	s.assert.ErrorIs(err, io.EOF)
}

func (s *TextReaderAITestSuite) TestReadRune_InvalidUTF8() {
	// Valid UTF-8 followed by invalid sequence
	invalidData := "abc" + string([]byte{0xe4, 0xbd, 0xa0}) + string([]byte{0xff, 0xfe}) + "def"
	tr := s.newReader(invalidData, 10)

	r, _, err := tr.ReadRune() // 'a'
	s.require.NoError(err)
	s.assert.Equal('a', r)
	r, _, err = tr.ReadRune() // 'b'
	s.require.NoError(err)
	s.assert.Equal('b', r)
	r, _, err = tr.ReadRune() // 'c'
	s.require.NoError(err)
	s.assert.Equal('c', r)
	r, _, err = tr.ReadRune() // '你'
	s.require.NoError(err)
	s.assert.Equal('你', r)

	// Next read should hit the invalid sequence
	r, size, err := tr.ReadRune()
	s.assert.ErrorIs(err, ErrInvalidUTF8)
	s.assert.Equal(rune(0), r)
	s.assert.Equal(0, size)

	// Reading after error might be problematic depending on desired behavior.
	// Let's assume it stops. If it should continue byte-wise, the test needs adjustment.
}

func (s *TextReaderAITestSuite) TestReadRune_Boundary() {
	// Rune spans across buffer fill boundary
	text := strings.Repeat("a", 9) + "世" + strings.Repeat("b", 10) // "世" is 3 bytes
	tr := s.newReader(text, 10)                                    // Capacity 10

	// Read 'a's
	for i := 0; i < 9; i++ {
		r, _, err := tr.ReadRune()
		s.require.NoError(err)
		s.assert.Equal('a', r)
	}
	// s.T().Logf("Before reading '世': r=%d, w=%d, cap=%d, buf=%v", tr.r, tr.w, tr.capacity, tr.buf)

	// Now read '世'. This should trigger fillAtLeast
	r, size, err := tr.ReadRune()
	s.require.NoError(err)
	s.assert.Equal('世', r)
	s.assert.Equal(3, size) // '世' is 3 bytes

	// s.T().Logf("After reading '世': r=%d, w=%d, cap=%d, buf=%v", tr.r, tr.w, tr.capacity, tr.buf)

	// Read 'b's
	for i := 0; i < 10; i++ {
		r, _, err := tr.ReadRune()
		s.require.NoError(err)
		s.assert.Equal('b', r)
	}

	_, _, err = tr.ReadRune()
	s.assert.ErrorIs(err, io.EOF)
}

func (s *TextReaderAITestSuite) TestUnreadRune() {
	text := "a界b"
	tr := s.newReader(text, 10)

	r, size, err := tr.ReadRune() // 'a'
	s.require.NoError(err)
	s.assert.Equal('a', r)
	s.assert.Equal(1, size)
	s.assert.EqualValues(1, tr.Pos().Offset())

	r, size, err = tr.ReadRune() // '界'
	s.require.NoError(err)
	s.assert.Equal('界', r)
	s.assert.Equal(3, size)
	s.assert.EqualValues(1+3, tr.Pos().Offset())

	// Unread '界'
	err = tr.UnreadRune()
	s.require.NoError(err)
	s.assert.EqualValues(1, tr.Pos().Offset())
	s.assert.Equal(-1, tr.lastRuneSize) // Reset after unread

	// Read '界' again
	r, size, err = tr.ReadRune()
	s.require.NoError(err)
	s.assert.Equal('界', r)
	s.assert.Equal(3, size)
	s.assert.EqualValues(1+3, tr.Pos().Offset())

	// Unread '界'
	err = tr.UnreadRune()
	s.require.NoError(err)
	s.assert.EqualValues(1, tr.Pos().Offset())

	// Try unreading again (should fail)
	err = tr.UnreadRune()
	s.assert.ErrorIs(err, bufio.ErrInvalidUnreadRune)
	s.assert.EqualValues(1, tr.Pos().Offset()) // Position shouldn't change on error

	// Read '界' again
	r, size, err = tr.ReadRune()
	s.require.NoError(err)
	s.assert.Equal('界', r)
	s.assert.Equal(3, size)
	s.assert.EqualValues(1+3, tr.Pos().Offset())

	r, size, err = tr.ReadRune() // 'b'
	s.require.NoError(err)
	s.assert.Equal('b', r)
	s.assert.Equal(1, size)
	s.assert.EqualValues(1+3+1, tr.Pos().Offset())

	// Unread 'b'
	err = tr.UnreadRune()
	s.require.NoError(err)
	s.assert.EqualValues(1+3, tr.Pos().Offset())

	// Read 'b' again
	r, size, err = tr.ReadRune()
	s.require.NoError(err)
	s.assert.Equal('b', r)
	s.assert.Equal(1, size)
	s.assert.EqualValues(1+3+1, tr.Pos().Offset())

	// Read past EOF
	_, _, err = tr.ReadRune()
	s.assert.ErrorIs(err, io.EOF)

	// Unread last rune ('b')
	err = tr.UnreadRune()
	s.require.NoError(err)
	s.assert.EqualValues(1+3, tr.Pos().Offset())

	// Read 'b' again
	r, size, err = tr.ReadRune()
	s.require.NoError(err)
	s.assert.Equal('b', r)
	s.assert.Equal(1, size)
	s.assert.EqualValues(1+3+1, tr.Pos().Offset())
}

func (s *TextReaderAITestSuite) TestUnreadByte() {
	text := "abc"
	tr := s.newReader(text, 10)

	b, err := tr.ReadByte() // 'a'
	s.require.NoError(err)
	s.assert.Equal(byte('a'), b)
	s.assert.EqualValues(1, tr.Pos().Offset())

	b, err = tr.ReadByte() // 'b'
	s.require.NoError(err)
	s.assert.Equal(byte('b'), b)
	s.assert.EqualValues(2, tr.Pos().Offset())

	// Unread 'b'
	err = tr.UnreadByte()
	s.require.NoError(err)
	s.assert.EqualValues(1, tr.Pos().Offset())
	s.assert.Equal(-1, tr.lastByte) // Reset after unread

	// Read 'b' again
	b, err = tr.ReadByte()
	s.require.NoError(err)
	s.assert.Equal(byte('b'), b)
	s.assert.EqualValues(2, tr.Pos().Offset())

	// Unread 'b'
	err = tr.UnreadByte()
	s.require.NoError(err)
	s.assert.EqualValues(1, tr.Pos().Offset())

	// Try unreading again (should fail)
	err = tr.UnreadByte()
	s.assert.ErrorIs(err, bufio.ErrInvalidUnreadByte)
	s.assert.EqualValues(1, tr.Pos().Offset()) // Position shouldn't change

	// Read 'b' again
	b, err = tr.ReadByte()
	s.require.NoError(err)
	s.assert.Equal(byte('b'), b)
	s.assert.EqualValues(2, tr.Pos().Offset())

	b, err = tr.ReadByte() // 'c'
	s.require.NoError(err)
	s.assert.Equal(byte('c'), b)
	s.assert.EqualValues(3, tr.Pos().Offset())

	// Read past EOF
	_, err = tr.ReadByte()
	s.assert.ErrorIs(err, io.EOF)

	// Unread last byte ('c')
	err = tr.UnreadByte()
	s.require.NoError(err)
	s.assert.EqualValues(2, tr.Pos().Offset())

	// Read 'c' again
	b, err = tr.ReadByte()
	s.require.NoError(err)
	s.assert.Equal(byte('c'), b)
	s.assert.EqualValues(3, tr.Pos().Offset())
}

func (s *TextReaderAITestSuite) TestMixedReadUnread() {
	text := "a你好b"
	tr := s.newReader(text, 10)

	// Read 'a' (byte)
	b, err := tr.ReadByte()
	s.require.NoError(err)
	s.assert.Equal(byte('a'), b)
	s.assert.EqualValues(1, tr.Pos().Offset())

	// Read '你' (rune)
	r, size, err := tr.ReadRune()
	s.require.NoError(err)
	s.assert.Equal('你', r)
	s.assert.Equal(3, size)
	s.assert.EqualValues(1+3, tr.Pos().Offset())

	// Unread '你' (rune)
	err = tr.UnreadRune()
	s.require.NoError(err)
	s.assert.EqualValues(1, tr.Pos().Offset())

	// Try UnreadByte (should fail, last op was UnreadRune)
	err = tr.UnreadByte()
	s.assert.ErrorIs(err, bufio.ErrInvalidUnreadByte)

	// Read '你' as bytes
	b, err = tr.ReadByte() // first byte of 你
	s.require.NoError(err)
	s.assert.Equal(byte(0xe4), b)
	s.assert.EqualValues(2, tr.Pos().Offset())

	b, err = tr.ReadByte() // second byte of 你
	s.require.NoError(err)
	s.assert.Equal(byte(0xbd), b)
	s.assert.EqualValues(3, tr.Pos().Offset())

	// Unread second byte
	err = tr.UnreadByte()
	s.require.NoError(err)
	s.assert.EqualValues(2, tr.Pos().Offset())

	// Read second byte again
	b, err = tr.ReadByte()
	s.require.NoError(err)
	s.assert.Equal(byte(0xbd), b)
	s.assert.EqualValues(3, tr.Pos().Offset())

	// Read third byte of 你
	b, err = tr.ReadByte()
	s.require.NoError(err)
	s.assert.Equal(byte(0xa0), b)
	s.assert.EqualValues(4, tr.Pos().Offset())

	// Read '好' (rune)
	r, size, err = tr.ReadRune()
	s.require.NoError(err)
	s.assert.Equal('好', r)
	s.assert.Equal(3, size)
	s.assert.EqualValues(4+3, tr.Pos().Offset())

	// Read 'b' (byte)
	b, err = tr.ReadByte()
	s.require.NoError(err)
	s.assert.Equal(byte('b'), b)
	s.assert.EqualValues(4+3+1, tr.Pos().Offset())

	// Unread 'b' (byte)
	err = tr.UnreadByte()
	s.require.NoError(err)
	s.assert.EqualValues(4+3, tr.Pos().Offset())

	// Try UnreadRune (should fail)
	err = tr.UnreadRune()
	s.assert.ErrorIs(err, bufio.ErrInvalidUnreadRune)
}

func (s *TextReaderAITestSuite) TestRead_LargerThanCapacity() {
	text := strings.Repeat("x", 100)
	capacity := 10

	tr := s.newReader(text, capacity)
	buf := make([]byte, 20) // Read size > capacity

	n, err := tr.Read(buf)
	s.require.NoError(err)
	s.assert.Equal(20, n)
	s.assert.Equal(strings.Repeat("x", 20), string(buf))
	s.assert.EqualValues(20, tr.Pos().Offset())

	// Check internal buffer state, should have been written directly to buf
	s.assert.Equal(0, tr.w)
	s.assert.Equal(0, tr.r)
	s.assert.Equal(-1, tr.lastRuneSize)
	s.assert.Equal(-1, tr.lastByte)

	// Read again, smaller amount
	s.T().Logf("READ SMALLER AMOUNT")

	n, err = tr.Read(buf[:5])
	s.require.NoError(err)
	s.assert.Equal(5, n)
	s.assert.Equal("xxxxx", string(buf[:5]))
	s.assert.EqualValues(20+5, tr.Pos().Offset())

	s.T().Logf("READ REMAINING")

	// Read remaining
	remaining, err := s.readAllBytes(tr)
	s.require.NoError(err)
	s.assert.Equal(100-25, len(remaining))
	s.assert.Equal(strings.Repeat("x", 100-25), string(remaining))
	s.assert.EqualValues(100, tr.Pos().Offset())

	_, err = tr.Read(buf)
	s.assert.ErrorIs(err, io.EOF)
}

func (s *TextReaderAITestSuite) TestRead_SmallerThanCapacity() {
	text := strings.Repeat("y", 50)
	capacity := 20
	tr := s.newReader(text, capacity)
	buf := make([]byte, 15) // Read size < capacity

	n, err := tr.Read(buf)
	s.require.NoError(err)
	s.assert.Equal(15, n)
	s.assert.Equal(strings.Repeat("y", 15), string(buf))
	s.assert.EqualValues(15, tr.Pos().Offset())

	// Check internal state
	// Should have read 'capacity' bytes initially
	s.assert.Equal(capacity, tr.w)
	s.assert.Equal(15, tr.r) // Read pointer advanced
	s.assert.Equal(int(byte('y')), tr.lastByte)
	s.assert.Equal(-1, tr.lastRuneSize)

	// Read again
	n, err = tr.Read(buf[:10])
	s.require.NoError(err)
	s.assert.Equal(10, n) // Reads 5 from buffer, then fills and reads 5 more
	s.assert.Equal(strings.Repeat("y", 10), string(buf[:10]))
	s.assert.EqualValues(15+10, tr.Pos().Offset())

	// Read remaining
	remaining, err := s.readAllBytes(tr)
	s.require.NoError(err)
	s.assert.Equal(50-25, len(remaining))
	s.assert.Equal(strings.Repeat("y", 50-25), string(remaining))
	s.assert.EqualValues(50, tr.Pos().Offset())

	_, err = tr.Read(buf)
	s.assert.ErrorIs(err, io.EOF)
}

func (s *TextReaderAITestSuite) TestRead_ZeroBytes() {
	// Test the potential panic identified in the review
	text := "abc"
	tr := s.newReader(text, 10)
	buf := make([]byte, 5)

	// Read all data
	n, err := tr.Read(buf)
	s.require.NoError(err)
	s.assert.Equal(3, n)

	// Try reading again (should return 0, io.EOF)
	n, err = tr.Read(buf)
	s.assert.ErrorIs(err, io.EOF)
	s.assert.Equal(0, n)
	// Crucially, accessing p[n-1] should not have happened
	s.assert.NotPanics(func() {
		_, _ = tr.Read(buf) // Call Read again after EOF
	})
}

func (s *TextReaderAITestSuite) TestSeek_Basic() {
	text := "0123456789abcdef"
	capacity := 10
	tr := s.newReader(text, capacity)

	// SeekStart within initial buffer (implicitly reads capacity)
	newPos, err := tr.Seek(5, io.SeekStart)
	s.require.NoError(err)
	s.assert.EqualValues(5, newPos)
	s.assert.EqualValues(5, tr.Pos().Offset())
	s.assert.Equal(5, tr.r) // Internal read pointer

	s.T().Logf("ReadRune after SeekStart")
	r, _, _ := tr.ReadRune()
	s.assert.Equal('5', r)
	s.assert.EqualValues(6, tr.Pos().Offset())

	s.T().Logf("SeekStart +2")
	// SeekCurrent forward within buffer
	newPos, err = tr.Seek(2, io.SeekCurrent)
	s.require.NoError(err)
	s.assert.EqualValues(8, newPos) // 5 + 2
	s.assert.EqualValues(8, tr.Pos().Offset())

	newPos, err = tr.Seek(2, io.SeekStart)
	s.require.NoError(err)
	s.assert.EqualValues(2, newPos)
	s.assert.EqualValues(2, tr.Pos().Offset())

	r, _, _ = tr.ReadRune()
	s.assert.Equal('2', r)
	s.assert.EqualValues(3, tr.Pos().Offset())

	// SeekCurrent backward within buffer
	s.T().Logf("SeekCurrent backward within buffer")
	newPos, err = tr.Seek(-2, io.SeekCurrent)
	s.require.NoError(err)
	s.assert.EqualValues(1, newPos)
	s.assert.EqualValues(1, tr.Pos().Offset())

	r, _, _ = tr.ReadRune()
	s.assert.Equal('1', r)
	s.assert.EqualValues(2, tr.Pos().Offset())

	// SeekEnd relative to current buffer content (w)
	// At this point, r=6, w=10 (0123456789)
	newPos, err = tr.Seek(-2, io.SeekEnd)
	s.require.NoError(err)
	s.assert.EqualValues(8, newPos) // w(10) - 2
	s.assert.EqualValues(8, tr.Pos().Offset())
	s.assert.Equal(8, tr.r)

	r, _, _ = tr.ReadRune()
	s.assert.Equal('8', r)
	s.assert.EqualValues(9, tr.Pos().Offset())

	// SeekStart to beginning
	newPos, err = tr.Seek(0, io.SeekStart)
	s.require.Error(err, "we don't have enough data to seek to 0, buffer has been already shrunk")
	s.assert.EqualValues(0, newPos)

	r, _, _ = tr.ReadRune()
	s.assert.Equal('9', r)
	s.assert.EqualValues(10, tr.Pos().Offset())
}

func (s *TextReaderAITestSuite) TestSeek_BeyondBuffer() {
	text := "0123456789abcdefghijklmnopqrstuvwxyz" // len 36
	capacity := 10
	tr := s.newReader(text, capacity)

	// Seek beyond initial buffer read
	_, err := tr.Seek(15, io.SeekStart) // Needs to read more data
	s.require.Error(err, "cannot seek beyond buffer capacity")
}

func (s *TextReaderAITestSuite) TestSeek_Errors() {
	text := "0123456789"
	capacity := 5
	tr := s.newReader(text, capacity)

	// Invalid whence
	_, err := tr.Seek(0, 99)
	s.assert.Error(err)
	s.assert.Contains(err.Error(), "invalid whence")

	// Negative absolute position
	_, err = tr.Seek(-1, io.SeekStart)
	s.assert.Error(err)
	s.assert.Contains(err.Error(), "negative position")

	// Negative relative position resulting in negative absolute
	_, err = tr.Seek(-1, io.SeekCurrent) // Currently at 0
	s.assert.Error(err)
	s.assert.Contains(err.Error(), "negative position")

	// Seek beyond EOF
	_, err = tr.Seek(100, io.SeekStart)
	s.assert.ErrorIs(err, ErrSeekOutOfBuffer)

	// Seek beyond EOF relative to current
	_, err = tr.Seek(5, io.SeekStart) // Move to 5
	s.require.NoError(err)
	_, err = tr.Seek(10, io.SeekCurrent) // Try to move to 15 (beyond 10)
	s.assert.ErrorIs(err, ErrSeekOutOfBuffer)

	// Seek beyond EOF relative to end (w)
	// After seeking to 5, w might be 10 (if it read ahead)
	// Let's read a bit to ensure w is updated
	_, _, _ = tr.ReadRune()         // Read '5', pos=6
	_, err = tr.Seek(5, io.SeekEnd) // Seek relative to w (which is likely 10) -> 15
	s.assert.ErrorIs(err, ErrSeekOutOfBuffer)
}

func (s *TextReaderAITestSuite) TestPositionTracking() {
	text := "line 1\nline 2 is longer\nline 3\n"
	tr := s.newReader(text, 10) // Use capacity smaller than line 2

	// Line 1
	r, _, err := tr.ReadRune() // 'l'
	s.require.NoError(err)
	s.assert.Equal('l', r)
	s.assert.EqualValues(1, tr.Pos().Offset())
	s.assert.EqualValues(1, tr.Pos().Line())
	s.assert.EqualValues(1, tr.Pos().Column())

	_, err = tr.Seek(6, io.SeekStart) // Seek to '\n'
	s.require.NoError(err)
	s.assert.EqualValues(6, tr.Pos().Offset())
	s.assert.EqualValues(1, tr.Pos().Line())
	s.assert.EqualValues(6, tr.Pos().Column()) // Column before reading newline

	r, _, err = tr.ReadRune() // '\n'
	s.require.NoError(err)
	s.assert.Equal('\n', r)
	s.assert.EqualValues(7, tr.Pos().Offset())
	s.assert.EqualValues(2, tr.Pos().Line())   // Line increments after reading newline
	s.assert.EqualValues(0, tr.Pos().Column()) // Column resets

	// Line 2 - Read bytes
	buf := make([]byte, 10)
	n, err := tr.Read(buf) // "line 2 is "
	s.require.NoError(err)
	s.assert.Equal(10, n)
	s.assert.EqualValues(7+10, tr.Pos().Offset())
	s.assert.EqualValues(2, tr.Pos().Line())
	s.assert.EqualValues(10, tr.Pos().Column())

	// Read rest of line 2 + newline
	restLine2, err := s.readAllRunes(tr) // "longer\nline 3\n"
	s.require.NoError(err)
	s.assert.Equal("longer\nline 3\n", restLine2)

	// Check final position
	s.assert.EqualValues(len(text), tr.Pos().Offset())
	s.assert.EqualValues(4, tr.Pos().Line()) // After last \n
	s.assert.EqualValues(0, tr.Pos().Column())
}

func (s *TextReaderAITestSuite) TestFillAtLeast_BufferTooSmallError() {
	capacity := 10
	tr := s.newReader("some data", capacity)

	// Try to fill more than capacity directly (won't happen via ReadRune/Byte)
	// We need to call fillAtLeast directly or simulate a scenario
	// Seek can trigger it if seeking far ahead
	_, err := tr.Seek(int64(capacity+1), io.SeekStart)
	s.assert.ErrorIs(err, ErrSeekOutOfBuffer)

	// Test the explicit check in fillAtLeast
	ok, err := tr.fillAtLeast(capacity + 1)
	s.assert.ErrorIs(err, ErrBufferTooSmall)
	s.assert.False(ok)
}

// --- Test Suite Runner ---

func TestTextReaderAITestSuite(t *testing.T) {
	suite.Run(t, new(TextReaderAITestSuite))
}

// --- Example of a test for the position package itself (if needed) ---
// This is separate from TextReader tests but ensures the dependency works.

func TestPositionScanRewind(t *testing.T) {
	assert := assert.New(t)
	pos := position.New()

	text1 := "hello\nworld"
	pos.Scan([]byte(text1))
	assert.EqualValues(len(text1), pos.Offset())
	assert.EqualValues(2, pos.Line())
	assert.EqualValues(5, pos.Column()) // At 'd'

	err := pos.Rewind(3)
	assert.NoError(err)
	assert.EqualValues(len(text1)-3, pos.Offset())
	assert.EqualValues(2, pos.Line())
	assert.EqualValues(2, pos.Column()) // At 'o'

	err = pos.Rewind(6)
	assert.NoError(err)
	assert.EqualValues(len(text1)-3-6, pos.Offset())
	assert.EqualValues(1, pos.Line())   // Back to line 1
	assert.EqualValues(2, pos.Column()) // After 'l'

	err = pos.Rewind(10) // Rewind too much
	assert.Error(err)    // Expect an error for rewinding past the beginning
}
