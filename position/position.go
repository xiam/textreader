package position

import (
	"fmt"
	"sync"
)

const newLine = '\n'

// Position represents a position in a text file.
type Position struct {
	mu sync.Mutex

	colsPerLine []int
	offset      int
}

func New() *Position {
	return &Position{
		mu: sync.Mutex{},
	}
}

func (p *Position) line() int {
	zl := len(p.colsPerLine)
	if zl < 1 {
		return 1
	}

	return zl
}

func (p *Position) column() int {
	zl := len(p.colsPerLine)

	if zl == 0 {
		return 0
	}

	return p.colsPerLine[zl-1]
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

	zl := len(p.colsPerLine) - 1
	if zl < 0 {
		p.colsPerLine = []int{0}
		zl = 0
	}

	for _, b := range in {
		if b == newLine {
			p.colsPerLine = append(p.colsPerLine, 0)
			zl++
		} else {
			p.colsPerLine[zl]++
		}
		p.offset++
	}
}

func (p *Position) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.reset()
}

func (p *Position) reset() {
	p.colsPerLine = p.colsPerLine[:0]
	p.offset = 0
}

func (p *Position) Copy() Position {
	p.mu.Lock()
	defer p.mu.Unlock()

	newPos := Position{
		colsPerLine: make([]int, len(p.colsPerLine)),
		offset:      p.offset,
	}

	copy(newPos.colsPerLine, p.colsPerLine)

	return newPos
}

func (p *Position) Rewind(positions int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch {
	case positions == 0:
		return nil // no-op
	case positions < 0:
		return fmt.Errorf("cannot rewind by a negative number: %d", positions)
	case positions > p.offset:
		return fmt.Errorf("cannot rewind by %d, only %d available", positions, p.offset)
	case positions == p.offset:
		p.reset()
		return nil
	}

	rewind := 0
	lastLine := len(p.colsPerLine) - 1

	for rewind < positions {
		columns := p.colsPerLine[lastLine]
		remaining := positions - rewind

		if remaining <= columns {
			break
		}

		rewind += columns
		lastLine--

		if lastLine >= 0 {
			rewind += 1 // for the newline
		}
	}

	if lastLine >= 0 {
		remaining := positions - rewind
		if p.colsPerLine[lastLine] >= remaining {
			p.colsPerLine[lastLine] -= remaining
			p.colsPerLine = p.colsPerLine[:lastLine+1]
			p.offset -= positions
			return nil
		}
	}

	return fmt.Errorf("rewind failed, wanted %d, had %d", positions, rewind)
}
