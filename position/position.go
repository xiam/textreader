package position

import (
	"fmt"
	"sync"
)

const newLine = '\n'

// Position represents a position in a text file.
type Position struct {
	mu sync.Mutex

	lines  []int
	offset int
}

func New() *Position {
	return &Position{
		lines: []int{0},
		mu:    sync.Mutex{},
	}
}

func (p *Position) line() int {
	return len(p.lines)
}

func (p *Position) column() int {
	if len(p.lines) == 0 {
		return 0
	}
	return p.lines[len(p.lines)-1]
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

	for _, b := range in {
		if b == newLine {
			p.lines = append(p.lines, 0)
		} else {
			p.lines[len(p.lines)-1]++
		}
		p.offset++
	}
}

func (p *Position) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.lines = []int{0}
	p.offset = 0
}

func (p *Position) Copy() Position {
	p.mu.Lock()
	defer p.mu.Unlock()

	return Position{}
}

func (p *Position) Rewind(n int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if n < 0 {
		return fmt.Errorf("cannot rewind by a negative number")
	}

	p.offset -= n
	if p.offset < 0 {
		p.offset = 0
	}

	for n > 0 {
		ll := len(p.lines) - 1
		if ll < 0 {
			break
		}
		if n >= p.lines[ll] {
			n = n - p.lines[ll] - 1
			p.lines = p.lines[:ll]
		} else {
			p.lines[ll] = p.lines[ll] - n
			n = 0
		}
	}

	if len(p.lines) == 0 {
		p.lines = []int{0}
	}

	return nil
}
