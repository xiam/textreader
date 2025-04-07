package textreader

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/xiam/textreader/position"
)

const (
	defaultCapacity = 64 * 1024

	shortReadSize = 1024
)

var (
	ErrBufferTooSmall  = errors.New("buffer too small")
	ErrInvalidUTF8     = errors.New("invalid UTF-8 encoding")
	ErrSeekOutOfBuffer = errors.New("seek out of buffer")
)

// TextReader reads from an io.Reader and keeps track of the current position.
type TextReader struct {
	br *bufio.Reader

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
		br:           bufio.NewReader(r),
		buf:          make([]byte, 0, capacity),
		pos:          position.New(),
		capacity:     capacity,
		lastRuneSize: -1,
		lastByte:     -1,
	}
}

func (t *TextReader) fillAtLeast(n int) error {
	if n < 0 {
		return fmt.Errorf("invalid size: %d", n)
	}

	if n == 0 {
		// nothing to do
		return nil
	}

	if n > t.capacity {
		// the requested read is larger than the buffer this is not allowed
		return ErrBufferTooSmall
	}

	// if we already have enough data in the buffer, just return
	if n <= t.w-t.r {
		return nil
	}

	// the next read will be beyond the buffer, so we need to shrink the buffer
	// to the current read position to free up space
	if t.r+n >= t.capacity {
		t.buf = t.buf[t.r:t.w]
		t.w = t.w - t.r
		t.r = 0
	}

	needed := n - (t.w - t.r)

	for t.w < t.capacity && needed > 0 {
		shortRead := make([]byte, shortReadSize)
		bytesRead, err := t.br.Read(shortRead)

		t.buf = append(t.buf, shortRead[:bytesRead]...)
		t.w += bytesRead
		needed -= bytesRead

		if err != nil {
			if errors.Is(err, io.EOF) {
				return io.EOF
			}
			return err
		}
	}

	if needed > 0 {
		return io.ErrUnexpectedEOF
	}

	return nil
}

// ReadRune reads a single UTF-8 encoded Unicode character and returns the rune
// and its size in bytes.
func (t *TextReader) ReadRune() (r rune, size int, err error) {
	// increase buffer size if necessary
	if err := t.fillAtLeast(utf8.UTFMax); err != nil {
		// we can tolerate EOF here, since we may be at the end of the stream and
		// the next character may not be a valid UTF-8 character
		if !errors.Is(err, io.EOF) {
			return 0, 0, err
		}
	}

	if t.r >= t.w {
		// we're at the end of the buffer
		return 0, 0, io.EOF
	}

	// read next byte from buffer as a rune
	r, size = rune(t.buf[t.r]), 1
	if r >= utf8.RuneSelf {
		if t.r+utf8.UTFMax > t.w {
			// we don't have enough data in the buffer to decode a full UTF-8
			return 0, 0, io.EOF
		}

		// decode UTF-8 rune
		r, size = utf8.DecodeRune(t.buf[t.r : t.r+utf8.UTFMax])
		if r == utf8.RuneError {
			return 0, 0, ErrInvalidUTF8
		}
	}

	// update buffer position
	t.lastRuneSize = size
	t.pos.Scan(t.buf[t.r : t.r+t.lastRuneSize])

	t.r += t.lastRuneSize

	t.lastByte = -1

	return r, size, nil
}

// UnreadRune unreads the last rune.
func (t *TextReader) UnreadRune() error {
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
	needed, filled := len(p), 0

	for filled < needed {
		if t.r < t.w {
			// we have data in the buffer. move as much data as possible from it to
			// `p`
			buffered := t.w - t.r

			n = needed - filled
			if n > buffered {
				n = buffered
			}

			copy(p[filled:], t.buf[t.r:t.r+n])
			t.pos.Scan(t.buf[t.r : t.r+n])
			t.r += n

			filled += n
		}

		if filled >= needed {
			t.lastRuneSize = -1
			t.lastByte = -1

			if t.r > 0 {
				t.lastByte = int(t.buf[t.r-1])
			}

			return filled, nil
		}

		// the size of the requested read is larger than the buffer
		if needed > t.capacity {
			// read remaining data directly into p
			n, err = t.br.Read(p[filled:])
			if err != nil {
				if errors.Is(err, io.EOF) && filled == 0 {
					return 0, fmt.Errorf("read: %v", err)
				}
			}

			t.pos.Scan(p[filled : filled+n])
			n = n + filled

			// reset the buffer since we dumped it all into p
			t.r = 0
			t.w = 0

			t.lastRuneSize = -1
			t.lastByte = -1

			if n > 0 {
				t.lastByte = int(p[n-1])
			}

			return n, nil
		}

		// fill the buffer with more data for the next read
		if err := t.fillAtLeast(needed - filled); err != nil {
			if errors.Is(err, io.EOF) {
				return filled, nil
			}

			return filled, err
		}
	}

	return filled, nil
}

func (t *TextReader) ReadByte() (byte, error) {
	if err := t.fillAtLeast(1); err != nil {
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
		return 0, errors.New("position out of range")
	}

	// calculate the relative position
	rel := abs - t.pos.Offset()
	if rel == 0 {
		return int64(abs), nil
	}

	if rel > 0 {

		if t.r+rel >= t.w {
			// we need to read more data
			if err := t.fillAtLeast((t.r + rel) - t.w); err != nil {
				if errors.Is(err, io.EOF) {
					return 0, ErrSeekOutOfBuffer
				}
				return 0, fmt.Errorf("fillAtLeast: %w", err)
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

	return int64(abs), nil
}

func (t *TextReader) Pos() *position.Position {
	return t.pos
}
