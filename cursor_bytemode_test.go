package textreader_test

import (
	"bytes"
	"io"
	"math/rand"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/textreader"
)

// allBytes holds every byte value once, 0x00 to 0xFF, in order. It contains
// 0x0A and every byte that is invalid UTF-8 on its own. utf8Bytes covers
// sequences that are valid UTF-8 in text mode.
func allBytes() []byte {
	b := make([]byte, 256)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

// utf8Bytes is valid UTF-8 text with multibyte runes and newlines. Byte mode
// must still read it as one rune per byte.
var utf8Bytes = []byte("é\n世界\n🦄x")

// bytePos is the reference model for byte mode: the line and column of file
// offset off in data. Line is 1 plus the number of 0x0A bytes before off, and
// the column counts the bytes since the last of them.
func bytePos(data []byte, off int) (line, column int) {
	prefix := data[:off]
	line = 1 + bytes.Count(prefix, []byte{'\n'})
	column = off - (bytes.LastIndexByte(prefix, '\n') + 1)
	return line, column
}

// requireByteCursor asserts that p is the byte-mode position of file offset off.
func requireByteCursor(t *testing.T, data []byte, off int, p textreader.Pos) {
	t.Helper()

	line, column := bytePos(data, off)
	require.Equal(t, off, p.ByteOffset(), "byte offset")
	require.Equal(t, off, p.RuneOffset(), "rune offset")
	require.Equal(t, line, p.Line(), "line at offset %d", off)
	require.Equal(t, column, p.Column(), "column at offset %d", off)
}

func TestByteReaderEveryByteValue(t *testing.T) {
	inputs := map[string][]byte{
		"all bytes":            allBytes(),
		"all bytes reversed":   reversed(allBytes()),
		"valid utf-8 as bytes": utf8Bytes,
	}

	for name, data := range inputs {
		t.Run(name, func(t *testing.T) {
			t.Run("ReadRune", func(t *testing.T) {
				tr := textreader.NewByteReader(bytes.NewReader(data))
				for i, want := range data {
					r, size, err := tr.ReadRune()
					require.NoError(t, err)
					assert.Equal(t, rune(want), r, "offset %d", i)
					assert.Equal(t, 1, size, "offset %d", i)
					requireByteCursor(t, data, i+1, tr.Cursor())
				}
				_, _, err := tr.ReadRune()
				assert.ErrorIs(t, err, io.EOF)
			})

			t.Run("ReadLocatedRune and SpanText", func(t *testing.T) {
				tr := textreader.NewByteReader(bytes.NewReader(data))
				cp := tr.Checkpoint()
				defer func() { _ = cp.Release() }()

				var first, last textreader.LocatedRune
				for i, want := range data {
					lr, err := tr.ReadLocatedRune()
					require.NoError(t, err)
					assert.Equal(t, rune(want), lr.Rune())
					assert.Equal(t, 1, lr.Size())
					requireByteCursor(t, data, i, lr.Pos())
					requireByteCursor(t, data, i+1, lr.End())

					text, err := tr.SpanText(lr.Span())
					require.NoError(t, err)
					assert.Equal(t, string(data[i:i+1]), text)

					if i == 0 {
						first = lr
					}
					last = lr
				}

				span := textreader.NewSpan(first.Pos(), last.End())
				assert.Equal(t, len(data), span.Bytes())
				assert.Equal(t, len(data), span.Runes())
				text, err := tr.SpanText(span)
				require.NoError(t, err)
				assert.Equal(t, string(data), text)

				// Checkpoint.Reset replays the same bytes from the mark.
				require.NoError(t, cp.Reset())
				requireByteCursor(t, data, 0, tr.Cursor())
				got := make([]byte, len(data))
				_, err = io.ReadFull(tr, got)
				require.NoError(t, err)
				assert.Equal(t, data, got)
				requireByteCursor(t, data, len(data), tr.Cursor())
			})

			t.Run("Read", func(t *testing.T) {
				tr := textreader.NewByteReader(bytes.NewReader(data))
				got, err := io.ReadAll(tr)
				require.NoError(t, err)
				assert.Equal(t, data, got)
				requireByteCursor(t, data, len(data), tr.Cursor())

				pos := tr.Pos()
				line, column := bytePos(data, len(data))
				assert.Equal(t, line, pos.Line())
				assert.Equal(t, column, pos.Column())
				assert.Equal(t, len(data), pos.Offset())
			})

			t.Run("Seek", func(t *testing.T) {
				tr := textreader.NewByteReader(bytes.NewReader(data))
				_, err := io.ReadAll(tr)
				require.NoError(t, err)

				// Every offset is reachable, backward from the end and then
				// forward again, and reads the byte at that file offset.
				for _, off := range seekOrder(len(data)) {
					n, err := tr.Seek(int64(off), io.SeekStart)
					require.NoError(t, err)
					require.Equal(t, int64(off), n)
					requireByteCursor(t, data, off, tr.Cursor())

					if off < len(data) {
						lr, err := tr.PeekRune()
						require.NoError(t, err)
						assert.Equal(t, rune(data[off]), lr.Rune())
						requireByteCursor(t, data, off, lr.Pos())
					}
				}
			})

			t.Run("PeekRune", func(t *testing.T) {
				tr := textreader.NewByteReader(bytes.NewReader(data))
				for i, want := range data {
					peeked, err := tr.PeekRune()
					require.NoError(t, err)
					assert.Equal(t, rune(want), peeked.Rune())
					assert.Equal(t, 1, peeked.Size())
					requireByteCursor(t, data, i, peeked.Pos())
					requireByteCursor(t, data, i, tr.Cursor())

					read, err := tr.ReadLocatedRune()
					require.NoError(t, err)
					assert.Equal(t, peeked, read)
				}
				_, err := tr.PeekRune()
				assert.ErrorIs(t, err, io.EOF)
			})

			t.Run("UnreadRune", func(t *testing.T) {
				tr := textreader.NewByteReader(bytes.NewReader(data))
				for i, want := range data {
					_, _, err := tr.ReadRune()
					require.NoError(t, err)
					require.NoError(t, tr.UnreadRune())
					requireByteCursor(t, data, i, tr.Cursor())

					r, size, err := tr.ReadRune()
					require.NoError(t, err)
					assert.Equal(t, rune(want), r)
					assert.Equal(t, 1, size)
					requireByteCursor(t, data, i+1, tr.Cursor())
				}
			})
		})
	}
}

func reversed(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[len(b)-1-i] = c
	}
	return out
}

