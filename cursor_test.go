package textreader_test

import (
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/textreader"
)

const sample = "αβγ\nhello 世界\n🦄tail"

func collectRunes(t *testing.T, tr *textreader.TextReader, n int) string {
	t.Helper()

	var sb strings.Builder
	for i := 0; i < n; i++ {
		lr, err := tr.ReadLocatedRune()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		sb.WriteRune(lr.Rune())
	}

	return sb.String()
}

// readToEnd consumes the reader and returns the text plus the last located
// rune seen.
func readToEnd(t *testing.T, tr *textreader.TextReader) (string, textreader.LocatedRune) {
	t.Helper()

	var (
		sb   strings.Builder
		last textreader.LocatedRune
	)
	for {
		lr, err := tr.ReadLocatedRune()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		sb.WriteRune(lr.Rune())
		last = lr
	}

	return sb.String(), last
}

func TestCursorPositions(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader(sample))

	// Start of the stream: line 1, column 0, nothing consumed.
	start := tr.Cursor()
	assert.Equal(t, 0, start.ByteOffset())
	assert.Equal(t, 0, start.RuneOffset())
	assert.Equal(t, 1, start.Line())
	assert.Equal(t, 0, start.Column())
	assert.True(t, start.IsStart())
	assert.Equal(t, "1:0", start.String())

	// 'α' is two bytes but one rune.
	lr, err := tr.ReadLocatedRune()
	require.NoError(t, err)
	assert.Equal(t, 'α', lr.Rune())
	assert.Equal(t, 2, lr.Size())
	assert.Equal(t, 0, lr.Pos().ByteOffset())
	assert.Equal(t, 0, lr.Pos().RuneOffset())
	assert.Equal(t, 0, lr.Pos().Column())

	end := lr.End()
	assert.Equal(t, 2, end.ByteOffset())
	assert.Equal(t, 1, end.RuneOffset())
	assert.Equal(t, 1, end.Line())
	assert.Equal(t, 1, end.Column())

	span := lr.Span()
	assert.Equal(t, 2, span.Bytes())
	assert.Equal(t, 1, span.Runes())
	assert.False(t, span.IsEmpty())
	assert.Equal(t, "1:0>1:1", span.String())
	assert.Equal(t, -1, span.Start().Compare(span.End()))
	assert.Equal(t, 0, span.Start().Compare(lr.Pos()))
	assert.Positive(t, end.Compare(span.Start()))

	// A located rune is a value: reading further does not change it.
	_ = collectRunes(t, tr, 3)
	assert.Equal(t, 0, lr.Pos().ByteOffset(), "located rune must not track later reads")
	assert.Equal(t, 'α', lr.Rune())
}

func TestCursorTracksNewlinesAndRunes(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("ab\ncd"))

	require.Equal(t, "ab", collectRunes(t, tr, 2))

	nl, err := tr.ReadLocatedRune()
	require.NoError(t, err)
	assert.Equal(t, '\n', nl.Rune())
	assert.Equal(t, 2, nl.Pos().ByteOffset())
	assert.Equal(t, 2, nl.Pos().RuneOffset())
	assert.Equal(t, 1, nl.Pos().Line())
	assert.Equal(t, 2, nl.Pos().Column())

	end := nl.End()
	assert.Equal(t, 2, end.Line())
	assert.Equal(t, 0, end.Column())

	require.Equal(t, "cd", collectRunes(t, tr, 2))

	done := tr.Cursor()
	assert.Equal(t, 5, done.ByteOffset(), "byte offset counts bytes")
	assert.Equal(t, 5, done.RuneOffset())
	assert.Equal(t, 2, done.Line())
	assert.Equal(t, 2, done.Column())
}

