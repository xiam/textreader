package position_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/textreader/position"
)

func TestPosition(t *testing.T) {
	{
		pos := position.New()

		pos.Scan([]byte("Hello, World!\n"))

		assert.Equal(t, 14, pos.Offset())
		assert.Equal(t, 0, pos.Column())
		assert.Equal(t, 2, pos.Line())

		err := pos.Rewind(0)
		require.NoError(t, err)

		assert.Equal(t, 14, pos.Offset())
		assert.Equal(t, 0, pos.Column())
		assert.Equal(t, 2, pos.Line())

		err = pos.Rewind(1)
		require.NoError(t, err)

		assert.Equal(t, 13, pos.Offset())
		assert.Equal(t, 13, pos.Column())
		assert.Equal(t, 1, pos.Line())

		err = pos.Rewind(5)
		require.NoError(t, err)

		assert.Equal(t, 8, pos.Offset())
		assert.Equal(t, 8, pos.Column())
		assert.Equal(t, 1, pos.Line())

		err = pos.Rewind(100)
		require.Error(t, err)

		assert.Equal(t, 8, pos.Offset())
		assert.Equal(t, 8, pos.Column())
		assert.Equal(t, 1, pos.Line())

		err = pos.Rewind(pos.Offset())
		require.NoError(t, err)

		assert.Equal(t, 0, pos.Offset())
		assert.Equal(t, 0, pos.Column())
		assert.Equal(t, 1, pos.Line())
	}

	{
		pos := position.New()

		pos.Scan([]byte("Hello\nDarkness\nMy\nOld\nFriend\n"))

		assert.Equal(t, 29, pos.Offset())
		assert.Equal(t, 0, pos.Column())
		assert.Equal(t, 6, pos.Line())

		err := pos.Rewind(1)
		require.NoError(t, err)

		assert.Equal(t, 28, pos.Offset())
		assert.Equal(t, 6, pos.Column())
		assert.Equal(t, 5, pos.Line())

		err = pos.Rewind(15)
		require.NoError(t, err)

		assert.Equal(t, 13, pos.Offset())
		assert.Equal(t, 7, pos.Column())
		assert.Equal(t, 2, pos.Line())
	}

	{
		pos := position.New()

		pos.Scan([]byte("Hello\nDarkness\nMy\nOld\nFriend\n"))

		assert.Equal(t, 29, pos.Offset())
		assert.Equal(t, 0, pos.Column())
		assert.Equal(t, 6, pos.Line())

		err := pos.Rewind(29)
		require.NoError(t, err)

		assert.Equal(t, 0, pos.Offset())
		assert.Equal(t, 0, pos.Column())
		assert.Equal(t, 1, pos.Line())

		err = pos.Rewind(1)
		require.Error(t, err)

		assert.Equal(t, 0, pos.Offset())
		assert.Equal(t, 0, pos.Column())
		assert.Equal(t, 1, pos.Line())
	}

	{
		pos := position.New()

		pos.Scan([]byte("Hello\nDarkness\nMy\nOld\nFriend\n"))

		assert.Equal(t, 29, pos.Offset())
		assert.Equal(t, 0, pos.Column())
		assert.Equal(t, 6, pos.Line())

		err := pos.Rewind(28)
		require.NoError(t, err)

		assert.Equal(t, 1, pos.Offset())
		assert.Equal(t, 1, pos.Column())
		assert.Equal(t, 1, pos.Line())

		err = pos.Rewind(-1)
		require.Error(t, err)
	}

	{
		pos := position.New()

		line := "🦄\n"

		pos.Scan([]byte(line))
		assert.Equal(t, len(line), pos.Offset())
		assert.Equal(t, 0, pos.Column())
		assert.Equal(t, 2, pos.Line())
	}

	{
		pos := position.New()

		line := "first line\nsecond line\nthird line\nfourth line\nfifth line 🦄\n"
		pos.Scan([]byte(line))
		assert.Equal(t, len(line), pos.Offset())
		assert.Equal(t, 0, pos.Column())
		assert.Equal(t, 6, pos.Line())
	}
}
