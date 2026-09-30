package textreader

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

var (
	// ErrRetentionExceeded reports that a read would have retained more input
	// than the configured retention limit allows while a checkpoint was
	// active. The read performs no partial work: the caller observes an error
	// instead of silent loss of checkpoint data.
	ErrRetentionExceeded = errors.New("textreader: retained input limit exceeded")

	// ErrCheckpointReleased reports use of a checkpoint that was already
	// committed or released.
	ErrCheckpointReleased = errors.New("textreader: checkpoint already released")

	// ErrCheckpointRewound reports a Commit on a checkpoint that the logical
	// cursor has moved behind. The caller must Reset back to the checkpoint or
	// abandon it with Release.
	ErrCheckpointRewound = errors.New("textreader: logical cursor is before the checkpoint")

	// ErrCheckpointActive reports a Remark on a checkpoint that is still
	// active, on the reader or another. An active checkpoint owns its mark and
	// its retained input, so it cannot be re-armed until it is committed or
	// released.
	ErrCheckpointActive = errors.New("textreader: checkpoint already active")

	// ErrPositionOutOfBuffer reports that a position or span is outside the
	// bytes the reader still retains.
	ErrPositionOutOfBuffer = errors.New("textreader: position outside retained input")
)

// Option configures a TextReader created by NewReader or NewRuneReader.
type Option func(*TextReader)

// WithCapacity sets the reader's buffer capacity in bytes. Values below the
// size of a maximal UTF-8 rune are raised to that size.
func WithCapacity(capacity int) Option {
	return func(t *TextReader) {
		t.capacity = capacity
	}
}

// WithMaxRetained bounds the retained region: the bytes from the oldest active
// checkpoint to the logical cursor that the reader must keep so a Reset can
// replay them. Zero (the default) means no bound.
//
// The limit applies only while at least one checkpoint is active. A reader with
// no active checkpoint compacts its buffer as usual, so basic callers pay no
// retention cost. The budget is enforced where the logical cursor would advance:
// a read that would push the retained region past the limit fails with
// ErrRetentionExceeded, moves the cursor by nothing, and consumes nothing — a
// bulk Read refuses the whole request rather than returning part of it. A rune
// that cannot fit the remaining budget is reported the same way, never
// half-decoded.
//
// Read-ahead in front of the cursor is bounded by the buffer capacity rather
// than by this limit, and it stays bounded while the cursor cannot advance.
func WithMaxRetained(n int) Option {
	return func(t *TextReader) {
		if n < 0 {
			n = 0
		}
		t.maxRetained = n
	}
}

// Pos is an immutable position in a text stream.
//
// Line is 1-based and Column is a 0-based count of runes since the last
// newline, matching position.Position. ByteOffset and RuneOffset are 0-based
// absolute offsets from the start of the stream.
//
// Pos values are produced by the reader and are safe to store, compare, and
// share. The zero Pos is not a valid stream position: a reader always reports
// Line 1 or greater.
type Pos struct {
	byteOffset int
	runeOffset int
	line       int
	column     int
}

// ByteOffset returns the absolute byte offset of the position.
func (p Pos) ByteOffset() int { return p.byteOffset }

// RuneOffset returns the absolute rune offset of the position.
func (p Pos) RuneOffset() int { return p.runeOffset }

// Line returns the 1-based line number of the position.
func (p Pos) Line() int { return p.line }

// Column returns the 0-based rune column of the position.
func (p Pos) Column() int { return p.column }

// IsStart reports whether the position is the start of the stream.
func (p Pos) IsStart() bool { return p.byteOffset == 0 && p.runeOffset == 0 }

// String returns the position formatted as "line:column".
func (p Pos) String() string { return fmt.Sprintf("%d:%d", p.line, p.column) }

// Compare orders p against q by byte offset and then by rune offset. It returns
// a negative number when p precedes q, zero when they are equal, and a positive
// number when p follows q.
func (p Pos) Compare(q Pos) int {
	switch {
	case p.byteOffset < q.byteOffset:
		return -1
	case p.byteOffset > q.byteOffset:
		return 1
	case p.runeOffset < q.runeOffset:
		return -1
	case p.runeOffset > q.runeOffset:
		return 1
	default:
		return 0
	}
}

