// Package textreader provides a buffered reader that keeps track of the
// current line, column, and offset of the text being read.
package textreader

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sync"
	"unicode/utf8"

	"github.com/xiam/textreader/position"
)

const (
	defaultCapacity = 64 * 1024
)

var (
	ErrBufferTooSmall  = errors.New("buffer too small")
	ErrInvalidUTF8     = errors.New("invalid UTF-8 encoding")
	ErrSeekOutOfBuffer = errors.New("seek out of buffer")
)

// TextReader reads from an io.Reader, buffering data and keeping track of the
// current position (line, column, and offset) in the text stream. It supports
// seeking only within the currently buffered data, which is good enough for
// giving context of the text around the current read position.
type TextReader struct {
	br io.Reader
	mu sync.Mutex

	pos *position.Position

	lastRuneSize int
	lastByte     int

	capacity int
	buf      []byte

	r int
	w int
}

// New returns a new TextReader that reads from r with the default buffer
// capacity.
func New(r io.Reader) *TextReader {
	return NewWithCapacity(r, defaultCapacity)
}

// NewWithCapacity returns a new TextReader with a buffer of at least the
// specified capacity.
func NewWithCapacity(r io.Reader, capacity int) *TextReader {
	if capacity < utf8.UTFMax {
		capacity = utf8.UTFMax
	}

	return &TextReader{
		br:           r,
		buf:          make([]byte, capacity),
		pos:          position.New(),
		capacity:     capacity,
		lastRuneSize: -1,
		lastByte:     -1,
	}
}

func (t *TextReader) fillAtLeast(n int) (bool, error) {
	if n < 0 {
		return false, fmt.Errorf("invalid size: %d", n)
	}

	if n == 0 {
		// Nothing to do.
		return true, nil
	}

	if n > t.capacity {
		// The requested read is larger than the buffer this is not allowed.
		return false, ErrBufferTooSmall
	}

	// If we already have enough data in the buffer, just return.
	if n <= t.w-t.r {
		return true, nil
	}

	// The next read will be beyond the buffer, so we need to shrink the buffer
	// to the current read position to free up space.
	if t.r+n >= t.capacity {

		copy(t.buf[0:], t.buf[t.r:t.w])

		t.w = t.w - t.r
		t.r = 0
	}

	var readErr error

	for t.w-t.r < n && readErr == nil {
		var bytesRead int

		bytesRead, readErr = t.br.Read(t.buf[t.w:t.capacity])
		t.w += bytesRead

		if bytesRead == 0 && readErr == nil {
			readErr = io.ErrNoProgress
		}
	}

	if readErr == nil {
		return true, nil
	}

	return t.w-t.r >= n, readErr
}

// ReadRune reads a single UTF-8 encoded Unicode character and returns the rune
// and its size in bytes.
func (t *TextReader) ReadRune() (r rune, size int, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Try to fill the buffer with at least enough bytes for a maximal rune.
	// We can tolerate an io.EOF here, as we might have a partial buffer to read from.
	_, err = t.fillAtLeast(utf8.UTFMax)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, 0, err
	}

	// If the buffer is empty after trying to fill, we are at the end of the stream.
	if t.r >= t.w {
		return 0, 0, io.EOF
	}

	// Let utf8.DecodeRune handle all cases: valid ASCII, valid multi-byte,
	// and invalid UTF-8 sequences.
	// If the sequence is invalid, it returns (utf8.RuneError, 1).
	r, size = utf8.DecodeRune(t.buf[t.r:t.w])

	// Advance the reader's position. This is crucial.
	// For an invalid byte, size will be 1, allowing us to skip it and continue.
	t.pos.Scan(t.buf[t.r : t.r+size])
	t.r += size

	// Update state to allow for UnreadRune.
	t.lastRuneSize = size
	t.lastByte = -1 // Invalidate byte-level unread

	// The error is nil because we successfully "read" a rune from the stream,
	// even if that rune is the replacement/error character. The caller is
	// responsible for checking if r == utf8.RuneError.
	return r, size, nil
}

// UnreadRune unreads the last rune read by ReadRune. It is an error to call
// UnreadRune if the most recent method called on the TextReader was not
// ReadRune.  Only one level of unread is supported.
func (t *TextReader) UnreadRune() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.lastRuneSize < 0 || t.r < t.lastRuneSize {
		return bufio.ErrInvalidUnreadRune
	}

	if err := t.pos.Rewind(t.lastRuneSize, 1); err != nil {
		return fmt.Errorf("rewind: %w", err)
	}

	t.r -= t.lastRuneSize

	t.lastRuneSize = -1
	t.lastByte = -1

	return nil
}

