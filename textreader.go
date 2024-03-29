package textreader

import (
	"bufio"
	"fmt"
	"io"
)

const newLine = '\n'

// Position represents a position in a text file.
type Position struct {
	Column uint64 // Starting from 0
	Line   uint64 // Starting from 1
	Offset uint64 // Starting from 0
}

func (p Position) String() string {
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

func newPosition() Position {
	return Position{
		Line: 1,
	}
}

// TextReader reads from an io.Reader and keeps track of the current position.
type TextReader struct {
	br *bufio.Reader

	position     Position
	lastPosition *Position
}

// NewReader returns a new TextReader that reads from r.
func NewReader(r io.Reader) *TextReader {
	return &TextReader{
		br:       bufio.NewReader(r),
		position: newPosition(),
	}
}

// ReadRune reads a single UTF-8 encoded Unicode character and returns the rune
// and its size in bytes.
func (t *TextReader) ReadRune() (r rune, size int, err error) {
	r, size, err = t.br.ReadRune()

	t.lastPosition = &Position{
		Column: t.position.Column,
		Line:   t.position.Line,
		Offset: t.position.Offset,
	}

	t.position.Offset += uint64(size)

	if err != nil {
		return
	}

	if r == newLine {
		t.position.Line++
		t.position.Column = 0
	} else {
		t.position.Column++
	}

	return r, size, nil
}

// UnreadRune unreads the last rune.
func (t *TextReader) UnreadRune() error {
	if t.lastPosition == nil {
		return bufio.ErrInvalidUnreadRune
	}

	err := t.br.UnreadRune()
	if err != nil {
		return err
	}

	t.position = *t.lastPosition
	t.lastPosition = nil

	return nil
}

// Read reads up to len(p) bytes into p and returns the number of bytes read.
func (t *TextReader) Read(p []byte) (n int, err error) {
	n, err = t.br.Read(p)
	t.position.Offset += uint64(n)

	for _, b := range p {
		if b == newLine {
			t.position.Line++
			t.position.Column = 0
		} else {
			t.position.Column++
		}
	}

	return n, err
}

// Position returns the current position of the reader.
func (t *TextReader) Position() Position {
	return t.position
}
