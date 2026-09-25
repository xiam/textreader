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
