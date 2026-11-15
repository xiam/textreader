package textreader_test

import (
	"io"
	"math/rand"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/textreader"
)

// repeatReader yields n bytes of b without holding them in memory.
type repeatReader struct {
	b byte
	n int
}

func (r *repeatReader) Read(p []byte) (int, error) {
	if r.n == 0 {
		return 0, io.EOF
	}
	if len(p) > r.n {
		p = p[:r.n]
	}
	for i := range p {
		p[i] = r.b
	}
	r.n -= len(p)
	return len(p), nil
}

func liveHeap() uint64 {
	runtime.GC()
	runtime.GC()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

// TestLineTableMemoryIsBounded reads 4 MiB of newlines with no checkpoint and
// checks the live heap does not grow with the stream, on both read paths.
func TestLineTableMemoryIsBounded(t *testing.T) {
	const (
		capacity = 4096
		size     = 4 << 20
		ceiling  = 64 * capacity
	)

	read := map[string]func(*testing.T, *textreader.TextReader){
		"ReadRune": func(t *testing.T, tr *textreader.TextReader) {
			for {
				if _, _, err := tr.ReadRune(); err != nil {
					require.ErrorIs(t, err, io.EOF)
					return
				}
			}
		},
		"Read larger than capacity": func(t *testing.T, tr *textreader.TextReader) {
			p := make([]byte, 32<<10)
			for {
				if _, err := tr.Read(p); err != nil {
					require.ErrorIs(t, err, io.EOF)
					return
				}
			}
		},
	}

	for name, fn := range read {
		t.Run(name, func(t *testing.T) {
			tr := textreader.NewReader(&repeatReader{b: '\n', n: size}, textreader.WithCapacity(capacity))

			before := liveHeap()
			fn(t, tr)
			after := liveHeap()

			cur := tr.Cursor()
			assert.Equal(t, size+1, cur.Line())
			assert.Equal(t, size, cur.ByteOffset())

			var grown uint64
			if after > before {
				grown = after - before
			}
			assert.Less(t, grown, uint64(ceiling), "live heap grew by %d bytes over %d bytes of newlines", grown, size)

			runtime.KeepAlive(tr)
		})
	}
}

// lineCol is the oracle: the line and rune column of offset in in.
func lineCol(in string, offset int) (line, column int) {
	head := in[:offset]
	line = strings.Count(head, "\n") + 1
	column = utf8.RuneCountInString(head[strings.LastIndexByte(head, '\n')+1:])
	return line, column
}

func requireAt(t *testing.T, tr *textreader.TextReader, in string, offset int, msgAndArgs ...interface{}) {
	t.Helper()

	line, column := lineCol(in, offset)
	cur := tr.Cursor()
	require.Equal(t, [3]int{offset, line, column}, [3]int{cur.ByteOffset(), cur.Line(), cur.Column()}, msgAndArgs...)

	pos := tr.Pos()
	require.Equal(t, [3]int{offset, line, column}, [3]int{pos.Offset(), pos.Line(), pos.Column()}, msgAndArgs...)
}

// bufferStart seeks backward, one rune at a time, to the oldest rune the reader
// still buffers, and returns its offset. The reader is left there.
func bufferStart(t *testing.T, tr *textreader.TextReader, in string) int {
	t.Helper()

	start := tr.Cursor().ByteOffset()
	for start > 0 {
		prev := start - 1
		for prev > 0 && !utf8.RuneStart(in[prev]) {
			prev--
		}
		if _, err := tr.Seek(int64(prev), io.SeekStart); err != nil {
			require.ErrorIs(t, err, textreader.ErrSeekOutOfBuffer)
			break
		}
		start = prev
	}
	return start
}

// TestTrimKeepsUnreadAndSeekExact reads through small buffers with no
// checkpoint, so the line table is trimmed often, and checks that UnreadRune
// and a backward Seek to the buffer start report exact positions. It also
// checks that the buffer start lands on a newline byte at least once.
func TestTrimKeepsUnreadAndSeekExact(t *testing.T) {
	in := strings.Repeat("ab\n\ncdé\n世\n🦄x\n", 40)

	startOnNewline := 0

	for capacity := 4; capacity <= 24; capacity++ {
		tr := textreader.NewReader(strings.NewReader(in), textreader.WithCapacity(capacity))

		for offset := 0; offset < len(in); {
			_, size, err := tr.ReadRune()
			require.NoError(t, err)
			next := offset + size

			// UnreadRune over the rune just read, which may be a newline.
			require.NoError(t, tr.UnreadRune())
			requireAt(t, tr, in, offset, "capacity %d: UnreadRune to %d", capacity, offset)
			_, _, err = tr.ReadRune()
			require.NoError(t, err)

			// Seek back to the oldest buffered byte, then forward again.
			start := bufferStart(t, tr, in)
			requireAt(t, tr, in, start, "capacity %d: Seek to buffer start %d", capacity, start)
			if start < len(in) && in[start] == '\n' {
				startOnNewline++
			}
			_, err = tr.Seek(int64(next), io.SeekStart)
			require.NoError(t, err)
			requireAt(t, tr, in, next, "capacity %d: Seek forward to %d", capacity, next)

			offset = next
		}
	}

	assert.Positive(t, startOnNewline, "no buffer start landed on a newline byte")
}

// TestTrimKeepsCheckpointResetExact trims the table while checkpoints are live
// and checks that Reset rewinds across several lines to the exact position.
func TestTrimKeepsCheckpointResetExact(t *testing.T) {
	in := strings.Repeat("ab\ncdé\n\n世界\n", 300)

	tr := textreader.NewReader(strings.NewReader(in), textreader.WithCapacity(16))

	offset := 0
	for offset < len(in) {
		cp := tr.Checkpoint()
		mark := offset

		// Read up to ten lines ahead.
		ahead := mark
		for lines := 0; lines < 10 && ahead < len(in); {
			lr, err := tr.ReadLocatedRune()
			require.NoError(t, err)
			line, column := lineCol(in, ahead)
			require.Equal(t, [2]int{line, column}, [2]int{lr.Pos().Line(), lr.Pos().Column()}, "rune at %d", ahead)
			if lr.Rune() == '\n' {
				lines++
			}
			ahead += lr.Size()
		}

		require.NoError(t, cp.Reset())
		requireAt(t, tr, in, mark, "Reset to %d", mark)

		// Commit half of the window, so the next mark is further on.
		for offset < mark+(ahead-mark)/2+1 && offset < len(in) {
			_, size, err := tr.ReadRune()
			require.NoError(t, err)
			offset += size
		}
		require.NoError(t, cp.Commit())
	}
}

// TestPosCopyRewindReach covers the one semantics change: a Pos copy holds the
// reader's trimmed line table, so it cannot Rewind far behind the buffer.
func TestPosCopyRewindReach(t *testing.T) {
	in := strings.Repeat("ab\n", 40000)

	const end = 100000

	tr := textreader.NewReader(strings.NewReader(in), textreader.WithCapacity(4096))
	for tr.Cursor().ByteOffset() < end {
		_, _, err := tr.ReadRune()
		require.NoError(t, err)
	}

	// Find the oldest buffered byte, then return the reader to the end.
	start := bufferStart(t, tr, in)
	_, err := tr.Seek(end, io.SeekStart)
	require.NoError(t, err)
	require.Less(t, end-start, 4096)

	p := tr.Pos()
	require.Equal(t, end, p.Offset())

	// Far behind the buffer: the line positions are gone.
	err = p.Rewind(end-1000, end-1000)
	require.Error(t, err)
	assert.Equal(t, end, p.Offset(), "a failed Rewind leaves the copy unchanged")

	// Back to the buffer start: exact.
	require.NoError(t, p.Rewind(end-start, end-start))
	line, column := lineCol(in, start)
	assert.Equal(t, [3]int{start, line, column}, [3]int{p.Offset(), p.Line(), p.Column()})

	// To offset 0: reset.
	require.NoError(t, p.Rewind(p.Offset(), 0))
	assert.Equal(t, "1:0", p.String())
	assert.Equal(t, 0, p.Offset())

	// The reader itself is unaffected.
	requireAt(t, tr, in, end)
}

// TestCheckpointResetAfterSplitRuneRead covers a Read that ends inside a
// multi-byte rune, followed by Reset on a checkpoint whose line is the oldest
// kept line entry. The carried bytes must be settled before the rewind.
func TestCheckpointResetAfterSplitRuneRead(t *testing.T) {
	in := "ab\n" + strings.Repeat("é", 20)

	for _, capacity := range []int{2, 4, 8} {
		tr := textreader.NewReader(strings.NewReader(in), textreader.WithCapacity(capacity))

		n, err := tr.Read(make([]byte, 3))
		require.NoError(t, err)
		require.Equal(t, 3, n)
		requireAt(t, tr, in, 3, "capacity %d: after the first Read", capacity)

		cp := tr.Checkpoint()

		// Two é and the first byte of the third.
		n, err = tr.Read(make([]byte, 5))
		require.NoError(t, err)
		require.Equal(t, 5, n)

		require.NoError(t, cp.Reset(), "capacity %d", capacity)
		requireAt(t, tr, in, 3, "capacity %d: after Reset", capacity)

		// The replay reads the same runes from the mark.
		for i := 0; i < 3; i++ {
			lr, err := tr.ReadLocatedRune()
			require.NoError(t, err)
			require.Equal(t, 'é', lr.Rune())
			require.Equal(t, [2]int{2, i}, [2]int{lr.Pos().Line(), lr.Pos().Column()})
		}
		require.NoError(t, cp.Commit())
	}
}

// TestCheckpointResetMidRuneMark covers a checkpoint taken while a Read left a
// partial UTF-8 sequence pending. Reset restores the marked position and the
// pending bytes, so the replay completes the rune.
func TestCheckpointResetMidRuneMark(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("a世b"), textreader.WithCapacity(16))

	_, err := tr.Read(make([]byte, 3)) // ends inside 世
	require.NoError(t, err)

	cp := tr.Checkpoint()
	assert.Equal(t, [3]int{3, 1, 1}, [3]int{cp.Pos().ByteOffset(), cp.Pos().Line(), cp.Pos().Column()})

	require.NoError(t, cp.Reset())
	cur := tr.Cursor()
	assert.Equal(t, [3]int{3, 1, 1}, [3]int{cur.ByteOffset(), cur.Line(), cur.Column()})

	// Read on, Reset again, and finish the rune from the mark.
	_, err = tr.Read(make([]byte, 2))
	require.NoError(t, err)
	require.NoError(t, cp.Reset())
	assert.Equal(t, cp.Pos(), tr.Cursor())

	rest := make([]byte, 2)
	n, err := tr.Read(rest)
	require.NoError(t, err)
	assert.Equal(t, "\x96b", string(rest[:n]), "the replay continues after the marked byte")
	cur = tr.Cursor()
	assert.Equal(t, [3]int{5, 1, 3}, [3]int{cur.ByteOffset(), cur.Line(), cur.Column()}, "世 completes from the restored carry")
	require.NoError(t, cp.Commit())
}

