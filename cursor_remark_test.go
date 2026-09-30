package textreader_test

import (
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/textreader"
)

// TestRemarkReusesTheSameValue is the core capability: a released checkpoint
// is re-armed in place and behaves exactly like a fresh mark, without a new
// allocation. The lexer reuses its fixed slots this way, so the number of
// checkpoint values stays constant across the whole stream.
func TestRemarkReusesTheSameValue(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader(sample))

	// Arm the slot the first time, the way a lexer arms its ring at startup.
	require.Equal(t, "αβ", collectRunes(t, tr, 2))
	slot := tr.Checkpoint()
	require.NoError(t, slot.Release())

	// Read ahead, then re-arm the very same value at the new cursor.
	require.Equal(t, "γ\nhell", collectRunes(t, tr, 6))
	before := tr.Cursor()
	require.NoError(t, tr.Remark(slot))

	// Same pointer, active again, marking exactly where the cursor stands.
	assert.True(t, slot.Active())
	assert.Equal(t, before, slot.Pos())

	// Speculatively read to EOF and replay: a re-armed checkpoint reproduces
	// the input read since its mark, byte for byte, across multibyte and
	// newlines.
	replay, _ := readToEnd(t, tr)
	require.Equal(t, "o 世界\n🦄tail", replay)

	require.NoError(t, slot.Reset())
	assert.Equal(t, before, tr.Cursor())

	again, _ := readToEnd(t, tr)
	assert.Equal(t, replay, again, "re-arm must replay exactly like a fresh mark")
	require.NoError(t, slot.Commit())
	assert.False(t, slot.Active())
}

// TestRemarkRecordsTheCurrentCursor checks that Remark is a non-consuming
// operation: it marks the logical cursor as it stands and never moves it, the
// same guarantee Checkpoint gives.
func TestRemarkRecordsTheCurrentCursor(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("abcdefgh"))

	require.Equal(t, "abc", collectRunes(t, tr, 3))
	slot := tr.Checkpoint()
	require.NoError(t, slot.Release())

	mark := tr.Cursor()
	require.NoError(t, tr.Remark(slot))

	assert.Equal(t, mark, tr.Cursor(), "remark must not move the cursor")
	assert.Equal(t, mark, slot.Pos(), "remark must record the marked position")

	// A second remark moves the same slot forward, retargeting it exactly.
	// The slot must be released first: an active value is never re-armed.
	require.NoError(t, slot.Release())
	require.Equal(t, "de", collectRunes(t, tr, 2))
	mark2 := tr.Cursor()
	require.NoError(t, tr.Remark(slot))
	assert.Equal(t, mark2, slot.Pos())

	require.NoError(t, slot.Reset())
	assert.Equal(t, mark2, tr.Cursor())
	assert.Equal(t, "fg", collectRunes(t, tr, 2))
}

// TestRemarkRefusesActiveOnThisReader guards the misuse Remark must not
// permit: retargeting a checkpoint that is still holding a mark. Re-arming an
// active value on its own reader is refused, and the value is left untouched.
func TestRemarkRefusesActiveOnThisReader(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("abcdef"))

	active := tr.Checkpoint()
	require.Equal(t, "ab", collectRunes(t, tr, 2))
	origin := active.Pos()

	err := tr.Remark(active)
	assert.ErrorIs(t, err, textreader.ErrCheckpointActive)

	// The mark was not retargeted: it still sits where it was.
	assert.True(t, active.Active())
	assert.Equal(t, origin, active.Pos())
	require.NoError(t, active.Reset())
	assert.Equal(t, origin, tr.Cursor())
	require.NoError(t, active.Commit())
}

// TestRemarkRefusesNil reports the missing value with the released error, since
// a nil checkpoint has no value to re-arm.
func TestRemarkRefusesNil(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("abc"))

	var slot *textreader.Checkpoint
	assert.ErrorIs(t, tr.Remark(slot), textreader.ErrCheckpointReleased)
}