// Read reads up to len(p) bytes into p and returns the number of bytes read.
// For reads larger than the buffer capacity, it will read directly from the
// underlying reader, and discard any previously buffered data.
func (t *TextReader) Read(p []byte) (n int, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	needed, filled := len(p), 0

	var readErr error

	for filled < needed {
		buffered := t.w - t.r

		if buffered > 0 {
			// We have data some in the buffer. move as much data as possible from it
			// to `p`

			n = needed - filled
			if n > buffered {
				n = buffered
			}

			copy(p[filled:], t.buf[t.r:t.r+n])
			t.pos.Scan(p[filled : filled+n])
			t.r += n

			filled += n
		}

		if readErr != nil {
			break
		}

		// The size of the requested read is larger than the buffer, there's no way
		// we can handle this
		if needed-filled > t.capacity {

			// Read remaining data directly into p
			n, readErr = t.br.Read(p[filled:])

			t.pos.Scan(p[filled : filled+n])

			// Reset the buffer since we dumped it all into
			t.r = 0
			t.w = 0

			t.lastRuneSize = -1
			t.lastByte = -1

			filled += n

			break
		}

		// Fill the buffer with more data for the next read
		_, readErr = t.fillAtLeast(needed - filled)
	}

	t.lastRuneSize = -1
	t.lastByte = -1

	if t.r > 0 {
		t.lastByte = int(t.buf[t.r-1])
	}

	if filled == 0 && readErr != nil {
		return 0, readErr
	}

	return filled, nil
}

// ReadByte reads and returns a single byte.
func (t *TextReader) ReadByte() (byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Try to fill the buffer with at least 1 byte.
	// We tolerate io.EOF here, as we might have buffered data to read.
	_, err := t.fillAtLeast(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}

	if t.r >= t.w {
		return 0, io.EOF
	}

	// Read byte from buffer
	b := t.buf[t.r]
	t.pos.Scan(t.buf[t.r : t.r+1])
	t.r++

	t.lastRuneSize = -1
	t.lastByte = int(b)

	return b, nil
}

// UnreadByte unreads the last byte read by ReadByte. It is an error to call
// UnreadByte if the most recent method called on the TextReader was not
// ReadByte.  Only one level of unread is supported.
func (t *TextReader) UnreadByte() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.lastByte < 0 || t.r == 0 {
		return bufio.ErrInvalidUnreadByte
	}

	if err := t.pos.Rewind(1, 1); err != nil {
		return fmt.Errorf("rewind: %w", err)
	}

	t.r--

	t.lastByte = -1
	t.lastRuneSize = -1

	return nil
}

// Seek sets the offset for the next Read or ReadRune, interpreting offset and
// whence according to the io.Seeker interface. This Seek implementation
// operates only on the data currently held in the reader's buffer. It cannot
// seek backwards to data that has already been read and discarded from the
// buffer. An attempt to seek to a position before the start of the current
// buffer will result in an ErrSeekOutOfBuffer.  It does not perform a seek on
// the underlying io.Reader.
func (t *TextReader) Seek(offset int64, whence int) (int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	var newR int64 // new read pointer relative to start of t.buf

	switch whence {
	case io.SeekStart:
		if offset < 0 {
			return 0, errors.New("textreader: negative position")
		}

		// Calculate the absolute stream offset that corresponds to the start of our buffer.
		bufferStartOffset := int64(t.pos.Offset() - t.r)

		// If the target absolute offset is before the start of our buffered data, we cannot seek there.
		if offset < bufferStartOffset {
			return 0, ErrSeekOutOfBuffer
		}

		// Calculate new read pointer relative to the buffer.
		newR = offset - bufferStartOffset

	case io.SeekCurrent:
		newR = int64(t.r) + offset
	case io.SeekEnd:
		newR = int64(t.w) + offset
	default:
		return 0, errors.New("textreader: invalid whence")
	}

	if newR < 0 {
		return 0, errors.New("textreader: negative position")
	}

	rel := newR - int64(t.r)
	if rel == 0 {
		return int64(t.pos.Offset()), nil
	}

	// Check bounds before converting to int for buffer operations
	if newR > int64(t.capacity) {
		return 0, ErrSeekOutOfBuffer
	}
	newRInt := int(newR)
	relInt := int(rel)

	if rel > 0 { // Seeking Forward
		if relInt > t.capacity {
			return 0, ErrSeekOutOfBuffer
		}

		bytesAvailable := t.w - t.r
		if relInt > bytesAvailable {
			if _, err := t.fillAtLeast(relInt); err != nil && !errors.Is(err, io.EOF) {
				return 0, fmt.Errorf("fillAtLeast: %w", err)
			}
		}

		if t.r+relInt > t.w {
			return 0, ErrSeekOutOfBuffer
		}

		t.pos.Scan(t.buf[t.r : t.r+relInt])
		t.r += relInt

	} else { // Seeking Backward
		if newRInt < 0 {
			return 0, ErrSeekOutOfBuffer
		}
		// Count runes in the slice we're rewinding over
		runeCount := utf8.RuneCount(t.buf[newRInt:t.r])
		if err := t.pos.Rewind(-relInt, runeCount); err != nil {
			return 0, fmt.Errorf("pos.Rewind: %w", err)
		}
		t.r += relInt
	}

	t.lastRuneSize = -1
	t.lastByte = -1

	return int64(t.pos.Offset()), nil
}

// Pos returns a copy of the reader's current position (line, column, and
// offset).  Modifying the returned Position will not affect the reader's
// state.
func (t *TextReader) Pos() *position.Position {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.pos.Copy()
}
