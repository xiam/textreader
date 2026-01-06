package position_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/textreader/position"
)

func TestColumnCountsRunes(t *testing.T) {
	t.Run("emoji", func(t *testing.T) {
		pos := position.New()
		// 🦄 is 4 bytes but 1 rune
		pos.Scan([]byte("🦄"))
		assert.Equal(t, 1, pos.Column(), "emoji should count as 1 column")
		assert.Equal(t, 4, pos.Offset(), "offset should count bytes")
	})

	t.Run("multiple emojis", func(t *testing.T) {
		pos := position.New()
		// 🎉🚀🌟 = 3 emojis, 12 bytes
		pos.Scan([]byte("🎉🚀🌟"))
		assert.Equal(t, 3, pos.Column(), "3 emojis should count as 3 columns")
		assert.Equal(t, 12, pos.Offset(), "offset should be 12 bytes")
	})

	t.Run("CJK characters", func(t *testing.T) {
		pos := position.New()
		// 日本語 = 3 characters, 9 bytes (3 bytes each)
		pos.Scan([]byte("日本語"))
		assert.Equal(t, 3, pos.Column(), "3 CJK chars should count as 3 columns")
		assert.Equal(t, 9, pos.Offset(), "offset should be 9 bytes")
	})

	t.Run("mixed ASCII and emoji", func(t *testing.T) {
		pos := position.New()
		// "hello 🌍!" = 8 runes (6 ASCII + 1 emoji + 1 ASCII), 11 bytes
		pos.Scan([]byte("hello 🌍!"))
		assert.Equal(t, 8, pos.Column(), "mixed content should count runes")
		assert.Equal(t, 11, pos.Offset(), "offset should count bytes")
	})

	t.Run("mixed ASCII and CJK", func(t *testing.T) {
		pos := position.New()
		// "abc中文def" = 8 runes (3 + 2 + 3), 12 bytes (3 + 6 + 3)
		pos.Scan([]byte("abc中文def"))
		assert.Equal(t, 8, pos.Column())
		assert.Equal(t, 12, pos.Offset())
	})

	t.Run("multiline with emoji", func(t *testing.T) {
		pos := position.New()
		// Line 1: "hello🌍" = 6 runes, 9 bytes
		// Line 2: "世界" = 2 runes, 6 bytes
		pos.Scan([]byte("hello🌍\n世界"))
		assert.Equal(t, 2, pos.Line())
		assert.Equal(t, 2, pos.Column(), "second line should have 2 CJK chars")
		assert.Equal(t, 16, pos.Offset(), "total bytes")
	})

	t.Run("rewind with multibyte characters", func(t *testing.T) {
		pos := position.New()
		// "café" where é is 2 bytes (U+00E9)
		pos.Scan([]byte("café"))
		assert.Equal(t, 4, pos.Column(), "café is 4 runes")
		assert.Equal(t, 5, pos.Offset(), "café is 5 bytes")

		// Rewind the é (2 bytes, 1 rune)
		err := pos.Rewind(2, 1)
		require.NoError(t, err)
		assert.Equal(t, 3, pos.Column(), "after rewind should be 3 runes")
		assert.Equal(t, 3, pos.Offset(), "after rewind should be 3 bytes")
	})

	t.Run("rewind across newline with emoji", func(t *testing.T) {
		pos := position.New()
		// "🎉\n🚀" = emoji, newline, emoji
		pos.Scan([]byte("🎉\n🚀"))
		assert.Equal(t, 2, pos.Line())
		assert.Equal(t, 1, pos.Column())
		assert.Equal(t, 9, pos.Offset())

		// Rewind 5 bytes (🚀=4 + \n=1), 2 runes
		err := pos.Rewind(5, 2)
		require.NoError(t, err)
		assert.Equal(t, 1, pos.Line())
		assert.Equal(t, 1, pos.Column(), "back to first emoji")
		assert.Equal(t, 4, pos.Offset())
	})
}