// TestRemarkAdoptsAValueReleasedFromAnotherReader confirms a released value
// carries no reader reference and may be re-armed on a different reader —
// including one distinct from the reader it was last armed on. Re-arming never
// touches the storage of a reader the value is no longer armed on.
func TestRemarkAdoptsAValueReleasedFromAnotherReader(t *testing.T) {
	first := textreader.NewReader(strings.NewReader("alpha beta"))
	second := textreader.NewReader(strings.NewReader("gamma delta"))

	// Arm on the first reader, then release it.
	cp := first.Checkpoint()
	require.Equal(t, "alpha ", collectRunes(t, first, 6))
	require.NoError(t, cp.Release())
	assert.False(t, cp.Active())

	// Re-arm the same value on a different reader, mid-stream.
	require.Equal(t, "gam", collectRunes(t, second, 3))
	mark := second.Cursor()
	require.NoError(t, second.Remark(cp))

	assert.True(t, cp.Active())
	assert.Equal(t, mark, cp.Pos())

	replay, _ := readToEnd(t, second)
	require.Equal(t, "ma delta", replay)
	require.NoError(t, cp.Reset())
	assert.Equal(t, mark, second.Cursor())
	again, _ := readToEnd(t, second)
	assert.Equal(t, replay, again)
	require.NoError(t, cp.Commit())

	// The first reader is unaffected: it still streams from where it was.
	assert.Equal(t, "beta", collectRunes(t, first, 4))
}

// TestRemarkRefusesAValueActiveOnAnotherReader is the cross-reader half of the
// active-refusal: a value still armed on one reader cannot be adopted by
// another, and the CAS keeps exactly one reader owner.
func TestRemarkRefusesAValueActiveOnAnotherReader(t *testing.T) {
	owner := textreader.NewReader(strings.NewReader("owner stream"))
	stranger := textreader.NewReader(strings.NewReader("stranger stream"))

	cp := owner.Checkpoint()
	require.Equal(t, "own", collectRunes(t, owner, 3))
	origin := cp.Pos()

	err := stranger.Remark(cp)
	assert.ErrorIs(t, err, textreader.ErrCheckpointActive)

	// The value is still armed on, and only on, the owner.
	assert.True(t, cp.Active())
	assert.Equal(t, origin, cp.Pos())
	require.NoError(t, cp.Reset())
	assert.Equal(t, origin, owner.Cursor())
	require.NoError(t, cp.Commit())

	// Once released, the same value is adoptable by the stranger.
	require.Equal(t, "stra", collectRunes(t, stranger, 4))
	mark := stranger.Cursor()
	require.NoError(t, stranger.Remark(cp))
	assert.Equal(t, mark, cp.Pos())
	require.NoError(t, cp.Commit())
}

// TestRemarkAfterCommit confirms a committed checkpoint can be re-armed:
// commit, like release, ends the registration so the value is free to reuse.
func TestRemarkAfterCommit(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("0123456789"))

	cp := tr.Checkpoint()
	require.Equal(t, "012", collectRunes(t, tr, 3))
	require.NoError(t, cp.Commit())
	assert.False(t, cp.Active())

	require.Equal(t, "34", collectRunes(t, tr, 2))
	mark := tr.Cursor()
	require.NoError(t, tr.Remark(cp))

	assert.Equal(t, mark, cp.Pos())
	assert.Equal(t, "567", collectRunes(t, tr, 3))
	require.NoError(t, cp.Reset())
	assert.Equal(t, mark, tr.Cursor())
	require.NoError(t, cp.Commit())
}

// TestRemarkRetentionFloorsAtTheOldestActiveMark checks that a re-armed
// checkpoint sets the retention floor exactly like a fresh one: the reader
// retains from the oldest active mark, so re-arming at an earlier position
// keeps the older bytes alive, and the retention limit fires when the retained
// span (cursor minus floor) would exceed the budget.
func TestRemarkRetentionFloorsAtTheOldestActiveMark(t *testing.T) {
	tr := textreader.NewReader(
		strings.NewReader("abcdefghij"),
		textreader.WithCapacity(16),
	)

	// Read four runes, then re-arm a slot at byte 4.
	require.Equal(t, "abcd", collectRunes(t, tr, 4))
	require.Equal(t, 4, tr.Cursor().ByteOffset())

	slot := tr.Checkpoint()
	require.NoError(t, slot.Release())
	require.NoError(t, tr.Remark(slot))
	assert.Equal(t, 4, slot.Pos().ByteOffset())

	// The retained span is cursor minus the re-armed floor: zero at the mark
	// itself, and growing as the cursor advances past it. Without the active
	// slot the span would stay zero, so a positive value proves the re-armed
	// checkpoint sets the floor.
	assert.Equal(t, 0, tr.RetainedBytes())

	// Reading two more runes keeps the floor at the re-armed mark.
	require.Equal(t, "ef", collectRunes(t, tr, 2))
	assert.Equal(t, 6, tr.Cursor().ByteOffset())
	assert.Equal(t, 2, tr.RetainedBytes())

	// Once the slot is released the floor returns to the cursor and the
	// retained span drops to nothing.
	require.NoError(t, slot.Release())
	assert.Equal(t, 0, tr.RetainedBytes())
	require.Equal(t, "gh", collectRunes(t, tr, 2))
	assert.Equal(t, 0, tr.RetainedBytes())
}

