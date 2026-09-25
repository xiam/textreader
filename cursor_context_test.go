package textreader_test

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/textreader"
)

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

// TestContextWidthDoesNotOverflow covers a valid but very large context width.
// The retained text is held by a checkpoint, so both edges are clamped by
// comparison instead of overflowing.
func TestContextWidthDoesNotOverflow(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("abc"))

	cp := tr.Checkpoint()
	defer func() { _ = cp.Release() }()

	first, err := tr.ReadLocatedRune()
	require.NoError(t, err)
	require.Equal(t, 'a', first.Rune())

	ctx, err := tr.Context(first.Span(), 0, math.MaxInt)
	require.NoError(t, err)
	assert.Equal(t, "abc", ctx, "the right edge clamps to the buffered end")

	ctx, err = tr.Context(first.Span(), math.MaxInt, math.MaxInt)
	require.NoError(t, err)
	assert.Equal(t, "abc", ctx, "the left edge clamps to the retention floor")
}
