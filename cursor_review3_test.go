package textreader_test

import (
	"errors"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/textreader"
)

var errSourceBoom = errors.New("source boom")

// dataErrReader returns data together with a non-EOF error, the way an
// io.Reader is allowed to.
type dataErrReader struct {
	done bool
}

func (r *dataErrReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}

	r.done = true

	return copy(p, "abcdefgh"), errSourceBoom
}

// TestBulkReadBudgetUsesActualAvailability covers a destination larger than the
// remaining budget: the budget must be judged against the bytes that would
// really advance the cursor, not against the caller's destination size.
func TestBulkReadBudgetUsesActualAvailability(t *testing.T) {
	tr := textreader.NewReader(
		strings.NewReader("a"),
		textreader.WithMaxRetained(1),
	)

	cp := tr.Checkpoint()
	defer func() { _ = cp.Release() }()

	buf := make([]byte, 4096)

	n, err := tr.Read(buf)
	require.NoError(t, err, "the one available byte fits a one-byte budget")
	assert.Equal(t, 1, n)
	assert.Equal(t, "a", string(buf[:n]))

	n, err = tr.Read(buf)
	require.ErrorIs(t, err, io.EOF, "the budget is spent and the source is drained")
	assert.Equal(t, 0, n)
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

// TestBulkReadKeepsSourceErrorAlongsideData covers the direct bulk-read path:
// an error returned with data must reach the caller, as io.Reader requires.
func TestBulkReadKeepsSourceErrorAlongsideData(t *testing.T) {
	tr := textreader.NewReader(&dataErrReader{}, textreader.WithCapacity(4))

	buf := make([]byte, 16)

	n, err := tr.Read(buf)
	require.ErrorIs(t, err, errSourceBoom)
	assert.Equal(t, 8, n)
	assert.Equal(t, "abcdefgh", string(buf[:n]))
}
