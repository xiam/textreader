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

// TextReader reads from an io.Reader and keeps track of the current position.
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

// New returns a new TextReader that reads from r.
func New(r io.Reader) *TextReader {
	return NewWithCapacity(r, defaultCapacity)
}

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
		// nothing to do
		return true, nil
	}

	if n > t.capacity {
		// the requested read is larger than the buffer this is not allowed
		return false, ErrBufferTooSmall
	}

	// if we already have enough data in the buffer, just return
	if n <= t.w-t.r {
		return true, nil
	}

	// the next read will be beyond the buffer, so we need to shrink the buffer
	// to the current read position to free up space
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

	// increase buffer size if necessary
	_, err = t.fillAtLeast(utf8.UTFMax)
	if err != nil && !errors.Is(err, io.EOF) {
		// we can tolerate EOF here, since we may be at the end of the stream and
		// the next character may not be a valid UTF-8 character
		return 0, 0, err
	}

	if t.r >= t.w {
		// we're at the end of the buffer
		return 0, 0, io.EOF
	}

	// read next byte from buffer as a rune
	r, size = rune(t.buf[t.r]), 1
	if r >= utf8.RuneSelf {
		// decode UTF-8 rune
		readSize := t.w - t.r
		if readSize > utf8.UTFMax {
			readSize = utf8.UTFMax
		}

		r, size = utf8.DecodeRune(t.buf[t.r : t.r+readSize])
		if r == utf8.RuneError {
			return 0, 0, ErrInvalidUTF8
		}
	}

	// update buffer position
	t.pos.Scan(t.buf[t.r : t.r+size])
	t.r += size

	t.lastRuneSize = size
	t.lastByte = -1

	return r, size, nil
}

// UnreadRune unreads the last rune.
func (t *TextReader) UnreadRune() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.lastRuneSize < 0 || t.r < t.lastRuneSize {
		return bufio.ErrInvalidUnreadRune
	}

	if err := t.pos.Rewind(t.lastRuneSize); err != nil {
		return fmt.Errorf("rewind: %v", err)
	}

	t.r -= t.lastRuneSize

	t.lastRuneSize = -1
	t.lastByte = -1

	return nil
}

// Read reads up to len(p) bytes into p and returns the number of bytes read.
func (t *TextReader) Read(p []byte) (n int, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	needed, filled := len(p), 0

	var readErr error

	for filled < needed {
		buffered := t.w - t.r

		if buffered > 0 {
			// we have data some in the buffer. move as much data as possible from it
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

		// the size of the requested read is larger than the buffer, there's no way
		// we can handle this
		if needed-filled > t.capacity {

			// read remaining data directly into p
			n, readErr = t.br.Read(p[filled:])

			t.pos.Scan(p[filled : filled+n])

			// reset the buffer since we dumped it all into
			t.r = 0
			t.w = 0

			t.lastRuneSize = -1
			t.lastByte = -1

			filled += n

			break
		}

		// fill the buffer with more data for the next read
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

func (t *TextReader) ReadByte() (byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	ok, err := t.fillAtLeast(1)
	if !ok || err != nil {
		return 0, err
	}

	if t.r >= t.w {
		return 0, io.EOF
	}

	// read byte from buffer
	b := t.buf[t.r]
	t.pos.Scan(t.buf[t.r : t.r+1])
	t.r++

	t.lastRuneSize = -1
	t.lastByte = int(b)

	return b, nil
}

func (t *TextReader) UnreadByte() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.lastByte < 0 || t.r == 0 {
		return bufio.ErrInvalidUnreadByte
	}

	if err := t.pos.Rewind(1); err != nil {
		return fmt.Errorf("rewind: %v", err)
	}

	t.r--

	t.lastByte = -1
	t.lastRuneSize = -1

	return nil
}

func (t *TextReader) Seek(offset int64, whence int) (int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	var abs int

	switch whence {
	case io.SeekStart:
		abs = int(offset)
	case io.SeekCurrent:
		abs = t.r + int(offset)
	case io.SeekEnd:
		abs = t.w + int(offset)
	default:
		return 0, errors.New("invalid whence")
	}

	if abs < 0 {
		return 0, errors.New("negative position")
	}

	if abs > t.capacity {
		return 0, ErrSeekOutOfBuffer
	}

	// calculate the relative position
	rel := abs - t.pos.Offset()
	if rel == 0 {
		return int64(abs), nil
	}

	if rel > 0 {

		if t.r+rel >= t.w {
			// we need to read more data
			ok, err := t.fillAtLeast((t.r + rel) - t.w)
			if !ok || err != nil {
				if err == nil || errors.Is(err, io.EOF) {
					return 0, ErrSeekOutOfBuffer
				}
				return 0, fmt.Errorf("fillAtLeast: %w", err)
			}

			if t.r+rel > t.w {
				// We couldn't read enough data (likely hit EOF)
				return int64(t.pos.Offset() + (t.w - t.r)), ErrSeekOutOfBuffer
			}
		}

		t.pos.Scan(t.buf[t.r : t.r+rel])
		t.r += rel

		t.lastRuneSize = -1
		t.lastByte = -1

		return int64(abs), nil
	}

	if t.r+rel < 0 {
		return 0, ErrSeekOutOfBuffer
	}

	t.r += rel
	if err := t.pos.Rewind(-rel); err != nil {
		return int64(abs), fmt.Errorf("Rewind: %w", err)
	}

	t.lastRuneSize = -1
	t.lastByte = -1

	return int64(abs), nil
}

func (t *TextReader) Pos() *position.Position {
	t.mu.Lock()
	defer t.mu.Unlock()

	p := t.pos.Copy()

	return &p
}