// Span is an immutable half-open range of the text stream: it covers the bytes
// from Start, up to but not including End.
type Span struct {
	start Pos
	end   Pos
}

// NewSpan builds the half-open span that starts at start and ends at end. The
// two positions are expected to come from the same reader; a span whose
// positions are reversed is reported as an error by the methods that consume
// it.
func NewSpan(start, end Pos) Span { return Span{start: start, end: end} }

// Start returns the position of the first byte of the span.
func (s Span) Start() Pos { return s.start }

// End returns the position immediately after the span.
func (s Span) End() Pos { return s.end }

// Bytes returns the width of the span in bytes.
func (s Span) Bytes() int { return s.end.byteOffset - s.start.byteOffset }

// Runes returns the width of the span in runes.
func (s Span) Runes() int { return s.end.runeOffset - s.start.runeOffset }

// IsEmpty reports whether the span covers no bytes.
func (s Span) IsEmpty() bool { return s.Bytes() <= 0 }

// String returns the span formatted as "start>end".
func (s Span) String() string { return s.start.String() + ">" + s.end.String() }

// LocatedRune is a rune together with the immutable position it was read from.
type LocatedRune struct {
	r    rune
	size int
	pos  Pos
}

// Rune returns the decoded rune.
func (lr LocatedRune) Rune() rune { return lr.r }

// Size returns the number of bytes the rune occupies in the stream.
func (lr LocatedRune) Size() int { return lr.size }

// Pos returns the position of the first byte of the rune.
func (lr LocatedRune) Pos() Pos { return lr.pos }

// End returns the position immediately after the rune. Line and column advance
// the way the reader counts them: a newline moves to the next line at column 0.
func (lr LocatedRune) End() Pos {
	end := lr.pos
	end.byteOffset += lr.size
	end.runeOffset++
	if lr.r == '\n' {
		end.line++
		end.column = 0
	} else {
		end.column++
	}
	return end
}

// Span returns the half-open span covered by the rune.
func (lr LocatedRune) Span() Span { return NewSpan(lr.pos, lr.End()) }

// String returns the rune as a string.
func (lr LocatedRune) String() string { return string(lr.r) }

// Checkpoint marks a logical position for later replay.
//
// While a checkpoint is active the reader retains every byte needed to restore
// the marked position, so a speculative consumer can read ahead and then Reset
// to replay the same input exactly. Retention lasts until the checkpoint is
// Committed or Released.
//
// A checkpoint is reader-wide state, not goroutine-local. Methods on the
// reader and on the checkpoint are safe for concurrent use, but two goroutines
// sharing one reader share the same logical cursor and the same checkpoints.
//
// The fields below the reader pointer are guarded by mu, not by any reader's
// mutex: Remark can move a released value from one reader to another, so the
// value's own lock is the only lock that is stable for its whole life.
// Readers that hold the value's pointer (Active, Reset, Commit, Release) must
// re-verify under mu that the value is still armed on the reader they locked;
// a value that migrates or is released in between must not be operated on.
type Checkpoint struct {
	// reader is nil once the checkpoint is committed or released, which is how
	// a stale handle is detected without touching reader state.
	reader atomic.Pointer[TextReader]

	// mu guards pos, byteOffset, runeOffset, and active.
	mu sync.Mutex

	pos        Pos
	byteOffset int
	runeOffset int
	active     bool
}

// Checkpoint records the current logical position and starts retaining input
// for replay. Marking never moves the cursor and never reads ahead.
//
// Checkpoints nest: several may be active at once, and the reader retains input
// from the oldest of them. A Read that would exceed the configured retention
// limit fails with ErrRetentionExceeded.
func (t *TextReader) Checkpoint() *Checkpoint {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Marking is a non-consuming operation: it records the logical cursor as it
	// stands and never settles, rewinds, or otherwise moves it. A pending
	// partial UTF-8 sequence is deliberately left pending, so the marked
	// position is exactly the one Pos reported before the call.
	c := &Checkpoint{active: true}
	c.mu.Lock()
	c.pos = t.cursorLocked()
	c.byteOffset = t.pos.Offset()
	c.runeOffset = t.runeOffset
	c.mu.Unlock()
	c.reader.Store(t)
	t.checkpoints = append(t.checkpoints, c)

	return c
}

