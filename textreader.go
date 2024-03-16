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

// TextReader reads from an io.Reader and keeps track of the current position.
type TextReader struct {
	br *bufio.Reader

	column uint64
	line   uint64
	offset uint64
}

// NewReader returns a new TextReader that reads from r.
func NewReader(r io.Reader) *TextReader {
	return &TextReader{
		br:   bufio.NewReader(r),
		line: 1,
	}
}

// ReadRune reads a single UTF-8 encoded Unicode character and returns the rune
// and its size in bytes.
func (t *TextReader) ReadRune() (r rune, size int, err error) {
	r, size, err = t.br.ReadRune()
	t.offset += uint64(size)

	if err != nil {
		return
	}

	if r == newLine {
		t.line++
		t.column = 0
	} else {
		t.column++
	}

	return r, size, nil
}

// Read reads up to len(p) bytes into p and returns the number of bytes read.
func (t *TextReader) Read(p []byte) (n int, err error) {
	n, err = t.br.Read(p)
	t.offset += uint64(n)

	for _, b := range p {
		if b == newLine {
			t.line++
			t.column = 0
		} else {
			t.column++
		}
	}

	return n, err
}

// Position returns the current position of the reader.
func (t *TextReader) Position() Position {
	return Position{
		Column: t.column,
		Line:   t.line,
		Offset: t.offset,
	}
}