// seekOrder visits every offset of an n-byte stream from n down to 0, then
// from 0 up to n, so each seek moves the cursor backward or forward by one.
func seekOrder(n int) []int {
	order := make([]int, 0, 2*n+2)
	for off := n; off >= 0; off-- {
		order = append(order, off)
	}
	for off := 0; off <= n; off++ {
		order = append(order, off)
	}
	return order
}

func TestByteReaderLinesEndOnlyAtLineFeed(t *testing.T) {
	// 0x0D, 0x85 (NEL in Latin-1) and 0xE2 0x80 0xA8 (U+2028 in UTF-8) are
	// bytes like any other in byte mode. Only 0x0A ends a line.
	data := []byte("ab\r\x85\n\xe2\x80\xa8\n\nz")
	tr := textreader.NewByteReader(bytes.NewReader(data))

	type want struct{ line, column int }
	expected := []want{
		{1, 0}, {1, 1}, {1, 2}, {1, 3}, {1, 4}, // a b \r \x85 \n
		{2, 0}, {2, 1}, {2, 2}, {2, 3}, // \xe2 \x80 \xa8 \n
		{3, 0}, // \n
		{4, 0}, // z
	}
	require.Len(t, expected, len(data))

	for i, w := range expected {
		lr, err := tr.ReadLocatedRune()
		require.NoError(t, err)
		assert.Equal(t, w.line, lr.Pos().Line(), "line at offset %d", i)
		assert.Equal(t, w.column, lr.Pos().Column(), "column at offset %d", i)
		requireByteCursor(t, data, i, lr.Pos())
	}

	end := tr.Cursor()
	assert.Equal(t, 4, end.Line())
	assert.Equal(t, 1, end.Column())
}