// Remark re-arms a checkpoint value c at the reader's current logical position
// so the caller can reuse it instead of calling Checkpoint and allocating a new
// mark. It is the opt-in half of the checkpoint-reuse capability: a consumer
// that owns a fixed set of checkpoint slots — a lexer with a bounded mark
// window, for example — arms each slot once with Checkpoint and then re-arms
// the same values in place. Re-arming allocates no new checkpoint, and a
// checkpoint that has never been released keeps its value forever, so the
// allocation cost is bounded by the number of slots, not by the number of marks
// made.
//
// Remark is a non-consuming operation, like Checkpoint: it records the logical
// cursor as it stands, never moves it, and leaves a pending partial UTF-8
// sequence pending. After a successful Remark the checkpoint is active and
// behaves exactly like a fresh Checkpoint: Pos reports the marked position,
// Reset replays the input read since the mark, and Commit or Release ends the
// retention. The reader retains input from the oldest active checkpoint, as
// before, and the retention limit applies to a re-armed checkpoint the same way
// it applies to a fresh one.
//
// Only a released checkpoint may be re-armed:
//
//   - a nil checkpoint returns ErrCheckpointReleased;
//   - an active checkpoint — on this reader or another — returns
//     ErrCheckpointActive and is left untouched. Re-arming a checkpoint that is
//     still holding a mark would silently retarget the mark another consumer
//     relies on, which is exactly the misuse Remark must not permit. A caller
//     that wants to move a slot's mark releases the old mark first and then
//     re-arms;
//   - the reader pointer is claimed by compare-and-swap, so one checkpoint
//     value is active on at most one reader at a time. A value that is active
//     on another reader cannot be adopted; a value that has been released
//     carries no reader reference and may be re-armed on any reader, including
//     one different from the reader it was last armed on. Re-arming never
//     touches the storage of a reader the value is no longer armed on.
//
// Ownership discipline. A reusable checkpoint is one shared mutable slot, not
// a per-mark object: re-arming the same value changes what every alias of it
// refers to. If a reference to the same *Checkpoint is retained from before
// Release, that old reference silently regains Pos, Reset, Commit, and Release
// authority over the new mark the moment the slot is re-armed — the reader
// cannot distinguish an alias from the slot owner and cannot detect the
// misuse, because generations of a reused slot are not distinguished. It is
// therefore the caller's discipline to keep all references to a released slot
// under one logical owner: once a slot is handed back for reuse, do not
// retain or use stale references to it. This is the aliasing contract the
// linked issue requires be stated explicitly.
//
// A released checkpoint does not retain input: release ends its registration,
// so re-arming re-registers the value without holding any extra storage. The
// checkpoint's own state is private and never exposed, so a re-armed value is
// indistinguishable in behavior from a fresh one.
//
// A failed `Remark` leaves the checkpoint exactly as it was: a released value
// stays released and an active value stays active, and the reader is left
// exactly as it was.
func (t *TextReader) Remark(c *Checkpoint) error {
	if c == nil {
		return ErrCheckpointReleased
	}

	// Claim the value before touching reader state. A released checkpoint has
	// a nil reader pointer; if the CAS fails, the value is active somewhere —
	// on this reader or another — and must be refused rather than retargeted.
	if !c.reader.CompareAndSwap(nil, t) {
		return ErrCheckpointActive
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// The CAS proved c is not active on any reader, so the value is not in the
	// active set: re-arming appends it. The marked position is the logical
	// cursor exactly as it stands, the same non-consuming guarantee Checkpoint
	// gives. The field writes take c.mu, not t.mu: a concurrent Pos or a
	// concurrent operation on a former owner must observe either the old or
	// the new values whole, never a torn mix.
	c.mu.Lock()
	c.pos = t.cursorLocked()
	c.byteOffset = t.pos.Offset()
	c.runeOffset = t.runeOffset
	c.active = true
	c.mu.Unlock()
	t.checkpoints = append(t.checkpoints, c)

	return nil
}

// Pos returns the position that was marked.
func (c *Checkpoint) Pos() Pos {
	if c == nil {
		return Pos{}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return c.pos
}

// Active reports whether the checkpoint can still be reset, committed, or
// released.
func (c *Checkpoint) Active() bool {
	if c == nil {
		return false
	}

	t := c.reader.Load()
	if t == nil {
		return false
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Re-verify under c.mu: the value may have migrated to another reader or
	// been released between the Load and the lock, in which case its state
	// here is no longer this reader's to report.
	c.mu.Lock()
	owner := c.reader.Load()
	still := c.active && owner != nil && owner == t
	c.mu.Unlock()

	return still
}

// Reset restores the logical cursor to the marked position, so subsequent reads
// replay exactly the input that was read since the mark. Reset does not consume
// the checkpoint: it may be called again to replay the same range a second
// time.
//
// Reset restores the complete position, including line and rune column, across
// Unicode text and newlines. It also clears the single-level UnreadRune state,
// because the rune that unread would restore is no longer the last one read.
//
// Reset fails with ErrCheckpointReleased on a checkpoint that was already
// committed or released, and with ErrPositionOutOfBuffer if the marked bytes
// are no longer retained (which can only happen if the retention limit changed
// the reader's guarantees, or if the checkpoint belongs to a different reader).
func (c *Checkpoint) Reset() error {
	if c == nil {
		return ErrCheckpointReleased
	}

	t := c.reader.Load()
	if t == nil {
		return ErrCheckpointReleased
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Re-verify under c.mu and copy the mark's fields there: the value may
	// have been released or migrated to another reader between the Load and
	// the lock, in which case this reader must not operate on its state. Once
	// verified here while holding t.mu the value cannot move again until the
	// lock is released: release and same-reader operations need t.mu, and
	// another reader's Remark cannot claim a value that still points at t.
	c.mu.Lock()
	if !c.active || c.reader.Load() != t {
		c.mu.Unlock()
		return ErrCheckpointReleased
	}
	byteOff := c.byteOffset
	runeOff := c.runeOffset
	c.mu.Unlock()

	bufferStart := t.pos.Offset() - t.r
	idx := byteOff - bufferStart
	if idx < 0 || idx > t.w {
		return ErrPositionOutOfBuffer
	}

	// Restore the logical position. The bytes are still retained, so moving
	// back is an exact inverse of the reads that moved forward. Rewinding
	// crosses only the lines between here and the mark, which is what keeps a
	// checkpoint cheap to reset.
	switch cur := t.pos.Offset(); {
	case cur > byteOff:
		if err := t.pos.Rewind(cur-byteOff, t.runeOffset-runeOff); err != nil {
			return fmt.Errorf("checkpoint reset: %w", err)
		}
	case cur < byteOff:
		t.pos.Scan(t.buf[t.r:idx])
	}

	t.runeOffset = runeOff
	t.r = idx
	t.lastRuneSize = -1
	t.utf8CarryLen = 0

	return nil
}

// Commit accepts the current logical position and drops the input retained for
// this checkpoint. The checkpoint becomes inactive.
//
// Commit fails with ErrCheckpointRewound when the logical cursor is behind the
// mark, because the caller has not yet decided what to do with the replayed
// range; Reset to the mark or abandon the checkpoint with Release instead.
func (c *Checkpoint) Commit() error {
	if c == nil {
		return ErrCheckpointReleased
	}

	t := c.reader.Load()
	if t == nil {
		return ErrCheckpointReleased
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Re-verify under c.mu: the value may have been released here and
	// re-armed on another reader between the Load and the lock, in which
	// case its mark is no longer this reader's to commit.
	c.mu.Lock()
	if !c.active || c.reader.Load() != t {
		c.mu.Unlock()
		return ErrCheckpointReleased
	}
	byteOff := c.byteOffset
	c.mu.Unlock()

	if t.pos.Offset() < byteOff {
		return ErrCheckpointRewound
	}

	return t.releaseCheckpointLocked(c)
}

// Release abandons the checkpoint and drops the input retained for it,
// whatever the logical cursor currently is. The checkpoint becomes inactive.
func (c *Checkpoint) Release() error {
	if c == nil {
		return ErrCheckpointReleased
	}

	t := c.reader.Load()
	if t == nil {
		return ErrCheckpointReleased
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Re-verify under c.mu: the value may have been released here and
	// re-armed on another reader between the Load and the lock, in which
	// case its mark is no longer this reader's to release.
	c.mu.Lock()
	if !c.active || c.reader.Load() != t {
		c.mu.Unlock()
		return ErrCheckpointReleased
	}
	c.mu.Unlock()

	return t.releaseCheckpointLocked(c)
}

func (t *TextReader) releaseCheckpointLocked(c *Checkpoint) error {
	for i, other := range t.checkpoints {
		if other == c {
			t.checkpoints = append(t.checkpoints[:i], t.checkpoints[i+1:]...)
			break
		}
	}

	// Deregistration under c.mu: a concurrent Pos or Remark on another
	// reader must observe either the fully armed or the fully released
	// state, never a mix, and the reader pointer is cleared only after the
	// state is, so a CAS in Remark cannot claim a value that is still
	// armed here.
	c.mu.Lock()
	c.active = false
	c.mu.Unlock()
	c.reader.Store(nil)

	return nil
}

// RetainedBytes reports how many bytes the reader currently holds for
// checkpoint replay. Without an active checkpoint it is zero.
func (t *TextReader) RetainedBytes() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	floor := t.retainFloor()
	if floor >= t.pos.Offset() {
		return 0
	}

	return t.pos.Offset() - floor
}

// SpanText returns the exact text covered by s.
//
// SpanText does not move the logical cursor and does not read from the
// underlying source. It fails with ErrPositionOutOfBuffer when any byte of the
// span is outside the retained input: keep a checkpoint open across a
// speculative range if its text must be recoverable afterwards. Recoverable
// spans are the ones built from located runes the reader returned while a
// checkpoint was active.
func (t *TextReader) SpanText(s Span) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	start, end := s.start.byteOffset, s.end.byteOffset
	if end < start {
		return "", fmt.Errorf("textreader: span end precedes start: %s", s)
	}

	window, err := t.windowFor(start, end)
	if err != nil {
		return "", err
	}

	return string(window), nil
}

// Context returns the retained text around s, extended by before bytes on the
// left and after bytes on the right. The result is clamped to the retained
// input, so a span close to the start of the stream returns a shorter prefix
// rather than an error.
//
// Like SpanText, Context does not move the logical cursor. It fails with
// ErrPositionOutOfBuffer when the span itself is not retained.
func (t *TextReader) Context(s Span, before, after int) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if before < 0 || after < 0 {
		return "", fmt.Errorf("textreader: negative context width: before=%d after=%d", before, after)
	}

	start, end := s.start.byteOffset, s.end.byteOffset
	if end < start {
		return "", fmt.Errorf("textreader: span end precedes start: %s", s)
	}

	if _, err := t.windowFor(start, end); err != nil {
		return "", err
	}

	bufferStart := t.pos.Offset() - t.r
	bufferEnd := bufferStart + t.w

	// The left edge is the retention floor, not the physical buffer start:
	// bytes below the floor are not retained, so context must not reach them
	// even when they have not been compacted away yet. Both edges are computed
	// by comparison rather than by addition, so a large width cannot overflow.
	floor := t.retainFloor()
	if before > start-floor {
		start = floor
	} else {
		start -= before
	}
	if after > bufferEnd-end {
		end = bufferEnd
	} else {
		end += after
	}

	return string(t.buf[start-bufferStart : end-bufferStart]), nil
}

// windowFor returns the retained bytes between two absolute offsets. Bytes
// below the retention floor are not retained: no checkpoint needs them, so the
// reader may have compacted them away already.
func (t *TextReader) windowFor(start, end int) ([]byte, error) {
	bufferStart := t.pos.Offset() - t.r

	if start < bufferStart || end > bufferStart+t.w || start < t.retainFloor() {
		return nil, ErrPositionOutOfBuffer
	}

	return t.buf[start-bufferStart : end-bufferStart], nil
}