func TestCheckpointReplayIsExact(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader(sample))

	// Read a little, then mark.
	require.Equal(t, "αβ", collectRunes(t, tr, 2))
	mark := tr.Cursor()

	cp := tr.Checkpoint()
	assert.True(t, cp.Active())
	assert.Equal(t, mark, cp.Pos())

	// Speculatively read everything to EOF.
	replay, _ := readToEnd(t, tr)
	require.Equal(t, "γ\nhello 世界\n🦄tail", replay)

	// The mark still sits where it was: the checkpoint did not move.
	assert.Equal(t, mark, cp.Pos())

	// Reset restores the complete position and the same runes come back.
	require.NoError(t, cp.Reset())
	assert.Equal(t, mark, tr.Cursor())

	again, _ := readToEnd(t, tr)
	assert.Equal(t, replay, again, "replay must reproduce Unicode, newlines and EOF exactly")

	_, err := tr.ReadLocatedRune()
	assert.ErrorIs(t, err, io.EOF)

	// Reset is repeatable while the checkpoint is live.
	require.NoError(t, cp.Reset())
	third, _ := readToEnd(t, tr)
	assert.Equal(t, replay, third)
}

func TestCheckpointNesting(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("0123456789"))

	outer := tr.Checkpoint()
	require.Equal(t, "012", collectRunes(t, tr, 3))

	inner := tr.Checkpoint()
	require.Equal(t, "345", collectRunes(t, tr, 3))

	require.NoError(t, inner.Reset())
	assert.Equal(t, 3, tr.Cursor().ByteOffset())
	assert.Equal(t, "345", collectRunes(t, tr, 3))

	// The outer checkpoint still replays the whole range.
	require.NoError(t, outer.Reset())
	assert.Equal(t, 0, tr.Cursor().ByteOffset())
	assert.Equal(t, "0123456", collectRunes(t, tr, 7))

	// Releasing the inner checkpoint must not invalidate the outer one.
	require.NoError(t, inner.Release())
	assert.False(t, inner.Active())
	require.NoError(t, outer.Reset())
	assert.Equal(t, "0123456789", collectRunes(t, tr, 10))
}

func TestCheckpointCommitAndRelease(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("abcdef"))

	cp := tr.Checkpoint()
	require.Equal(t, "abcdef", collectRunes(t, tr, 6))
	assert.Positive(t, tr.RetainedBytes())
	require.NoError(t, cp.Commit())
	assert.False(t, cp.Active())
	assert.Equal(t, 0, tr.RetainedBytes(), "commit drops the retained input")

	// A consumed checkpoint is done for good.
	assert.ErrorIs(t, cp.Reset(), textreader.ErrCheckpointReleased)
	assert.ErrorIs(t, cp.Release(), textreader.ErrCheckpointReleased)
	assert.ErrorIs(t, cp.Commit(), textreader.ErrCheckpointReleased)

	// Commit refuses while the cursor is behind the mark; Release does not.
	tr2 := textreader.NewReader(strings.NewReader("abcdef"))
	require.Equal(t, "abc", collectRunes(t, tr2, 3))
	cp2 := tr2.Checkpoint()
	require.Equal(t, "def", collectRunes(t, tr2, 3))

	_, err := tr2.Seek(1, io.SeekStart)
	require.NoError(t, err)

	assert.ErrorIs(t, cp2.Commit(), textreader.ErrCheckpointRewound)
	require.NoError(t, cp2.Release())
	assert.False(t, cp2.Active())

	// Resetting back to the mark restores the position exactly, and committing
	// from there is accepted.
	tr3 := textreader.NewReader(strings.NewReader("abcdef"))
	cp3 := tr3.Checkpoint()
	require.Equal(t, "abc", collectRunes(t, tr3, 3))
	require.NoError(t, cp3.Reset())
	assert.Equal(t, 0, tr3.Cursor().ByteOffset())
	require.NoError(t, cp3.Commit())
}

func TestRetentionLimitIsExplicit(t *testing.T) {
	const input = "abcdefghijklmnopqrstuvwxyz"

	tr := textreader.NewReader(
		strings.NewReader(input),
		textreader.WithCapacity(8),
		textreader.WithMaxRetained(16),
	)

	cp := tr.Checkpoint()

	read := 0
	var err error
	for {
		_, err = tr.ReadLocatedRune()
		if err != nil {
			break
		}
		read++
	}

	require.ErrorIs(t, err, textreader.ErrRetentionExceeded)
	assert.Positive(t, read)
	assert.LessOrEqual(t, read, 16, "the reader must never retain more than the limit")
	assert.Equal(t, read, tr.Cursor().ByteOffset())
	assert.LessOrEqual(t, tr.RetainedBytes(), 16)

	// The failing read consumed nothing, and the checkpoint still replays.
	require.NoError(t, cp.Reset())
	assert.Equal(t, 0, tr.Cursor().ByteOffset())

	replayed, rerr := readAllLocated(t, tr)
	require.ErrorIs(t, rerr, textreader.ErrRetentionExceeded)
	assert.Equal(t, input[:read], replayed, "replay reproduces exactly the committed range")
}