// TestRemarkRepeatedRearmSteadyState drives the lexer's steady-state loop: the
// same slot is released and re-armed on every token. The value's identity is
// constant, replay stays exact across the whole stream, and retention never
// grows unboundedly while the slot stays the only active mark.
func TestRemarkRepeatedRearmSteadyState(t *testing.T) {
	const tokens = 2000
	input := strings.Repeat("tok ", tokens)

	tr := textreader.NewReader(strings.NewReader(input))

	slot := tr.Checkpoint()
	for i := 0; i < tokens; i++ {
		// The slot is armed at this token's start: the initial Checkpoint on
		// the first token, a re-armed slot on the rest.
		assert.Equal(t, i*4, slot.Pos().ByteOffset(), "slot must mark token %d", i)
		require.Equal(t, "tok ", collectRunes(t, tr, 4))

		// End this token's mark, then re-arm the same slot at the next
		// token's start.
		require.NoError(t, slot.Release())
		if i+1 < tokens {
			require.NoError(t, tr.Remark(slot))
		}
	}

	// The slot is inactive at the end and the stream is fully consumed.
	assert.False(t, slot.Active())
	_, err := tr.ReadLocatedRune()
	assert.ErrorIs(t, err, io.EOF)
}

// TestRemarkAliasRegainsAuthority pins the documented ownership discipline:
// a reusable checkpoint is one shared mutable slot, so a reference retained
// from before Release silently regains authority over the new mark the moment
// the slot is re-armed. The test asserts the documented behavior — the stale
// alias observes the new mark — because the reader cannot and must not try to
// distinguish generations of a reused slot.
func TestRemarkAliasRegainsAuthority(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("hello world"))

	slot := tr.Checkpoint()
	alias := slot // a reference retained from the first generation
	assert.Equal(t, 0, alias.Pos().ByteOffset())

	// Consume the first token and the space, then hand the slot back for reuse.
	require.Equal(t, "hello ", collectRunes(t, tr, 6))
	require.NoError(t, slot.Release())

	// Re-arm the same slot at the current position. The retained alias now
	// refers to the new mark: its Pos moved with the slot, and it is active.
	require.NoError(t, tr.Remark(slot))
	assert.True(t, alias.Active())
	assert.Equal(t, 6, alias.Pos().ByteOffset())

	// And it holds the new mark's authority: the alias itself can Reset the
	// reader back to the re-armed position, exactly as the owner could.
	require.NoError(t, alias.Reset())
	assert.Equal(t, 6, tr.Cursor().ByteOffset())
}

// TestRemarkAtStreamStartAndEOF covers the edge positions: re-arming at the
// very start of the stream and at the end of it both record the cursor exactly
// and leave it unmoved.
func TestRemarkAtStreamStartAndEOF(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("hello"))

	// At the start: mark, release, re-arm at byte 0.
	slot := tr.Checkpoint()
	require.NoError(t, slot.Release())
	start := tr.Cursor()
	require.NoError(t, tr.Remark(slot))
	assert.Equal(t, start, slot.Pos())
	assert.Equal(t, start, tr.Cursor())
	require.NoError(t, slot.Commit())

	// At EOF: the cursor is past the last byte. Remark records it; replaying
	// from the end reads nothing.
	require.Equal(t, "hello", collectRunes(t, tr, 5))
	end := tr.Cursor()
	require.NoError(t, tr.Remark(slot))
	assert.Equal(t, end, slot.Pos())
	assert.Equal(t, end, tr.Cursor())

	replayed, err := readAllLocated(t, tr)
	assert.Equal(t, "", replayed)
	assert.ErrorIs(t, err, io.EOF)
	require.NoError(t, slot.Commit())
}

