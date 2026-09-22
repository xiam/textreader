package textreader_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/textreader"
)

// drainLocated consumes the reader and returns the text it produced.
func drainLocated(t *testing.T, tr *textreader.TextReader) (string, error) {
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

// TestRuneReaderKeepsRuneAtBufferBoundary covers a rune source whose next rune
// does not fit the remaining buffer room.
func TestRuneReaderKeepsRuneAtBufferBoundary(t *testing.T) {
	tr := textreader.NewRuneReader(
		&runeOnlySource{runes: []rune("a🦄b")},
		textreader.WithCapacity(4),
	)

	got, err := drainLocated(t, tr)
	require.ErrorIs(t, err, io.EOF)
	assert.Equal(t, "a🦄b", got)
}

// TestRetentionLimitAllowsRuneThatFits covers a retention limit that is large
// enough for the next rune but smaller than a maximal rune.
func TestRetentionLimitAllowsRuneThatFits(t *testing.T) {
	t.Run("byte source", func(t *testing.T) {
		tr := textreader.NewReader(
			strings.NewReader("ab"),
			textreader.WithCapacity(4),
			textreader.WithMaxRetained(1),
		)

		cp := tr.Checkpoint()
		defer func() { _ = cp.Release() }()

		lr, err := tr.ReadLocatedRune()
		require.NoError(t, err)
		assert.Equal(t, 'a', lr.Rune())

		_, err = tr.ReadLocatedRune()
		assert.ErrorIs(t, err, textreader.ErrRetentionExceeded)
	})

	t.Run("rune source", func(t *testing.T) {
		tr := textreader.NewRuneReader(
			&runeOnlySource{runes: []rune("ab")},
			textreader.WithCapacity(4),
			textreader.WithMaxRetained(1),
		)

		cp := tr.Checkpoint()
		defer func() { _ = cp.Release() }()

		lr, err := tr.ReadLocatedRune()
		require.NoError(t, err)
		assert.Equal(t, 'a', lr.Rune())

		_, err = tr.ReadLocatedRune()
		assert.ErrorIs(t, err, textreader.ErrRetentionExceeded)
	})
}

// TestContextStopsAtRetentionFloor covers context extension reaching below the
// retention floor once the checkpoint that covered those bytes is gone.
func TestContextStopsAtRetentionFloor(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("abcdef"))

	cp := tr.Checkpoint()

	require.Equal(t, "ab", collectRunes(t, tr, 2))

	// Commit accepts the current position: bytes 0..2 are no longer retained.
	require.NoError(t, cp.Commit())

	now := tr.Cursor()
	empty := textreader.NewSpan(now, now)

	ctx, err := tr.Context(empty, 2, 0)
	require.NoError(t, err)
	assert.Equal(t, "", ctx, "context must not reach below the retention floor")
}

// TestReadSplittingRuneKeepsCoordinates covers byte reads that split a
// multi-byte rune across calls.
func TestReadSplittingRuneKeepsCoordinates(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("é"))

	buf := make([]byte, 1)

	n, err := tr.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	n, err = tr.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	cur := tr.Cursor()
	assert.Equal(t, 2, cur.ByteOffset(), "byte offset counts bytes")
	assert.Equal(t, 1, cur.RuneOffset(), "one rune was read")
	assert.Equal(t, 1, cur.Column(), "one rune column was read")

	_, err = tr.Read(buf)
	assert.True(t, errors.Is(err, io.EOF), "want EOF, got %v", err)
}