// readAllLocated consumes the reader and reports the text plus the error that
// ended it.
func readAllLocated(t *testing.T, tr *textreader.TextReader) (string, error) {
	t.Helper()

	var sb strings.Builder
	for {
		lr, err := tr.ReadLocatedRune()
		if err != nil {
			return sb.String(), err
		}
		sb.WriteRune(lr.Rune())
	}
}

func TestRetentionLimitWithoutCheckpoint(t *testing.T) {
	tr := textreader.NewReader(
		strings.NewReader(strings.Repeat("x", 4096)),
		textreader.WithCapacity(8),
		textreader.WithMaxRetained(8),
	)

	// No checkpoint is active: the limit never applies and reads stream through.
	got, err := io.ReadAll(tr)
	require.NoError(t, err)
	assert.Len(t, got, 4096)
	assert.Equal(t, 0, tr.RetainedBytes())
}

func TestSpanTextAndContextAreNonMutating(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("hello 世界\n🦄 tail"))

	cp := tr.Checkpoint()
	defer func() { _ = cp.Release() }()

	first, err := tr.ReadLocatedRune()
	require.NoError(t, err)

	text, last := readToEnd(t, tr)
	text = string(first.Rune()) + text
	whole := textreader.NewSpan(first.Pos(), last.End())

	before := tr.Cursor()

	got, err := tr.SpanText(whole)
	require.NoError(t, err)
	assert.Equal(t, "hello 世界\n🦄 tail", got)

	ctx, err := tr.Context(whole, 2, 2)
	require.NoError(t, err)
	assert.Equal(t, got, ctx)

	assert.Equal(t, before, tr.Cursor(), "text and context access must not move the cursor")

	// Context is clamped to the retained window rather than erroring.
	narrow := textreader.NewSpan(first.Pos(), first.End())
	ctx, err = tr.Context(narrow, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, "h", ctx)

	// Negative widths are rejected.
	_, err = tr.Context(whole, -1, 0)
	require.Error(t, err)
}

func TestSpanTextOutsideRetainedInput(t *testing.T) {
	tr := textreader.NewReader(
		strings.NewReader(strings.Repeat("abcdefghij", 40)),
		textreader.WithCapacity(8),
	)

	first, err := tr.ReadLocatedRune()
	require.NoError(t, err)
	stale := textreader.NewSpan(first.Pos(), first.End())

	// Consume the stream with no checkpoint held, so the head is compacted away.
	_, err = io.ReadAll(tr)
	require.NoError(t, err)

	_, err = tr.SpanText(stale)
	assert.ErrorIs(t, err, textreader.ErrPositionOutOfBuffer)

	_, err = tr.Context(stale, 1, 1)
	assert.ErrorIs(t, err, textreader.ErrPositionOutOfBuffer)
}

func TestSpanTextRejectsReversedSpan(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("abcdef"))
	cp := tr.Checkpoint()
	defer func() { _ = cp.Release() }()

	require.Equal(t, "abcd", collectRunes(t, tr, 4))
	late := tr.Cursor()
	early := cp.Pos()

	reversed := textreader.NewSpan(late, early)

	_, err := tr.SpanText(reversed)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "precedes")

	_, err = tr.Context(reversed, 1, 1)
	require.Error(t, err)
}