func TestPosition(t *testing.T) {
	{
		pos := position.New()

		pos.Scan([]byte("Hello, World!\n"))

		assert.Equal(t, 14, pos.Offset())
		assert.Equal(t, 0, pos.Column())
		assert.Equal(t, 2, pos.Line())

		err := pos.Rewind(0, 0)
		require.NoError(t, err)

		assert.Equal(t, 14, pos.Offset())
		assert.Equal(t, 0, pos.Column())
		assert.Equal(t, 2, pos.Line())

		err = pos.Rewind(1, 1) // rewind newline
		require.NoError(t, err)

		assert.Equal(t, 13, pos.Offset())
		assert.Equal(t, 13, pos.Column()) // "Hello, World!" = 13 chars
		assert.Equal(t, 1, pos.Line())

		err = pos.Rewind(5, 5)
		require.NoError(t, err)

		assert.Equal(t, 8, pos.Offset())
		assert.Equal(t, 8, pos.Column())
		assert.Equal(t, 1, pos.Line())

		err = pos.Rewind(100, 100)
		require.Error(t, err)

		assert.Equal(t, 8, pos.Offset())
		assert.Equal(t, 8, pos.Column())
		assert.Equal(t, 1, pos.Line())

		err = pos.Rewind(pos.Offset(), pos.Column()) // rewind all remaining
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

		err := pos.Rewind(1, 1) // rewind newline after "Friend"
		require.NoError(t, err)

		assert.Equal(t, 28, pos.Offset())
		assert.Equal(t, 6, pos.Column()) // "Friend" = 6 chars
		assert.Equal(t, 5, pos.Line())

		err = pos.Rewind(15, 15)
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

		err := pos.Rewind(29, 29)
		require.NoError(t, err)

		assert.Equal(t, 0, pos.Offset())
		assert.Equal(t, 0, pos.Column())
		assert.Equal(t, 1, pos.Line())

		err = pos.Rewind(1, 1)
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

		err := pos.Rewind(28, 28)
		require.NoError(t, err)

		assert.Equal(t, 1, pos.Offset())
		assert.Equal(t, 1, pos.Column())
		assert.Equal(t, 1, pos.Line())

		err = pos.Rewind(-1, -1)
		require.Error(t, err)
	}

	{
		pos := position.New()

		// Test that Column counts runes, not bytes
		// 🦄 is 4 bytes but 1 rune
		pos.Scan([]byte("🦄"))
		assert.Equal(t, 4, pos.Offset())  // 4 bytes
		assert.Equal(t, 1, pos.Column())  // 1 rune (not 4!)
		assert.Equal(t, 1, pos.Line())

		pos.Scan([]byte("\n"))
		assert.Equal(t, 5, pos.Offset())
		assert.Equal(t, 0, pos.Column())
		assert.Equal(t, 2, pos.Line())
	}

	{
		pos := position.New()

		// "fifth line 🦄" = 11 chars + 1 emoji = 12 runes (but 14 bytes)
		line := "first line\nsecond line\nthird line\nfourth line\nfifth line 🦄\n"
		pos.Scan([]byte(line))
		assert.Equal(t, len(line), pos.Offset()) // byte count
		assert.Equal(t, 0, pos.Column())         // after newline
		assert.Equal(t, 6, pos.Line())

		// Rewind the newline to check column of "fifth line 🦄"
		err := pos.Rewind(1, 1)
		require.NoError(t, err)
		assert.Equal(t, 12, pos.Column()) // 12 runes, not 14 bytes
	}
}

// TestCopySafety verifies that Copy() returns a *Position with a fresh mutex,
// which is safe for concurrent use.
func TestCopySafety(t *testing.T) {
	t.Run("copy returns independent position", func(t *testing.T) {
		pos := position.New()
		pos.Scan([]byte("hello\nworld"))

		// Copy returns *Position with fresh mutex
		copied := pos.Copy()

		// Both should have same state
		assert.Equal(t, pos.Line(), copied.Line())
		assert.Equal(t, pos.Column(), copied.Column())
		assert.Equal(t, pos.Offset(), copied.Offset())

		// Modify original - copy should be unaffected
		pos.Scan([]byte("more"))
		assert.Equal(t, 11, copied.Offset(), "copy should be unaffected by original modification")
	})

	t.Run("concurrent use of original and copy is safe", func(t *testing.T) {
		pos := position.New()
		pos.Scan([]byte("test data"))

		// Copy has its own fresh mutex - safe for concurrent use
		copied := pos.Copy()

		var wg sync.WaitGroup

		// Use both original and copy concurrently
		for i := 0; i < 100; i++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				pos.Scan([]byte("x"))
				_ = pos.Line()
			}()
			go func() {
				defer wg.Done()
				_ = copied.Line()
				_ = copied.Column()
			}()
		}

		wg.Wait()
	})
}