func TestByteReaderReadMixedWithReadRune(t *testing.T) {
	// The chunks split valid UTF-8 sequences, which text mode carries across
	// reads. Byte mode counts each byte when it is read.
	data := append(append([]byte{}, utf8Bytes...), allBytes()...)

	rng := rand.New(rand.NewSource(25))
	for trial := 0; trial < 50; trial++ {
		tr := textreader.NewByteReader(
			iotest.OneByteReader(bytes.NewReader(data)),
			textreader.WithCapacity(8),
		)

		off := 0
		for off < len(data) {
			if rng.Intn(2) == 0 {
				r, size, err := tr.ReadRune()
				require.NoError(t, err)
				require.Equal(t, rune(data[off]), r)
				require.Equal(t, 1, size)
				off++
			} else {
				p := make([]byte, 1+rng.Intn(20))
				n, err := tr.Read(p)
				require.NoError(t, err)
				require.Equal(t, data[off:off+n], p[:n])
				off += n
			}
			requireByteCursor(t, data, off, tr.Cursor())

			// A peek after a Read that ended inside a UTF-8 sequence reports
			// the file offset, with no projected carry.
			if off < len(data) {
				lr, err := tr.PeekRune()
				require.NoError(t, err)
				requireByteCursor(t, data, off, lr.Pos())
			}
		}
	}
}

func TestByteReaderLargeRead(t *testing.T) {
	// A Read larger than the buffer capacity reads directly from the source
	// and accounts the bytes in chunks.
	data := bytes.Repeat(append(allBytes(), utf8Bytes...), 64)

	tr := textreader.NewByteReader(bytes.NewReader(data), textreader.WithCapacity(64))
	p := make([]byte, len(data)/2)
	n, err := io.ReadFull(tr, p)
	require.NoError(t, err)
	assert.Equal(t, data[:n], p)
	requireByteCursor(t, data, n, tr.Cursor())

	rest, err := io.ReadAll(tr)
	require.NoError(t, err)
	assert.Equal(t, data[n:], rest)
	requireByteCursor(t, data, len(data), tr.Cursor())
}

func TestByteReaderCheckpointReset(t *testing.T) {
	// The valid UTF-8 sequences put multibyte runes between the cursor and
	// the mark, where text mode would count fewer runes than bytes.
	data := bytes.Join([][]byte{utf8Bytes, allBytes(), utf8Bytes, allBytes()}, nil)

	t.Run("backward to the mark", func(t *testing.T) {
		tr := textreader.NewByteReader(bytes.NewReader(data), textreader.WithCapacity(16))

		_, err := io.CopyN(io.Discard, tr, 100)
		require.NoError(t, err)
		cp := tr.Checkpoint()
		defer func() { _ = cp.Release() }()
		requireByteCursor(t, data, 100, cp.Pos())

		_, err = io.CopyN(io.Discard, tr, 300)
		require.NoError(t, err)
		require.NoError(t, cp.Reset())
		requireByteCursor(t, data, 100, tr.Cursor())

		for i := 100; i < len(data); i++ {
			lr, err := tr.ReadLocatedRune()
			require.NoError(t, err)
			require.Equal(t, rune(data[i]), lr.Rune())
			requireByteCursor(t, data, i, lr.Pos())
		}
	})

	t.Run("forward to a mark ahead of the cursor", func(t *testing.T) {
		// Reset scans the retained bytes between the cursor and the mark.
		// Every byte must count as one rune there too.
		for _, mark := range []int{0x0B, 0x0F, 0x80, 0xC3, 0x10F, 0x11F, 0x1C3, len(data) - 1} {
			tr := textreader.NewByteReader(bytes.NewReader(data), textreader.WithCapacity(16))
			outer := tr.Checkpoint()

			_, err := io.CopyN(io.Discard, tr, int64(mark))
			require.NoError(t, err)
			cp := tr.Checkpoint()
			requireByteCursor(t, data, mark, cp.Pos())

			for _, back := range []int{0, 1, mark / 2, mark - 1} {
				_, err = tr.Seek(int64(back), io.SeekStart)
				require.NoError(t, err)
				requireByteCursor(t, data, back, tr.Cursor())

				require.NoError(t, cp.Reset())
				requireByteCursor(t, data, mark, tr.Cursor())

				lr, err := tr.ReadLocatedRune()
				require.NoError(t, err)
				assert.Equal(t, rune(data[mark]), lr.Rune())
				requireByteCursor(t, data, mark, lr.Pos())
			}

			require.NoError(t, cp.Release())
			require.NoError(t, outer.Release())
		}
	})
}