func TestPeekRuneDoesNotConsume(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("世x"))

	before := tr.Cursor()
	peeked, err := tr.PeekRune()
	require.NoError(t, err)
	assert.Equal(t, '世', peeked.Rune())
	assert.Equal(t, 3, peeked.Size())
	assert.Equal(t, before, tr.Cursor(), "peek must not move the cursor")

	read, err := tr.ReadLocatedRune()
	require.NoError(t, err)
	assert.Equal(t, peeked, read)

	// Peek at EOF reports EOF and leaves the cursor alone.
	require.Equal(t, "x", collectRunes(t, tr, 1))
	atEnd := tr.Cursor()
	_, err = tr.PeekRune()
	assert.ErrorIs(t, err, io.EOF)
	assert.Equal(t, atEnd, tr.Cursor())
}

func TestReaderErrorReplaysDeterministically(t *testing.T) {
	boom := errors.New("boom")
	src := &erroringReader{data: []byte("abc"), err: boom}

	tr := textreader.NewReader(src)

	cp := tr.Checkpoint()
	assert.Equal(t, "abc", collectRunes(t, tr, 3))

	_, err := tr.ReadLocatedRune()
	require.ErrorIs(t, err, boom)

	// Reset replays the buffered runes and then the same error, rather than
	// whatever the source would report on a second pass.
	require.NoError(t, cp.Reset())
	assert.Equal(t, "abc", collectRunes(t, tr, 3))

	_, err = tr.ReadLocatedRune()
	require.ErrorIs(t, err, boom)
}

func TestNewRuneReaderTracksPositions(t *testing.T) {
	src := &runeOnlySource{runes: []rune("aβ\n🦄")}

	tr := textreader.NewRuneReader(src)

	assert.Equal(t, "aβ", collectRunes(t, tr, 2))

	nl, err := tr.ReadLocatedRune()
	require.NoError(t, err)
	assert.Equal(t, '\n', nl.Rune())
	assert.Equal(t, 3, nl.Pos().ByteOffset())
	assert.Equal(t, 2, nl.Pos().RuneOffset())
	assert.Equal(t, 1, nl.Pos().Line())

	emoji, err := tr.ReadLocatedRune()
	require.NoError(t, err)
	assert.Equal(t, '🦄', emoji.Rune())
	assert.Equal(t, 4, emoji.Pos().ByteOffset())
	assert.Equal(t, 2, emoji.Pos().Line())
	assert.Equal(t, 0, emoji.Pos().Column())

	_, err = tr.ReadLocatedRune()
	assert.ErrorIs(t, err, io.EOF)

	end := tr.Cursor()
	assert.Equal(t, 8, end.ByteOffset())
	assert.Equal(t, 4, end.RuneOffset())
	assert.Equal(t, 2, end.Line())
	assert.Equal(t, 1, end.Column())
}

func TestRuneReaderCheckpointReplay(t *testing.T) {
	src := &runeOnlySource{runes: []rune("hello 世界")}
	tr := textreader.NewRuneReader(src)

	cp := tr.Checkpoint()
	require.Equal(t, "hello 世界", collectRunes(t, tr, 64))

	require.NoError(t, cp.Reset())
	assert.Equal(t, "hello 世界", collectRunes(t, tr, 64))
}

func TestRuneReaderErrorReplays(t *testing.T) {
	boom := errors.New("rune source failed")
	src := &failingRuneSource{runes: []rune("ab"), err: boom}

	tr := textreader.NewRuneReader(src)
	cp := tr.Checkpoint()

	assert.Equal(t, "ab", collectRunes(t, tr, 2))

	_, err := tr.ReadLocatedRune()
	require.ErrorIs(t, err, boom)

	require.NoError(t, cp.Reset())
	assert.Equal(t, "ab", collectRunes(t, tr, 2))

	_, err = tr.ReadLocatedRune()
	require.ErrorIs(t, err, boom)
}

func TestConcurrentCursorUse(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader(strings.Repeat(sample, 32)))

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				cp := tr.Checkpoint()
				_, _ = tr.ReadLocatedRune()
				_ = tr.Cursor()
				_ = cp.Active()
				_, _ = tr.PeekRune()
				_ = tr.RetainedBytes()
				_ = cp.Reset()
				_ = cp.Active()

				now := tr.Cursor()
				_, _ = tr.SpanText(textreader.NewSpan(now, now))

				_ = cp.Release()
			}
		}()
	}
	wg.Wait()

	// The reader is still usable and positionally sane afterwards.
	got, err := io.ReadAll(tr)
	require.NoError(t, err)
	assert.NotNil(t, got)
}

