package textreader_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/textreader"
)

// TestRetentionBudgetEnforcedOnConsumption covers data buffered before a
// checkpoint: the limit must bound what the checkpoint makes the reader retain,
// not only what a later fill may read.
func TestRetentionBudgetEnforcedOnConsumption(t *testing.T) {
	tr := textreader.NewReader(
		strings.NewReader("abcdef"),
		textreader.WithCapacity(64),
		textreader.WithMaxRetained(1),
	)

	// Read-ahead happens before the checkpoint, so the bytes are already
	// buffered when the budget starts to apply.
	first, err := tr.ReadLocatedRune()
	require.NoError(t, err)
	require.Equal(t, 'a', first.Rune())

	cp := tr.Checkpoint()
	defer func() { _ = cp.Release() }()

	_, err = tr.ReadLocatedRune()
	require.NoError(t, err, "one retained byte is within a one-byte limit")
	assert.Equal(t, 1, tr.RetainedBytes())

	_, err = tr.ReadLocatedRune()
	require.ErrorIs(t, err, textreader.ErrRetentionExceeded)
	assert.Equal(t, 1, tr.RetainedBytes(), "the refused read must not move the cursor")
	assert.Equal(t, 2, tr.Cursor().ByteOffset(), "the cursor stays where the last accepted read left it")
}

// TestRetentionBudgetRejectsRuneLargerThanBudget covers a rune that cannot fit
// the remaining budget: it must be reported, not half-decoded.
func TestRetentionBudgetRejectsRuneLargerThanBudget(t *testing.T) {
	t.Run("byte source", func(t *testing.T) {
		tr := textreader.NewReader(
			strings.NewReader("€"),
			textreader.WithMaxRetained(2),
		)

		cp := tr.Checkpoint()
		defer func() { _ = cp.Release() }()

		_, err := tr.ReadLocatedRune()
		require.ErrorIs(t, err, textreader.ErrRetentionExceeded)
		assert.Equal(t, 0, tr.Cursor().ByteOffset(), "the cursor must not move")
		assert.Equal(t, 0, tr.Cursor().RuneOffset())
	})

	t.Run("rune source", func(t *testing.T) {
		tr := textreader.NewRuneReader(
			&runeOnlySource{runes: []rune("€")},
			textreader.WithMaxRetained(2),
		)

		cp := tr.Checkpoint()
		defer func() { _ = cp.Release() }()

		_, err := tr.ReadLocatedRune()
		require.ErrorIs(t, err, textreader.ErrRetentionExceeded)
		assert.Equal(t, 0, tr.Cursor().ByteOffset(), "the cursor must not move")
	})
}

// TestBulkReadBudgetIsAtomic covers the documented promise that an over-budget
// read performs no partial work.
func TestBulkReadBudgetIsAtomic(t *testing.T) {
	tr := textreader.NewReader(
		strings.NewReader("abcdef"),
		textreader.WithMaxRetained(4),
	)

	cp := tr.Checkpoint()
	defer func() { _ = cp.Release() }()

	buf := make([]byte, 5)

	n, err := tr.Read(buf)
	require.ErrorIs(t, err, textreader.ErrRetentionExceeded)
	assert.Equal(t, 0, n, "no partial work")
	assert.Equal(t, 0, tr.Cursor().ByteOffset(), "the cursor must not move")

	// A read that fits the budget still succeeds.
	small := make([]byte, 4)
	n, err = tr.Read(small)
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, "abcd", string(small))
}