// TestCheckpointResetWithSplitReads drives random byte reads, rune reads,
// marks, and resets over multi-byte text through small buffers. After every
// Reset the cursor equals the marked position; at every rune boundary with
// nothing pending it equals the oracle; and the bytes read always continue the
// input.
func TestCheckpointResetWithSplitReads(t *testing.T) {
	in := strings.Repeat("a世\né\n🦄b\n\n", 30)
	rng := rand.New(rand.NewSource(23))

	for iter := 0; iter < 300; iter++ {
		tr := textreader.NewReader(strings.NewReader(in), textreader.WithCapacity(4+rng.Intn(12)))

		type mark struct {
			cp     *textreader.Checkpoint
			offset int
		}
		var marks []mark
		offset := 0 // bytes read, by the oracle

		for steps := 0; steps < 200 && offset < len(in); steps++ {
			op := rng.Intn(10)
			if op >= 4 && op < 6 && !utf8.RuneStart(in[offset]) {
				// A rune read while a partial sequence is pending settles it
				// as replacement runes by design; keep rune reads on rune
				// boundaries so the oracle applies.
				op = 0
			}
			switch {
			case op < 4:
				p := make([]byte, 1+rng.Intn(5))
				n, err := tr.Read(p)
				if err != nil {
					require.ErrorIs(t, err, io.EOF)
				}
				require.Equal(t, in[offset:offset+n], string(p[:n]), "iter %d: Read at %d", iter, offset)
				offset += n
			case op < 6:
				lr, err := tr.ReadLocatedRune()
				require.NoError(t, err)
				line, column := lineCol(in, offset)
				require.Equal(t, [3]int{offset, line, column}, [3]int{lr.Pos().ByteOffset(), lr.Pos().Line(), lr.Pos().Column()}, "iter %d: rune at %d", iter, offset)
				offset += lr.Size()
			case op < 8 && len(marks) < 4:
				marks = append(marks, mark{tr.Checkpoint(), offset})
			case len(marks) > 0:
				i := rng.Intn(len(marks))
				m := marks[i]
				require.NoError(t, m.cp.Reset(), "iter %d: Reset to %d", iter, m.offset)
				require.Equal(t, m.cp.Pos(), tr.Cursor(), "iter %d: Reset to %d", iter, m.offset)
				offset = m.offset
				// Marks after the one reset to are abandoned.
				for _, later := range marks[i+1:] {
					require.NoError(t, later.cp.Release())
				}
				marks = marks[:i+1]
			}
		}

		for _, m := range marks {
			require.NoError(t, m.cp.Release())
		}

		// Read the rest; the end position matches the oracle.
		rest, err := io.ReadAll(tr)
		require.NoError(t, err)
		require.Equal(t, in[offset:], string(rest), "iter %d", iter)
		requireAt(t, tr, in, len(in), "iter %d: end", iter)
	}
}
