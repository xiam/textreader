package textreader

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/xiam/textreader/position"
)

const defaultCapacity = 1024 * 1024 * 4 // TODO: MB enough? find a good default capacity

// TextReader reads from an io.Reader and keeps track of the current position.
type TextReader struct {
	br *bufio.Reader

	pos *position.Position

	lastRuneSize int
	lastByte     int

	capacity int
	buf      []byte
	r        int
	w        int
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
	if n > t.capacity {
		return errors.New("buffer too small")
	}

	// if not enough data in buffer, read more
	if t.r+n > t.w {
		slice := make([]byte, n)
		n, err := t.br.Read(slice)
		if err != nil && err != io.EOF {
			return err
		}

		// shrink buffer to push new data
		if t.w+n > t.capacity {
			t.buf = t.buf[t.r:t.w]
			t.w = t.w - t.r
			t.r = 0
		}

		t.buf = append(t.buf, slice[:n]...)
		t.w += n
	}

	return nil
}

// ReadRune reads a single UTF-8 encoded Unicode character and returns the rune
// and its size in bytes.
func (t *TextReader) ReadRune() (r rune, size int, err error) {
	// increase buffer size if necessary
	if err := t.fillAtLeast(utf8.UTFMax); err != nil {
		return 0, 0, err
	}
	if t.r >= t.w {
		return 0, 0, io.EOF
	}

	t.lastRuneSize = -1
	t.lastByte = -1

	// read rune from buffer
	r, size = rune(t.buf[t.r]), 1
	if r >= utf8.RuneSelf {
		r, size = utf8.DecodeRune(t.buf[t.r : t.r+utf8.UTFMax])
		if r == utf8.RuneError {
			return 0, 0, errors.New("invalid UTF-8 encoding")
		}
	}

	// update buffer position
	t.pos.Scan(t.buf[t.r : t.r+size])

	t.lastRuneSize = size
	t.r += t.lastRuneSize

	return r, size, nil
}

// UnreadRune unreads the last rune.
func (t *TextReader) UnreadRune() error {
	if t.lastRuneSize < 0 || t.w < t.lastRuneSize {
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

	if len(p) >= t.capacity {
		// read directly into p
		n, err = t.br.Read(p)

		t.pos.Scan(p[:n])

		// copy last bytes from p to buffer (up to capacity)
		w := t.capacity
		if w > n {
			w = n
		}

		copy(t.buf, p[n-w:n])
		t.r = 0
		t.w = w

		t.lastRuneSize = -1
		t.lastByte = int(p[n-1])

		return n, err
	}

	// use buffer
	if err := t.fillAtLeast(len(p)); err != nil {
		return 0, err
	}

	n = copy(p, t.buf[t.r:t.w])
	t.pos.Scan(t.buf[t.r:t.w])
	t.r += n

	t.lastRuneSize = -1
	t.lastByte = int(p[n-1])

	return n, nil
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

	// if abs is in buffer, just move r back
	if abs <= t.w {
		if abs > t.r {
			t.pos.Scan(t.buf[t.r:abs])
		} else {
			if err := t.pos.Rewind(t.r - abs); err != nil {
				return 0, fmt.Errorf("rewind: %v", err)
			}
		}

		t.r = abs
		t.lastRuneSize = -1
		t.lastByte = -1
		return int64(abs), nil
	}

	// if abs is beyond buffer, read more data
	if err := t.fillAtLeast(abs - t.w); err != nil {
		return 0, err
	}

	if abs <= t.w {
		if abs > t.r {
			t.pos.Scan(t.buf[t.r:abs])
		} else {
			if err := t.pos.Rewind(t.r - abs); err != nil {
				return 0, fmt.Errorf("rewind: %v", err)
			}
		}

		t.r = abs
		t.lastRuneSize = -1
		t.lastByte = -1

		return int64(abs), nil
	}

	return 0, errors.New("seek out of buffer")
}

func (t *TextReader) Pos() *position.Position {
	return t.pos
}
