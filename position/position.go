// Package position tracks a location in a stream of UTF-8 text as line, column,
// and byte offset. Column counts runes since the last newline, while offset
// counts bytes from the start of the stream.
package position

import (
	"fmt"
	"sync"
	"unicode/utf8"
)

const newLine = '\n'

// Position represents a position in a text file.
type Position struct {
	mu sync.Mutex

	runesPerLine []int // rune count per line (for Column)
	bytesPerLine []int // byte count per line (for Rewind)
	offset       int   // total byte offset
}

// New returns a new Position at the start of the stream: line 1, column 0, and
// offset 0.
func New() *Position {
	return &Position{
		mu: sync.Mutex{},
	}
}

func (p *Position) line() int {
	zl := len(p.runesPerLine)
	if zl < 1 {
		return 1
	}

	return zl
}

func (p *Position) column() int {
	zl := len(p.runesPerLine)

	if zl == 0 {
		return 0
	}

	return p.runesPerLine[zl-1]
}

// String returns the position formatted as "line:column".
func (p *Position) String() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return fmt.Sprintf("%d:%d", p.line(), p.column())
}

// Line returns the current 1-based line number.
func (p *Position) Line() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.line()
}

// Column returns the current column as the number of runes read since the last
// newline. It is 0 at the start of a line and before any input is scanned.
func (p *Position) Column() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.column()
}

// Offset returns the total number of bytes scanned from the start of the stream.
func (p *Position) Offset() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.offset
}

// State returns the current line, rune column, and byte offset together, under
// a single lock. It is the bulk form of Line, Column, and Offset.
func (p *Position) State() (line, column, offset int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.line(), p.column(), p.offset
}

// AdvanceBytes moves the byte offset by n without touching the line or the rune
// column.
//
// It exists for bytes whose rune contribution is not yet known: the trailing
// bytes of an incomplete UTF-8 sequence still count as bytes read, while their
// effect on the column waits until the sequence completes. n may be negative,
// which undoes a provisional advance; the caller must keep the offset
// non-negative.
func (p *Position) AdvanceBytes(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.offset += n
}

// ScanRunes credits the runes in in to the current column without advancing the
// byte offset. The caller has already advanced the offset over those bytes, and
// in must not contain a line break, so the line is unchanged. It is how the
// bytes of an incomplete UTF-8 sequence contribute their rune column once the
// sequence is settled.
func (p *Position) ScanRunes(in []byte) {
	if len(in) == 0 {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.runesPerLine) == 0 {
		p.runesPerLine = append(p.runesPerLine, 0)
	}

	p.runesPerLine[len(p.runesPerLine)-1] += utf8.RuneCount(in)
}

// Scan advances the position over in, updating the line, column, and offset,
// and returns the line, rune column, and byte offset the scan started from. It
// assumes in holds valid UTF-8; each '\n' starts a new line.
//
// The leading state is returned so a caller that needs the position of the
// first byte of in does not have to read it separately.
func (p *Position) Scan(in []byte) (line, column, offset int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	line, column, offset = p.line(), p.column(), p.offset

	zl := len(p.runesPerLine) - 1
	if zl < 0 {
		p.runesPerLine = []int{0}
		p.bytesPerLine = []int{0}
		zl = 0
	}

	for len(in) > 0 {
		r, size := utf8.DecodeRune(in)
		if r == newLine {
			p.runesPerLine = append(p.runesPerLine, 0)
			p.bytesPerLine = append(p.bytesPerLine, 0)
			zl++
		} else {
			p.runesPerLine[zl]++
			p.bytesPerLine[zl] += size
		}
		p.offset += size
		in = in[size:]
	}

	return line, column, offset
}

// Reset returns the position to the start of the stream (line 1, column 0,
// offset 0).
func (p *Position) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.reset()
}

func (p *Position) reset() {
	p.runesPerLine = p.runesPerLine[:0]
	p.bytesPerLine = p.bytesPerLine[:0]
	p.offset = 0
}

// Copy returns a deep copy of the position. The returned Position is
// independent of the receiver and carries its own zero-value mutex.
func (p *Position) Copy() *Position {
	p.mu.Lock()
	defer p.mu.Unlock()

	return &Position{
		// Note: mu is intentionally not copied - a fresh zero-value mutex is correct.
		// Per Go docs: "A Mutex must not be copied after first use."
		runesPerLine: append([]int(nil), p.runesPerLine...),
		bytesPerLine: append([]int(nil), p.bytesPerLine...),
		offset:       p.offset,
	}
}

// Rewind moves the position backward by the given number of bytes and runes,
// for example to undo a preceding Scan. Passing 0 for both is a no-op. It
// returns an error if either amount is negative or if bytes exceeds the number
// of bytes scanned so far.
func (p *Position) Rewind(bytes, runes int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch {
	case bytes == 0 && runes == 0:
		return nil // no-op
	case bytes < 0 || runes < 0:
		return fmt.Errorf("cannot rewind by negative amounts: bytes=%d, runes=%d", bytes, runes)
	case bytes > p.offset:
		return fmt.Errorf("cannot rewind by %d bytes, only %d available", bytes, p.offset)
	case bytes == p.offset:
		p.reset()
		return nil
	}

	bytesRewound := 0
	runesRewound := 0
	lastLine := len(p.bytesPerLine) - 1

	for bytesRewound < bytes && lastLine >= 0 {
		lineBytes := p.bytesPerLine[lastLine]
		lineRunes := p.runesPerLine[lastLine]
		remainingBytes := bytes - bytesRewound

		if remainingBytes <= lineBytes {
			// Partial rewind within this line
			break
		}

		// Consume entire line
		bytesRewound += lineBytes
		runesRewound += lineRunes
		lastLine--

		if lastLine >= 0 {
			bytesRewound++ // for the newline
			runesRewound++ // newline is 1 rune
		}
	}

	if lastLine >= 0 {
		remainingBytes := bytes - bytesRewound
		remainingRunes := runes - runesRewound

		if p.bytesPerLine[lastLine] >= remainingBytes {
			p.bytesPerLine[lastLine] -= remainingBytes
			p.runesPerLine[lastLine] -= remainingRunes
			p.bytesPerLine = p.bytesPerLine[:lastLine+1]
			p.runesPerLine = p.runesPerLine[:lastLine+1]
			p.offset -= bytes
			return nil
		}
	}

	return fmt.Errorf("rewind failed: wanted %d bytes, rewound %d", bytes, bytesRewound)
}
