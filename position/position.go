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

func (p *Position) String() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return fmt.Sprintf("%d:%d", p.line(), p.column())
}

func (p *Position) Line() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.line()
}

func (p *Position) Column() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.column()
}

func (p *Position) Offset() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.offset
}

func (p *Position) Scan(in []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()

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
}

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