func TestLongReadAheadGrowsRetainedBuffer(t *testing.T) {
	const size = 200_000
	input := strings.Repeat("a", size)

	tr := textreader.NewReader(strings.NewReader(input), textreader.WithCapacity(64))

	cp := tr.Checkpoint()

	read := 0
	for {
		_, err := tr.ReadLocatedRune()
		if err != nil {
			break
		}
		read++
	}

	require.Equal(t, size, read)
	assert.Equal(t, size, tr.RetainedBytes())

	require.NoError(t, cp.Reset())
	assert.Equal(t, 0, tr.Cursor().ByteOffset())
	assert.Len(t, collectRunes(t, tr, size), size)
}

func TestSpanInvalidatedWhenRetentionEnds(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("abcdef"))

	cp := tr.Checkpoint()

	first, err := tr.ReadLocatedRune()
	require.NoError(t, err)

	last, err := tr.ReadLocatedRune()
	require.NoError(t, err)

	span := textreader.NewSpan(first.Pos(), last.End())

	got, err := tr.SpanText(span)
	require.NoError(t, err)
	assert.Equal(t, "ab", got)

	// Committing the checkpoint accepts the current position and ends
	// retention, so the span stops being recoverable — deterministically, even
	// though its bytes have not been compacted away yet.
	require.NoError(t, cp.Commit())

	_, err = tr.SpanText(span)
	assert.ErrorIs(t, err, textreader.ErrPositionOutOfBuffer)

	_, err = tr.Context(span, 1, 1)
	assert.ErrorIs(t, err, textreader.ErrPositionOutOfBuffer)

	// A new checkpoint does not resurrect the old range.
	cp2 := tr.Checkpoint()
	defer func() { _ = cp2.Release() }()

	_, err = tr.SpanText(span)
	assert.ErrorIs(t, err, textreader.ErrPositionOutOfBuffer)
}

func TestSeekForwardAcrossGrownBuffer(t *testing.T) {
	text := strings.Repeat("abc\n", 200)
	tr := textreader.NewReader(strings.NewReader(text), textreader.WithCapacity(16))

	cp := tr.Checkpoint()

	_, err := io.ReadAll(tr)
	require.NoError(t, err)

	require.NoError(t, cp.Reset())
	assert.Equal(t, 0, tr.Cursor().ByteOffset())

	// The buffer grew past the initial capacity while the checkpoint retained
	// the range, so a forward seek over more than `capacity` bytes is legal.
	off, err := tr.Seek(int64(len(text)), io.SeekCurrent)
	require.NoError(t, err)
	assert.Equal(t, int64(len(text)), off)
	assert.Equal(t, len(text), tr.Cursor().ByteOffset())

	_, err = tr.ReadLocatedRune()
	assert.ErrorIs(t, err, io.EOF)
}

// erroringReader yields its data and then a permanent error.
type erroringReader struct {
	data []byte
	err  error
	off  int
}

func (e *erroringReader) Read(p []byte) (int, error) {
	if e.off < len(e.data) {
		n := copy(p, e.data[e.off:])
		e.off += n
		return n, nil
	}

	return 0, e.err
}

// runeOnlySource implements io.RuneReader and nothing else, like the rune
// readers existing textlexer callers pass in.
type runeOnlySource struct {
	runes []rune
	pos   int
}

func (s *runeOnlySource) ReadRune() (rune, int, error) {
	if s.pos >= len(s.runes) {
		return 0, 0, io.EOF
	}

	r := s.runes[s.pos]
	s.pos++

	return r, len(string(r)), nil
}

// failingRuneSource yields its runes and then a permanent error.
type failingRuneSource struct {
	runes []rune
	err   error
	pos   int
}

func (s *failingRuneSource) ReadRune() (rune, int, error) {
	if s.pos >= len(s.runes) {
		return 0, 0, s.err
	}

	r := s.runes[s.pos]
	s.pos++

	return r, len(string(r)), nil
}