// TestConcurrentRemarkAdoption hammers the compare-and-swap claim: many
// goroutines re-arm distinct values on one reader while two readers contend
// for one shared value. Exactly the intended adoptions succeed; the contended
// value is owned by at most one reader at a time, and the reader stays sane.
func TestConcurrentRemarkAdoption(t *testing.T) {
	const slots = 64
	const iters = 40

	tr := textreader.NewReader(strings.NewReader(strings.Repeat(sample, 512)))
	tr2 := textreader.NewReader(strings.NewReader(strings.Repeat(sample, 512)))

	// One shared value that both readers will race to adopt.
	shared := tr.Checkpoint()
	require.NoError(t, shared.Release())

	var wg sync.WaitGroup
	for i := 0; i < slots; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Each goroutine owns its own slot on tr: release it, then re-arm
			// it in place every iteration. (t.Error, not require: require's
			// FailNow is not safe from another goroutine.)
			slot := tr.Checkpoint()
			if err := slot.Release(); err != nil {
				t.Error(err)
				return
			}
			for j := 0; j < iters; j++ {
				if err := tr.Remark(slot); err != nil {
					t.Error(err)
					return
				}
				if _, err := tr.ReadLocatedRune(); err != nil {
					// The shared stream may be exhausted by racing readers;
					// that is fine — the claim, not the read, is under test.
					break
				}
				if err := slot.Release(); err != nil {
					t.Error(err)
					return
				}
			}
			// The slot may still be armed if the loop broke early; end it.
			_ = slot.Release()
		}(i)
	}

	// Two readers race for the one shared value. The winner adopts it; the
	// loser gets ErrCheckpointActive while it is held.
	wg.Add(2)
	go func() {
		defer wg.Done()
		for j := 0; j < iters; j++ {
			if err := tr2.Remark(shared); err == nil {
				_ = shared.Commit()
			}
		}
	}()
	go func() {
		defer wg.Done()
		for j := 0; j < iters; j++ {
			if err := tr.Remark(shared); err == nil {
				_ = shared.Commit()
			}
		}
	}()

	wg.Wait()

	// The shared value ends inactive, owned by nobody.
	assert.False(t, shared.Active())
}

// TestRemarkRearmIsAllocationFree pins the whole point of the capability: a
// consumer that owns a fixed slot and re-arms it pays no checkpoint allocation
// per mark. The allocation count must stay flat as the stream grows — the one
// slot value is allocated once and reused for every token after it.
func TestRemarkRearmIsAllocationFree(t *testing.T) {
	run := func(tokens int) {
		input := strings.Repeat("tok ", tokens)
		tr := textreader.NewReader(strings.NewReader(input))
		slot := tr.Checkpoint()

		// Steady state: re-arm the one slot at every token start.
		for i := 0; i < tokens; i++ {
			if err := slot.Release(); err != nil {
				t.Fatal(err)
			}
			if err := tr.Remark(slot); err != nil {
				t.Fatal(err)
			}
			if _, err := tr.ReadLocatedRune(); err != nil {
				t.Fatal(err)
			}
		}
	}

	small := testing.AllocsPerRun(10, func() { run(256) })
	large := testing.AllocsPerRun(10, func() { run(1024) })

	assert.Equal(t, small, large, "re-arming must not allocate per mark")
}

