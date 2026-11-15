package position_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/textreader/position"
)

// rewindTo rewinds p from the end of in to the byte offset target and returns
// the Rewind error.
func rewindTo(p *position.Position, in string, target int) error {
	return p.Rewind(len(in)-target, utf8.RuneCountInString(in[target:]))
}

// TestTrimBeforeKeepsRewindExact compares a trimmed position against an
// untrimmed one: every Rewind to an offset at or above the trim base must report
// the same line, column, and offset.
func TestTrimBeforeKeepsRewindExact(t *testing.T) {
	inputs := []string{
		"ab\ncd\n\nefg\nh",
		"\n\n\n\n\n\n",
		"αβ\n世界\n🦄\nζηθ\n",
		strings.Repeat("x\n", 40) + "tail",
	}

	for _, in := range inputs {
		for floor := 0; floor <= len(in); floor++ {
			trimmed := position.New()
			trimmed.Scan([]byte(in))
			trimmed.TrimBefore(floor)

			assert.Equal(t, scanned(in).String(), trimmed.String(), "input %q floor %d: trim must not move the position", in, floor)
			assert.Equal(t, len(in), trimmed.Offset())

			for target := floor; target <= len(in); target++ {
				if target < len(in) && !utf8.RuneStart(in[target]) {
					continue
				}

				want := scanned(in)
				require.NoError(t, rewindTo(want, in, target))

				got := trimmed.Copy()
				require.NoError(t, rewindTo(got, in, target), "input %q floor %d target %d", in, floor, target)

				wl, wc, wo := want.State()
				gl, gc, gotOff := got.State()
				assert.Equal(t, [3]int{wl, wc, wo}, [3]int{gl, gc, gotOff}, "input %q floor %d target %d", in, floor, target)
			}
		}
	}
}

func scanned(in string) *position.Position {
	p := position.New()
	p.Scan([]byte(in))
	return p
}

// TestTrimBeforeFloorOnNewline pins the boundary: a floor that sits exactly on
// a newline byte keeps that line's entry, so a Rewind to the newline reports the
// end of the line.
func TestTrimBeforeFloorOnNewline(t *testing.T) {
	in := "aaa\nbbbb\ncc\ndd"
	newline := strings.Index(in, "bbbb\n") + len("bbbb") // offset 8, the second '\n'

	p := scanned(in)
	p.TrimBefore(newline)

	require.NoError(t, rewindTo(p, in, newline))
	assertState(t, p, 2, 4, newline)

	// One byte below the newline is still inside the kept line.
	require.NoError(t, p.Rewind(1, 1))
	assertState(t, p, 2, 3, newline-1)

	// One byte above the newline drops the line: the newline is out of reach.
	p = scanned(in)
	p.TrimBefore(newline + 1)
	require.Error(t, rewindTo(p, in, newline))
	require.NoError(t, rewindTo(p, in, newline+1))
	assertState(t, p, 3, 0, newline+1)
}

// TestTrimBeforeRewindBelowBase checks the narrowed reach of a trimmed
// position: a Rewind below the oldest kept line fails and leaves the position
// unchanged, while a Rewind to offset 0 still resets it.
func TestTrimBeforeRewindBelowBase(t *testing.T) {
	in := strings.Repeat("line\n", 10) + "end"

	p := scanned(in)
	p.TrimBefore(len(in))
	line, column, offset := p.State()
	require.Equal(t, 11, line)

	// The oldest kept line is the current one, which starts at offset 50.
	err := rewindTo(p, in, 49)
	require.Error(t, err)
	assertState(t, p, line, column, offset)

	require.NoError(t, rewindTo(p, in, 50))
	assertState(t, p, 11, 0, 50)

	require.NoError(t, p.Rewind(p.Offset(), 50))
	assertState(t, p, 1, 0, 0)

	// The reset position scans and rewinds from the start again.
	p.Scan([]byte("ab\ncd"))
	require.NoError(t, p.Rewind(3, 3))
	assertState(t, p, 1, 2, 2)
}

// TestTrimBeforeIsIncremental trims while scanning, as a reader does when its
// buffer start advances, and checks line numbers stay exact.
func TestTrimBeforeIsIncremental(t *testing.T) {
	const lines = 10000

	p := position.New()
	for i := 0; i < lines; i++ {
		p.Scan([]byte("ab\n"))
		p.TrimBefore(p.Offset() - 7)
	}

	assertState(t, p, lines+1, 0, 3*lines)

	// The last two newlines are still within reach.
	require.NoError(t, p.Rewind(4, 4))
	assertState(t, p, lines-1, 2, 3*lines-4)
}

func TestTrimBeforeNoop(t *testing.T) {
	p := position.New()
	p.TrimBefore(100)
	assertState(t, p, 1, 0, 0)

	p.Scan([]byte("abc"))
	p.TrimBefore(100)
	assertState(t, p, 1, 3, 3)
	require.NoError(t, p.Rewind(2, 2))
	assertState(t, p, 1, 1, 1)
}

func assertState(t *testing.T, p *position.Position, line, column, offset int) {
	t.Helper()

	l, c, o := p.State()
	assert.Equal(t, [3]int{line, column, offset}, [3]int{l, c, o}, "line, column, offset")
}
