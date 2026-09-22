package textreader

import (
	"errors"
	"fmt"
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
type Checkpoint struct {
	// reader is nil once the checkpoint is committed or released, which is how
	// a stale handle is detected without touching reader state.
	reader atomic.Pointer[TextReader]

	pos        Pos
	byteOffset int
	runeOffset int

	// active is guarded by reader.mu.
	active bool
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
	c := &Checkpoint{
		pos:        t.cursorLocked(),
		byteOffset: t.pos.Offset(),
		runeOffset: t.runeOffset,
		active:     true,
	}
	c.reader.Store(t)
	t.checkpoints = append(t.checkpoints, c)

	return c
}

// Pos returns the position that was marked.
func (c *Checkpoint) Pos() Pos { return c.pos }

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

	return c.active
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

	if !c.active {
		return ErrCheckpointReleased
	}

	bufferStart := t.pos.Offset() - t.r
	idx := c.byteOffset - bufferStart
	if idx < 0 || idx > t.w {
		return ErrPositionOutOfBuffer
	}

	// Restore the logical position. The bytes are still retained, so moving
	// back is an exact inverse of the reads that moved forward. Rewinding
	// crosses only the lines between here and the mark, which is what keeps a
	// checkpoint cheap to reset.
	switch cur := t.pos.Offset(); {
	case cur > c.byteOffset:
		if err := t.pos.Rewind(cur-c.byteOffset, t.runeOffset-c.runeOffset); err != nil {
			return fmt.Errorf("checkpoint reset: %w", err)
		}
	case cur < c.byteOffset:
		t.pos.Scan(t.buf[t.r:idx])
	}

	t.runeOffset = c.runeOffset
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

	if !c.active {
		return ErrCheckpointReleased
	}
	if t.pos.Offset() < c.byteOffset {
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

	if !c.active {
		return ErrCheckpointReleased
	}

	return t.releaseCheckpointLocked(c)
}

func (t *TextReader) releaseCheckpointLocked(c *Checkpoint) error {
	for i, other := range t.checkpoints {
		if other == c {
			t.checkpoints = append(t.checkpoints[:i], t.checkpoints[i+1:]...)
			break
		}
	}

	c.active = false
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
