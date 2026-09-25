package textreader_test

import (
	"bufio"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/textreader"
)

// TestPeekAgreesWithNextReadAfterSplitByteRead covers the position a peek
// reports when a byte read left an incomplete UTF-8 sequence pending: the peek
// and the read that follows it must describe the same located rune.
func TestPeekAgreesWithNextReadAfterSplitByteRead(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("é"))

	// Read only the first byte of the two-byte rune.
	buf := make([]byte, 1)

	n, err := tr.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, byte(0xC3), buf[0])

	peeked, err := tr.PeekRune()
	require.NoError(t, err)

	read, err := tr.ReadLocatedRune()
	require.NoError(t, err)

	assert.Equal(t, read.Rune(), peeked.Rune(), "the peek must report the rune the read returns")
	assert.Equal(t, read.Size(), peeked.Size())

	assert.Equal(t, read.Pos().ByteOffset(), peeked.Pos().ByteOffset())
	assert.Equal(t, read.Pos().RuneOffset(), peeked.Pos().RuneOffset())
	assert.Equal(t, read.Pos().Column(), peeked.Pos().Column())
	assert.Equal(t, read.Pos().Line(), peeked.Pos().Line())
}

// TestPeekEndsUnreadAuthority covers the single-level unread contract: peeking
// is not a read, so it must not leave an earlier ReadRune available to undo.
func TestPeekEndsUnreadAuthority(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("ab"))

	r, _, err := tr.ReadRune()
	require.NoError(t, err)
	require.Equal(t, 'a', r)

	_, err = tr.PeekRune()
	require.NoError(t, err)

	err = tr.UnreadRune()
	require.ErrorIs(t, err, bufio.ErrInvalidUnreadRune)

	r, _, err = tr.ReadRune()
	require.NoError(t, err)
	assert.Equal(t, 'b', r, "the peek must not rewind the cursor")
}

// TestNonConsumingCallsDoNotMoveTheCursor covers the logical cursor across
// operations that do not consume input. Reading one byte of a two-byte rune
// leaves a partial sequence pending; peeking, marking, and a zero relative seek
// must each leave the cursor exactly where it was, and the mark must record that
// same position.
func TestNonConsumingCallsDoNotMoveTheCursor(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("éx"))

	buf := make([]byte, 1)

	n, err := tr.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	before := tr.Cursor()

	peeked, err := tr.PeekRune()
	require.NoError(t, err)
	assert.Equal(t, before, tr.Cursor(), "peeking must not move the cursor")

	cp := tr.Checkpoint()
	defer func() { _ = cp.Release() }()
	assert.Equal(t, before, tr.Cursor(), "marking must not move the cursor")
	assert.Equal(t, before, cp.Pos(), "the mark must record the cursor it was taken at")

	off, err := tr.Seek(0, io.SeekCurrent)
	require.NoError(t, err)
	assert.Equal(t, int64(before.ByteOffset()), off)
	assert.Equal(t, before, tr.Cursor(), "a zero relative seek must not move the cursor")

	// The peek still describes the location the next read returns, even though
	// neither of them moved the stored cursor.
	read, err := tr.ReadLocatedRune()
	require.NoError(t, err)
	assert.Equal(t, peeked.Pos(), read.Pos())
}