func TestByteReaderRemark(t *testing.T) {
	data := append(allBytes(), allBytes()...)
	tr := textreader.NewByteReader(bytes.NewReader(data), textreader.WithCapacity(16))

	slot := tr.Checkpoint()
	for mark := 0; mark+40 <= len(data); mark += 37 {
		if mark > 0 {
			require.NoError(t, tr.Remark(slot))
		}
		requireByteCursor(t, data, mark, slot.Pos())

		want := make([]byte, 40)
		_, err := io.ReadFull(tr, want)
		require.NoError(t, err)
		assert.Equal(t, data[mark:mark+40], want)

		require.NoError(t, slot.Reset())
		requireByteCursor(t, data, mark, tr.Cursor())

		_, err = io.CopyN(io.Discard, tr, 37)
		require.NoError(t, err)
		requireByteCursor(t, data, mark+37, tr.Cursor())
		require.NoError(t, slot.Release())
	}
}

func TestByteReaderContextReturnsRawBytes(t *testing.T) {
	data := []byte("\x00\xff\xd8\xff\xe0\n\xc3\xa9\x80")
	tr := textreader.NewByteReader(bytes.NewReader(data))
	cp := tr.Checkpoint()
	defer func() { _ = cp.Release() }()

	var runes []textreader.LocatedRune
	for range data {
		lr, err := tr.ReadLocatedRune()
		require.NoError(t, err)
		runes = append(runes, lr)
	}

	span := textreader.NewSpan(runes[2].Pos(), runes[4].End())
	text, err := tr.SpanText(span)
	require.NoError(t, err)
	assert.Equal(t, "\xd8\xff\xe0", text)

	around, err := tr.Context(span, 2, 3)
	require.NoError(t, err)
	assert.Equal(t, "\x00\xff\xd8\xff\xe0\n\xc3\xa9", around)
}

// highBytes returns n bytes, all of them 0x80 or above.
func highBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(0x80 + i%0x80)
	}
	return b
}

// byteRunes reads each byte of a reader as one rune. It is the adapter a
// consumer had to write before byte mode: the cursor re-encodes each rune of
// 0x80 and above as two bytes of UTF-8.
type byteRunes struct{ r io.ByteReader }

func (b byteRunes) ReadRune() (rune, int, error) {
	c, err := b.r.ReadByte()
	if err != nil {
		return 0, 0, err
	}
	return rune(c), 1, nil
}

func TestByteReaderRetentionCountsFileBytes(t *testing.T) {
	const n = 1000
	data := append(highBytes(n), 'x')

	readMarked := func(tr *textreader.TextReader, count int) error {
		cp := tr.Checkpoint()
		defer func() { _ = cp.Release() }()
		for i := 0; i < count; i++ {
			if _, err := tr.ReadLocatedRune(); err != nil {
				return err
			}
		}
		return nil
	}

	t.Run("byte mode retains n bytes", func(t *testing.T) {
		tr := textreader.NewByteReader(bytes.NewReader(data),
			textreader.WithCapacity(64), textreader.WithMaxRetained(n))

		cp := tr.Checkpoint()
		for i := 0; i < n; i++ {
			_, err := tr.ReadLocatedRune()
			require.NoError(t, err, "byte %d", i)
		}
		assert.Equal(t, n, tr.RetainedBytes())

		// One more byte exceeds the limit and leaves the cursor unchanged.
		_, err := tr.ReadLocatedRune()
		require.ErrorIs(t, err, textreader.ErrRetentionExceeded)
		requireByteCursor(t, data, n, tr.Cursor())

		_, err = tr.Read(make([]byte, 1))
		require.ErrorIs(t, err, textreader.ErrRetentionExceeded)
		_, err = tr.Seek(1, io.SeekCurrent)
		require.ErrorIs(t, err, textreader.ErrRetentionExceeded)
		requireByteCursor(t, data, n, tr.Cursor())

		require.NoError(t, cp.Reset())
		got := make([]byte, n)
		_, err = io.ReadFull(tr, got)
		require.NoError(t, err)
		assert.Equal(t, data[:n], got)
		require.NoError(t, cp.Commit())
	})

	t.Run("rune adapter needs 2n", func(t *testing.T) {
		adapter := func(limit int) *textreader.TextReader {
			return textreader.NewRuneReader(byteRunes{bytes.NewReader(data)},
				textreader.WithCapacity(64), textreader.WithMaxRetained(limit))
		}

		assert.ErrorIs(t, readMarked(adapter(n), n), textreader.ErrRetentionExceeded)
		assert.NoError(t, readMarked(adapter(2*n), n))
	})
}