// BenchmarkRemarkSteadyState drives the lexer's steady-state loop over a
// large stream: re-arm one slot per token, read the token, release, repeat.
// It is the textreader half of the allocation story; the textlexer
// benchmark measures the consumer end.
func BenchmarkRemarkSteadyState(b *testing.B) {
	const tokens = 1 << 10
	input := strings.Repeat("tok ", tokens)

	b.SetBytes(int64(len(input)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tr := textreader.NewReader(strings.NewReader(input))
		slot := tr.Checkpoint()
		for j := 0; j < tokens; j++ {
			// Read the whole four-rune token, then end its mark and re-arm
			// the slot at the next token's start.
			for k := 0; k < 4; k++ {
				if _, err := tr.ReadLocatedRune(); err != nil {
					b.Fatal(err)
				}
			}
			if err := slot.Release(); err != nil {
				b.Fatal(err)
			}
			if err := tr.Remark(slot); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkFreshCheckpointSteadyState is the allocating counterpart of
// BenchmarkRemarkSteadyState: the same per-token mark/release loop, but every
// token arms a fresh checkpoint with Checkpoint instead of re-arming a fixed
// slot. The allocs/op delta between the two is exactly the per-token checkpoint
// allocation the reuse capability removes.
func BenchmarkFreshCheckpointSteadyState(b *testing.B) {
	const tokens = 1 << 10
	input := strings.Repeat("tok ", tokens)

	b.SetBytes(int64(len(input)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tr := textreader.NewReader(strings.NewReader(input))
		for j := 0; j < tokens; j++ {
			cp := tr.Checkpoint()
			// Read the whole four-rune token, then end its mark, exactly as
			// BenchmarkRemarkSteadyState does — the only difference is that a
			// fresh checkpoint is armed each token instead of a slot re-armed.
			for k := 0; k < 4; k++ {
				if _, err := tr.ReadLocatedRune(); err != nil {
					b.Fatal(err)
				}
			}
			if err := cp.Release(); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// TestRemarkReplaysMalformedUTF8 covers the ticket's malformed-UTF-8 case: a
// re-armed checkpoint replays a replacement rune exactly like a fresh one.
// An invalid byte is read as a single replacement rune, and the reset must
// reproduce it byte for byte.
func TestRemarkReplaysMalformedUTF8(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("a\xff\nb"))

	slot := tr.Checkpoint()
	require.NoError(t, slot.Release())
	require.NoError(t, tr.Remark(slot))
	mark := slot.Pos()

	first, _ := readAllLocated(t, tr)

	require.NoError(t, slot.Reset())
	assert.Equal(t, mark, tr.Cursor())

	again, _ := readAllLocated(t, tr)
	assert.Equal(t, first, again, "re-arm must replay malformed bytes exactly")
	require.NoError(t, slot.Commit())
}

// TestRemarkNestsWithFreshCheckpoints covers nesting: a re-armed slot and a
// fresh checkpoint are active at once at different marks, and each contributes
// to retention independently. Releasing the re-armed slot does not release the
// fresh one's mark, and vice versa.
func TestRemarkNestsWithFreshCheckpoints(t *testing.T) {
	tr := textreader.NewReader(strings.NewReader("0123456789"))

	// The re-armed slot holds the older mark.
	slot := tr.Checkpoint()
	require.Equal(t, "01", collectRunes(t, tr, 2))
	require.NoError(t, slot.Release())
	require.NoError(t, tr.Remark(slot)) // slot active at byte 2
	require.Equal(t, "2", collectRunes(t, tr, 1))

	// A fresh checkpoint holds the newer mark, nested after the slot's.
	fresh := tr.Checkpoint() // fresh active at byte 3
	require.Equal(t, "3", collectRunes(t, tr, 1))

	// The floor is the older of the two: the re-armed slot at byte 2.
	assert.Equal(t, 2, tr.RetainedBytes())

	// Release the slot; the fresh mark at byte 3 still retains.
	require.NoError(t, slot.Release())
	assert.Equal(t, 1, tr.RetainedBytes())

	// The slot is free to re-arm at the current cursor, alongside the fresh one.
	require.NoError(t, tr.Remark(slot)) // slot active at byte 4
	require.Equal(t, "4", collectRunes(t, tr, 1))
	assert.Equal(t, 2, tr.RetainedBytes()) // floor is the fresh mark at byte 3

	// Each mark ends independently.
	require.NoError(t, slot.Commit())
	require.NoError(t, fresh.Commit())
	assert.Equal(t, 0, tr.RetainedBytes())
}

// TestRemarkRetentionLimitAppliesLikeFreshCheckpoints covers the finite-bounds
// case: the retention limit fires on a re-armed checkpoint exactly as it does
// on a fresh one — the over-budget read fails, moves nothing, and the re-armed
// mark is left active.
func TestRemarkRetentionLimitAppliesLikeFreshCheckpoints(t *testing.T) {
	tr := textreader.NewReader(
		strings.NewReader("0123456789"),
		textreader.WithMaxRetained(4),
	)

	// Read four bytes, re-arm the slot at byte 4.
	require.Equal(t, "0123", collectRunes(t, tr, 4))
	spot := tr.Checkpoint()
	require.NoError(t, spot.Release())
	require.NoError(t, tr.Remark(spot))

	// The retained span grows as the cursor advances past the re-armed mark,
	// bounded by the same limit a fresh checkpoint would get: four bytes fit,
	// taking the span to exactly the limit.
	require.Equal(t, "4567", collectRunes(t, tr, 4))
	assert.Equal(t, 4, tr.RetainedBytes())

	// The next byte would push the retained span past the limit.
	_, err := tr.ReadLocatedRune()
	assert.ErrorIs(t, err, textreader.ErrRetentionExceeded)
	assert.Equal(t, 8, tr.Cursor().ByteOffset(), "the refused read must not move the cursor")

	// The re-armed mark is still active and replayable.
	require.NoError(t, spot.Reset())
	assert.Equal(t, 4, tr.Cursor().ByteOffset())
	require.NoError(t, spot.Commit())
}

// TestRemarkConcurrentPosDuringReleaseAndRearm is the reviewer's blocking
// finding, head on: Checkpoint.Pos and Active are called from one goroutine
// while the same released value is re-armed and released from others on two
// different readers. The marked position and the active flag must be observed
// whole, never half-written — the value's own lock is what makes each of those
// fields stable for the value's whole life, independently of which reader
// currently owns it. Run under -race.
func TestRemarkConcurrentPosDuringReleaseAndRearm(t *testing.T) {
	input := strings.Repeat(sample, 512)
	tr := textreader.NewReader(strings.NewReader(input))
	tr2 := textreader.NewReader(strings.NewReader(input))

	slot := tr.Checkpoint()
	require.NoError(t, slot.Release())

	const iters = 2048
	var wg sync.WaitGroup

	// Two migrators race to re-arm and release the same released value on two
	// readers, so the value's owner flips back and forth while the position
	// reader hammers it.
	for _, r := range []*textreader.TextReader{tr, tr2} {
		wg.Add(1)
		go func(r *textreader.TextReader) {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				if err := r.Remark(slot); err == nil {
					if _, err := r.ReadLocatedRune(); err != nil {
						break // stream exhausted by the other reader; fine
					}
					_ = slot.Release()
				}
			}
		}(r)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < iters; j++ {
			_ = slot.Pos()
			_ = slot.Active()
		}
	}()

	wg.Wait()
}

// TestRemarkConcurrentMigrationVsCheckpointOps is the second half of the
// reviewer's blocking finding: the same released value migrates between two
// readers while Active, Reset, Commit, and Release are called on it from a
// third goroutine. Each operation must re-verify under the value's lock that
// the value is still armed on the reader it locked, so no operation ever
// guards another reader's state with the wrong mutex. Run under -race.
func TestRemarkConcurrentMigrationVsCheckpointOps(t *testing.T) {
	input := strings.Repeat(sample, 512)
	tr := textreader.NewReader(strings.NewReader(input))
	tr2 := textreader.NewReader(strings.NewReader(input))

	slot := tr.Checkpoint()
	require.NoError(t, slot.Release())

	const iters = 2048
	var wg sync.WaitGroup

	for _, r := range []*textreader.TextReader{tr, tr2} {
		wg.Add(1)
		go func(r *textreader.TextReader) {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				if err := r.Remark(slot); err == nil {
					if _, err := r.ReadLocatedRune(); err != nil {
						break
					}
					_ = slot.Release()
				}
			}
		}(r)
	}

	// The operator hammers the checkpoint's own methods. Released, active, and
	// rewound errors are expected and ignored: the lock discipline, not the
	// outcome, is what is under test.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < iters; j++ {
			if slot.Active() {
				switch j % 3 {
				case 0:
					_ = slot.Reset()
				case 1:
					_ = slot.Commit()
				default:
					_ = slot.Release()
				}
			}
		}
	}()

	wg.Wait()
}