func TestByteReaderRandomOperations(t *testing.T) {
	// A random walk over Read, ReadRune, UnreadRune, PeekRune, Seek and
	// checkpoints through a small buffer. After every step the cursor is the
	// model's file offset.
	rng := rand.New(rand.NewSource(0x25))
	data := make([]byte, 4096)
	rng.Read(data)
	for i := range data {
		if rng.Intn(16) == 0 {
			data[i] = '\n'
		}
	}

	for trial := 0; trial < 20; trial++ {
		tr := textreader.NewByteReader(
			iotest.HalfReader(bytes.NewReader(data)),
			textreader.WithCapacity(32),
		)

		off := 0
		canUnread := false
		var cp *textreader.Checkpoint
		cpOff := 0

		for step := 0; step < 2000 && off < len(data); step++ {
			switch op := rng.Intn(7); op {
			case 0:
				_, _, err := tr.ReadRune()
				require.NoError(t, err)
				off++
				canUnread = true
				requireByteCursor(t, data, off, tr.Cursor())
				continue
			case 1:
				if canUnread {
					require.NoError(t, tr.UnreadRune())
					off--
				}
			case 2:
				p := make([]byte, 1+rng.Intn(48))
				n, err := tr.Read(p)
				require.NoError(t, err)
				require.Equal(t, data[off:off+n], p[:n])
				off += n
			case 3:
				lr, err := tr.PeekRune()
				require.NoError(t, err)
				require.Equal(t, rune(data[off]), lr.Rune())
			case 4:
				// Seek backward within the bytes a checkpoint retains, or
				// forward within the buffer.
				target := off - rng.Intn(8)
				if cp != nil {
					target = cpOff + rng.Intn(off-cpOff+1)
				}
				if target < 0 {
					target = 0
				}
				if _, err := tr.Seek(int64(target), io.SeekStart); err == nil {
					off = target
				}
			case 5:
				if cp == nil {
					cp, cpOff = tr.Checkpoint(), off
				} else {
					require.NoError(t, cp.Reset())
					off = cpOff
				}
			case 6:
				if cp != nil {
					require.NoError(t, cp.Release())
					cp = nil
				}
			}
			canUnread = false
			requireByteCursor(t, data, off, tr.Cursor())
		}
	}
}

func TestByteReaderOptionsAndEmptyInput(t *testing.T) {
	tr := textreader.NewByteReader(bytes.NewReader(nil), nil, textreader.WithCapacity(1))
	_, _, err := tr.ReadRune()
	assert.ErrorIs(t, err, io.EOF)
	_, err = tr.PeekRune()
	assert.ErrorIs(t, err, io.EOF)
	requireByteCursor(t, nil, 0, tr.Cursor())
	assert.True(t, tr.Cursor().IsStart())
}

func TestByteReaderDoesNotWaitForMoreBytes(t *testing.T) {
	// A byte cursor on a stream, such as a socket, returns a byte as soon as
	// the source delivers it. It does not wait for the rest of a maximal
	// UTF-8 rune.
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	tr := textreader.NewByteReader(pr)

	type result struct {
		lr  textreader.LocatedRune
		err error
	}
	got := make(chan result, 1)

	for i, b := range []byte{0xF0, 0x0A} {
		go func() {
			lr, err := tr.PeekRune()
			if err == nil {
				lr, err = tr.ReadLocatedRune()
			}
			got <- result{lr, err}
		}()

		_, err := pw.Write([]byte{b})
		require.NoError(t, err)

		select {
		case res := <-got:
			require.NoError(t, res.err)
			assert.Equal(t, rune(b), res.lr.Rune())
			assert.Equal(t, i, res.lr.Pos().ByteOffset())
		case <-time.After(10 * time.Second):
			t.Fatalf("read of byte %d did not return after the source delivered it", i)
		}
	}
}
