// Package textreader provides a buffered reader that keeps track of the
// current line, column, and offset of the text being read.
//
// The package has two layers.
//
// The basic layer is the sequential reader: Read, ReadRune, UnreadRune, Seek,
// and Pos. It tracks the position of the next read and is sufficient for a
// caller that consumes the stream once, in order.
//
// The cursor layer adds the mechanics a speculative consumer (a lexer, a
// hand-written parser, a syntax highlighter) needs: immutable positions, spans
// and located runes; logical checkpoints with exact replay; non-mutating access
// to the retained text and its surroundings; and a bounded retained-input
// policy. See cursor.go. The cursor layer owns text-stream mechanics only. It
// knows nothing about tokens, lexical rules, or longest-match selection.
//
// The text constructors (New, NewWithCapacity, NewReader, NewRuneReader) read
// UTF-8 text. NewByteReader reads binary input instead: each byte is one rune of
// size 1, so every position the reader reports is a file offset.
//
// Deprecated: use github.com/xiam/bytescan/cursor, which has the same API. To
// migrate, change the import path github.com/xiam/textreader to
// github.com/xiam/bytescan/cursor, and github.com/xiam/textreader/position to
// github.com/xiam/bytescan/cursor/position. Rename TextReader to cursor.Reader.
package textreader

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sync"
	"unicode/utf8"

	"github.com/xiam/textreader/position"
)

const (
	defaultCapacity = 64 * 1024
)

var (
	ErrBufferTooSmall  = errors.New("buffer too small")
	ErrInvalidUTF8     = errors.New("invalid UTF-8 encoding")
	ErrSeekOutOfBuffer = errors.New("seek out of buffer")
)

// TextReader reads from an io.Reader, buffering data and keeping track of the
// current position (line, column, and offset) in the text stream. It supports
// seeking only within the currently buffered data, which is good enough for
// giving context of the text around the current read position.
//
// A TextReader is safe for concurrent use by multiple goroutines. Positions
// reported by the reader are immutable values; no method hands out a mutable
// reference to the reader's state.
type TextReader struct {
	// br is the byte source. It is nil when the reader was built over an
	// io.RuneReader source, in which case runeSrc is set instead.
	br io.Reader
	// runeSrc is the rune source, used by NewRuneReader.
	runeSrc io.RuneReader

	// byteMode reads each input byte as one rune of size 1 (see
	// NewByteReader). It is fixed at construction. In byte mode the UTF-8
	// carry below is never used, and the rune offset equals the byte offset.
	byteMode bool

	mu sync.Mutex

	// pos tracks the logical position of the next read: bytes and runes
	// scanned so far, plus the line and rune column. It never describes
	// physical read-ahead.
	pos *position.Position

	// runeOffset is the absolute rune offset of the logical cursor. It is
	// maintained alongside pos because position.Position tracks bytes only.
	runeOffset int

	lastRuneSize int

	capacity int
	// maxRetained bounds the bytes held for checkpoint replay. Zero means no
	// bound. The limit applies only while a checkpoint is active: a basic
	// reader with no checkpoint never pays a retention cost and its buffer
	// compacts as usual.
	maxRetained int

	buf []byte

	// r and w are indices into buf. The absolute byte offset of buf[i] is
	// pos.Offset() - r + i; that invariant holds after every operation.
	r int
	w int

	// checkpoints holds the active checkpoints, oldest first.
	checkpoints []*Checkpoint

	// readErr is a sticky error returned by the underlying source. Replaying
	// from a checkpoint reproduces a reader error deterministically instead of
	// observing whatever the source reports on the second pass.
	readErr error

	// pendingRune holds a rune read from a rune source that did not fit the
	// room available in the buffer. It is encoded on the next fill, so a rune
	// is never dropped at a buffer boundary.
	pendingRune rune
	hasPending  bool

	// utf8Carry holds the leading bytes of an incomplete UTF-8 sequence seen
	// across byte reads. Their byte count is already reflected in the logical
	// position; their rune and column contribution is credited when the
	// sequence completes, which keeps rune coordinates exact when a Read splits
	// a rune.
	utf8Carry    [utf8.UTFMax]byte
	utf8CarryLen int
}

// New returns a new TextReader that reads from r with the default buffer
// capacity.
func New(r io.Reader) *TextReader {
	return NewWithCapacity(r, defaultCapacity)
}

// NewWithCapacity returns a new TextReader with a buffer of at least the
// specified capacity.
func NewWithCapacity(r io.Reader, capacity int) *TextReader {
	return NewReader(r, WithCapacity(capacity))
}

// NewReader returns a new positioned cursor over r, configured by opts. It is
// the option-aware counterpart of New; New and NewWithCapacity remain the
// compatible constructors.
func NewReader(r io.Reader, opts ...Option) *TextReader {
	t := newTextReader(opts...)
	t.br = r
	return t
}

// NewRuneReader returns a new positioned cursor over an io.RuneReader.
//
// The rune source is read one rune at a time and each rune is encoded back to
// UTF-8 in the reader's buffer, so byte offsets describe the stream the rune
// reader represents. This is not a second UTF-8 decoder: no bytes are decoded,
// the decoder itself is the caller's io.RuneReader.
func NewRuneReader(rr io.RuneReader, opts ...Option) *TextReader {
	t := newTextReader(opts...)
	t.runeSrc = rr
	return t
}

// NewByteReader returns a new positioned cursor that reads each byte of r as
// one rune, U+0000 to U+00FF, of size 1. It accepts the same options as
// NewReader.
//
// In byte mode no input is decoded as UTF-8:
//
//   - ReadRune, ReadLocatedRune and PeekRune return rune(b) and size 1 for each
//     byte b, including bytes of 0x80 and above and bytes that would form valid
//     UTF-8 in text mode;
//   - ByteOffset and RuneOffset of every Pos are both the file offset;
//   - the byte 0x0A ends a line, and Column counts the bytes since the last
//     0x0A;
//   - Read, SpanText and Context return the raw input bytes;
//   - Seek, UnreadRune, Checkpoint, Reset, Remark and WithMaxRetained work on
//     file offsets, so a checkpoint over n bytes retains n bytes.
//
// Pos returns a position.Position that holds the same line, column and offset.
// Its Scan method decodes UTF-8, so a caller that scans more input on that
// copy gets text-mode columns for the scanned bytes.
func NewByteReader(r io.Reader, opts ...Option) *TextReader {
	t := newTextReader(opts...)
	t.br = r
	t.byteMode = true
	return t
}

func newTextReader(opts ...Option) *TextReader {
	t := &TextReader{
		capacity:     defaultCapacity,
		pos:          position.New(),
		lastRuneSize: -1,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	if t.capacity < utf8.UTFMax {
		t.capacity = utf8.UTFMax
	}
	t.buf = make([]byte, t.capacity)
	return t
}

func (t *TextReader) fillAtLeast(n int) (bool, error) {
	if n < 0 {
		return false, fmt.Errorf("invalid size: %d", n)
	}

	if n == 0 {
		// Nothing to do.
		return true, nil
	}

	if n > t.capacity && t.runeSrc == nil && len(t.checkpoints) == 0 {
		// The requested read is larger than the buffer this is not allowed.
		return false, ErrBufferTooSmall
	}

	// If we already have enough data in the buffer, just return.
	if n <= t.w-t.r {
		return true, nil
	}

	// The next read will be beyond the buffer, so we need to release space.
	// Compaction stops at the oldest active checkpoint: bytes a checkpoint may
	// still need to replay are never discarded.
	if t.r+n >= len(t.buf) {
		t.compact()
	}

	var readErr error

	for t.w-t.r < n && readErr == nil {
		// Make room for a rune held over from an earlier window, or for the
		// next read when a checkpoint still needs the bytes already buffered.
		if t.hasPending && len(t.buf)-t.w < encodedRuneLen(t.pendingRune) {
			if err := t.grow(t.w + encodedRuneLen(t.pendingRune)); err != nil {
				readErr = err
				break
			}
		}
		if t.w >= len(t.buf) && (t.runeSrc != nil || len(t.checkpoints) > 0) {
			if err := t.grow(len(t.buf) + 1); err != nil {
				readErr = err
				break
			}
		}

		var (
			bytesRead int
			err       error
		)
		bytesRead, err = t.readInto(t.buf[t.w:])
		t.w += bytesRead
		readErr = err

		if bytesRead == 0 && readErr == nil {
			readErr = io.ErrNoProgress
		}
	}

	if readErr == nil {
		return true, nil
	}

	if !errors.Is(readErr, io.EOF) && t.w-t.r > 0 {
		// The source failed, but buffered bytes are still owed to the caller.
		// Serve them first: the sticky error surfaces once the buffer drains,
		// which is what makes replay from a checkpoint deterministic and keeps
		// a multi-rune fill from reporting a failure mid-buffer.
		return true, nil
	}

	return t.w-t.r >= n, readErr
}

// encodedRuneLen reports how many bytes enc holds once the rune is encoded to
// UTF-8.
func encodedRuneLen(r rune) int {
	if n := utf8.RuneLen(r); n > 0 {
		return n
	}
	// Invalid runes are encoded as the three-byte replacement character.
	return 3
}

// readInto performs one underlying read into p and records a sticky error.
func (t *TextReader) readInto(p []byte) (int, error) {
	if t.readErr != nil {
		return 0, t.readErr
	}

	var (
		n   int
		err error
	)
	if t.runeSrc != nil {
		n, err = t.fillRunes(p)
	} else {
		n, err = t.br.Read(p)
	}

	if err != nil && !errors.Is(err, io.EOF) {
		t.readErr = err
	}

	return n, err
}

// fillRunes reads runes from the rune source and encodes them into p.
//
// A rune that does not fit the room left in p is held in the reader rather than
// dropped, and encoded on the next fill; the caller makes room for it or reports
// the retention limit.
func (t *TextReader) fillRunes(p []byte) (int, error) {
	n := 0

	for n < len(p) {
		var r rune

		if t.hasPending {
			r, t.hasPending = t.pendingRune, false
		} else {
			var err error
			r, _, err = t.runeSrc.ReadRune()
			if err != nil {
				return n, err
			}
		}

		var encoded [utf8.UTFMax]byte
		size := utf8.EncodeRune(encoded[:], r)

		if size > len(p)-n {
			t.pendingRune, t.hasPending = r, true
			return n, nil
		}

		copy(p[n:], encoded[:size])
		n += size
	}

	return n, nil
}

// grow makes room for at least min bytes in the buffer.
//
// Growth is bounded by the retention budget, not here: a checkpoint only forces
// the reader to keep the retained region, and the cursor cannot advance past
// that budget, so the buffer settles instead of growing without bound.
func (t *TextReader) grow(min int) error {
	if min <= len(t.buf) {
		return nil
	}

	newLen := len(t.buf)
	if newLen == 0 {
		newLen = utf8.UTFMax
	}
	for newLen < min {
		newLen *= 2
	}

	grown := make([]byte, newLen)
	copy(grown, t.buf[:t.w])
	t.buf = grown

	return nil
}

// retentionHeadroom reports how many more bytes the logical cursor may advance
// before the retained region would exceed the configured limit. It reports -1
// when no limit applies, which is the case whenever the limit is unset or no
// checkpoint is active.
func (t *TextReader) retentionHeadroom() int {
	if t.maxRetained <= 0 || len(t.checkpoints) == 0 {
		return -1
	}

	return t.maxRetained - (t.pos.Offset() - t.retainFloor())
}

// retainFloor returns the absolute byte offset of the oldest byte the reader
// must keep. Without an active checkpoint nothing needs to be retained and the
// floor is the logical cursor itself.
func (t *TextReader) retainFloor() int {
	floor := t.pos.Offset()

	for _, c := range t.checkpoints {
		if c == nil {
			continue
		}

		// A concurrent Pos or a Remark on another reader may be reading or
		// rewriting these fields right now, so the floor is sampled under
		// the value's own lock. The value is in this reader's active set,
		// so it is still armed on this reader.
		c.mu.Lock()
		if c.active && c.byteOffset < floor {
			floor = c.byteOffset
		}
		c.mu.Unlock()
	}

	return floor
}

// compact shifts the buffer forward, releasing bytes before the retention
// floor. It never advances past the logical cursor.
func (t *TextReader) compact() {
	bufferStart := t.pos.Offset() - t.r

	shift := t.retainFloor() - bufferStart
	if shift <= 0 {
		return
	}
	if shift > t.r {
		shift = t.r
	}

	copy(t.buf, t.buf[shift:t.w])
	t.w -= shift
	t.r -= shift

	t.trimLines()
}

// trimLines drops the per-line position entries no Rewind can still need. The
// oldest byte a Rewind can reach is the physical buffer start: UnreadRune, a
// backward Seek and Checkpoint.Reset all move only within the buffer, and the
// buffer start is never above the oldest active checkpoint. It is called
// wherever the buffer start advances.
func (t *TextReader) trimLines() {
	t.pos.TrimBefore(t.pos.Offset() - t.r)
}

// accountBytes advances the logical position over b, which continues the stream
// at the current position.
//
// The byte offset counts every byte immediately. A trailing incomplete UTF-8
// sequence is carried instead: its bytes are already counted, and its rune and
// column contribution is credited once the sequence completes. That keeps rune
// coordinates exact when a byte read splits a rune.
func (t *TextReader) accountBytes(b []byte) {
	if t.byteMode {
		t.scanLocked(b)
		t.runeOffset += len(b)
		return
	}

	if t.utf8CarryLen > 0 {
		need := encodedLeadLen(t.utf8Carry[0])

		if t.utf8CarryLen+len(b) < need {
			t.pos.AdvanceBytes(len(b))
			copy(t.utf8Carry[t.utf8CarryLen:], b)
			t.utf8CarryLen += len(b)
			return
		}

		take := need - t.utf8CarryLen
		copy(t.utf8Carry[t.utf8CarryLen:], b[:take])

		// Undo the provisional byte advance for the carried bytes and account
		// the completed sequence as a whole.
		t.pos.AdvanceBytes(-t.utf8CarryLen)
		t.pos.Scan(t.utf8Carry[:need])
		t.runeOffset += utf8.RuneCount(t.utf8Carry[:need])
		t.utf8CarryLen = 0

		b = b[take:]
	}

	i := 0
	for i < len(b) && utf8.FullRune(b[i:]) {
		_, size := utf8.DecodeRune(b[i:])
		i += size
	}

	t.pos.Scan(b[:i])
	t.runeOffset += utf8.RuneCount(b[:i])

	if i < len(b) {
		t.pos.AdvanceBytes(len(b) - i)
		copy(t.utf8Carry[:], b[i:])
		t.utf8CarryLen = len(b) - i
	}
}

// scanLocked advances the logical position over b, which continues the stream
// at the position. It updates the line, column and byte offset, not the rune
// offset. In text mode b is decoded as UTF-8. In byte mode each byte counts as
// one rune.
func (t *TextReader) scanLocked(b []byte) {
	if !t.byteMode {
		t.pos.Scan(b)
		return
	}

	// position.Position.Scan decodes UTF-8, and it decodes a byte that does not
	// start a complete, valid sequence as utf8.RuneError of size 1. Only the
	// bytes 0xC2 to 0xF4 can start a valid multibyte sequence. Each segment
	// below ends at the first such byte, which has no byte after it in the
	// segment. So Scan counts every byte of the segment as one rune, and the
	// position API needs no byte-mode variant.
	for len(b) > 0 {
		i := 0
		for i < len(b) {
			c := b[i]
			i++
			if c >= 0xC2 && c <= 0xF4 {
				break
			}
		}
		t.pos.Scan(b[:i])
		b = b[i:]
	}
}

// runeLookahead reports how many bytes a rune read asks the buffer for: one in
// byte mode, a maximal UTF-8 rune otherwise. Byte mode never waits on the
// source for bytes it does not need.
func (t *TextReader) runeLookahead() int {
	if t.byteMode {
		return 1
	}
	return utf8.UTFMax
}

// flushCarry credits a carried incomplete sequence as if it had been decoded on
// its own. Operations other than Read call it first, so the position they start
// from includes every byte read so far.
func (t *TextReader) flushCarry() {
	if t.utf8CarryLen == 0 {
		return
	}

	t.pos.AdvanceBytes(-t.utf8CarryLen)
	t.pos.Scan(t.utf8Carry[:t.utf8CarryLen])
	t.runeOffset += utf8.RuneCount(t.utf8Carry[:t.utf8CarryLen])
	t.utf8CarryLen = 0
}

// encodedLeadLen reports how many bytes belong to the UTF-8 sequence that starts
// with the leading byte b. A byte that cannot start a sequence reports one, so
// it is accounted as a single replacement rune.
func encodedLeadLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b&0xE0 == 0xC0:
		return 2
	case b&0xF0 == 0xE0:
		return 3
	case b&0xF8 == 0xF0:
		return 4
	default:
		return 1
	}
}

// ReadRune reads a single UTF-8 encoded Unicode character and returns the rune
// and its size in bytes.
func (t *TextReader) ReadRune() (r rune, size int, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	located, err := t.readRuneLocked()
	if err != nil {
		return 0, 0, err
	}

	return located.r, located.size, nil
}

func (t *TextReader) readRuneLocked() (LocatedRune, error) {
	t.flushCarry()

	// Try to fill the buffer with at least enough bytes for a maximal rune.
	// We can tolerate an io.EOF here, as we might have a partial buffer to read from.
	_, err := t.fillAtLeast(t.runeLookahead())
	if err != nil && !errors.Is(err, io.EOF) {
		return LocatedRune{}, err
	}

	// If the buffer is empty after trying to fill, we are at the end of the stream.
	if t.r >= t.w {
		return LocatedRune{}, io.EOF
	}

	// In byte mode every byte is one rune of size 1. Otherwise let
	// utf8.DecodeRune handle all cases: valid ASCII, valid multi-byte, and
	// invalid UTF-8 sequences. If the sequence is invalid, it returns
	// (utf8.RuneError, 1).
	var (
		r    rune
		size int
	)
	if t.byteMode {
		r, size = rune(t.buf[t.r]), 1
	} else {
		r, size = utf8.DecodeRune(t.buf[t.r:t.w])
	}

	// The retention budget bounds the retained region, so it is enforced where
	// the logical cursor would advance. A rune larger than the remaining budget
	// is reported whole: it is never half-decoded and the cursor does not move.
	if headroom := t.retentionHeadroom(); headroom >= 0 && size > headroom {
		return LocatedRune{}, ErrRetentionExceeded
	}

	// Advance the reader's position. This is crucial.
	// For an invalid byte, size will be 1, allowing us to skip it and continue.
	// Scan reports where the rune started, which is the located rune's position.
	line, column, offset := t.pos.Scan(t.buf[t.r : t.r+size])

	located := LocatedRune{
		r:    r,
		size: size,
		pos: Pos{
			byteOffset: offset,
			runeOffset: t.runeOffset,
			line:       line,
			column:     column,
		},
	}

	t.r += size
	t.runeOffset++

	// Update state to allow for UnreadRune.
	t.lastRuneSize = size

	// The error is nil because we successfully "read" a rune from the stream,
	// even if that rune is the replacement/error character. The caller is
	// responsible for checking if r == utf8.RuneError.
	return located, nil
}

// ReadLocatedRune reads the next rune and reports it together with the immutable
// position it started at. The returned located rune describes a committed read:
// it is not changed by later read-ahead.
func (t *TextReader) ReadLocatedRune() (LocatedRune, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.readRuneLocked()
}

// PeekRune reports the rune at the logical cursor without consuming it. The
// logical cursor is unchanged on return, so the rune can be read again with
// ReadRune or ReadLocatedRune.
//
// The reported location is the one the next read will return: a pending partial
// UTF-8 sequence is projected onto the reported position, so a peek never
// describes a location the committed read would not, and the stored logical
// cursor is left exactly as it was. Because peeking is not a read, it also ends
// the single-level UnreadRune authority, exactly as a read other than ReadRune
// would.
func (t *TextReader) PeekRune() (LocatedRune, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.lastRuneSize = -1

	_, err := t.fillAtLeast(t.runeLookahead())
	if err != nil && !errors.Is(err, io.EOF) {
		return LocatedRune{}, err
	}

	if t.r >= t.w {
		return LocatedRune{}, io.EOF
	}

	if t.byteMode {
		return LocatedRune{r: rune(t.buf[t.r]), size: 1, pos: t.cursorLocked()}, nil
	}

	r, size := utf8.DecodeRune(t.buf[t.r:t.w])

	return LocatedRune{r: r, size: size, pos: t.projectedCursorLocked()}, nil
}

// UnreadRune unreads the last rune read by ReadRune. It is an error to call
// UnreadRune if the most recent method called on the TextReader was not
// ReadRune.  Only one level of unread is supported.
func (t *TextReader) UnreadRune() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.flushCarry()

	if t.lastRuneSize < 0 || t.r < t.lastRuneSize {
		return bufio.ErrInvalidUnreadRune
	}

	if err := t.pos.Rewind(t.lastRuneSize, 1); err != nil {
		return fmt.Errorf("rewind: %w", err)
	}

	t.r -= t.lastRuneSize
	if t.runeOffset > 0 {
		t.runeOffset--
	}
	t.lastRuneSize = -1

	return nil
}

// Read reads up to len(p) bytes into p and returns the number of bytes read.
// For reads larger than the buffer capacity, it will read directly from the
// underlying reader, and discard any previously buffered data. The direct path
// is never taken while a checkpoint is active: those bytes may still be needed
// for replay, so they are buffered instead.
func (t *TextReader) Read(p []byte) (n int, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	needed, filled := len(p), 0

	// The retention budget is measured against the bytes the cursor would
	// actually advance over, not the size of the caller's destination. `Read`
	// may legitimately return fewer bytes than requested, so make the buffer
	// show what is really available before judging the request over budget. An
	// over-budget read still does no partial work.
	if headroom := t.retentionHeadroom(); headroom >= 0 && needed > headroom {
		_, _ = t.fillAtLeast(headroom + 1)

		avail := t.w - t.r
		want := needed
		if avail < want {
			want = avail
		}
		if want > headroom {
			return 0, ErrRetentionExceeded
		}
	}

	var readErr error

	for filled < needed {
		buffered := t.w - t.r

		if buffered > 0 {
			// We have data some in the buffer. move as much data as possible from it
			// to `p`

			n = needed - filled
			if n > buffered {
				n = buffered
			}

			copy(p[filled:], t.buf[t.r:t.r+n])
			t.accountBytes(p[filled : filled+n])
			t.r += n

			filled += n
		}

		if readErr != nil {
			break
		}

		// The size of the requested read is larger than the buffer, there's no way
		// we can handle this
		if needed-filled > t.capacity && t.br != nil && len(t.checkpoints) == 0 {

			// Read remaining data directly into p, through the same sticky
			// bookkeeping as the buffered path so an error returned alongside
			// data is remembered and surfaced to the caller and later reads.
			n, readErr = t.readInto(p[filled:])

			// Reset the buffer since we dumped it all into p. Nothing behind
			// the cursor stays rewindable once this Read returns, so the line
			// table is trimmed to the cursor after every capacity-sized chunk
			// instead of growing with len(p).
			t.r = 0
			t.w = 0
			t.lastRuneSize = -1

			for done := 0; done < n; {
				end := min(done+t.capacity, n)
				t.accountBytes(p[filled+done : filled+end])
				t.trimLines()
				done = end
			}

			filled += n

			break
		}

		// Fill the buffer with more data for the next read
		_, readErr = t.fillAtLeast(needed - filled)
	}

	t.lastRuneSize = -1

	// An error returned alongside data must reach the caller rather than being
	// deferred, which is what io.Reader requires.
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return filled, readErr
	}

	if filled == 0 && readErr != nil {
		return 0, readErr
	}

	return filled, nil
}

// Seek sets the offset for the next Read or ReadRune, interpreting offset and
// whence according to the io.Seeker interface. This Seek implementation
// operates only on the data currently held in the reader's buffer. It cannot
// seek backwards to data that has already been read and discarded from the
// buffer. An attempt to seek to a position before the start of the current
// buffer will result in an ErrSeekOutOfBuffer.  It does not perform a seek on
// the underlying io.Reader.
func (t *TextReader) Seek(offset int64, whence int) (int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	var newR int64 // new read pointer relative to start of t.buf

	switch whence {
	case io.SeekStart:
		if offset < 0 {
			return 0, errors.New("textreader: negative position")
		}

		// Calculate the absolute stream offset that corresponds to the start of our buffer.
		bufferStartOffset := int64(t.pos.Offset() - t.r)

		// If the target absolute offset is before the start of our buffered data, we cannot seek there.
		if offset < bufferStartOffset {
			return 0, ErrSeekOutOfBuffer
		}

		// Calculate new read pointer relative to the buffer.
		newR = offset - bufferStartOffset

	case io.SeekCurrent:
		newR = int64(t.r) + offset
	case io.SeekEnd:
		newR = int64(t.w) + offset
	default:
		return 0, errors.New("textreader: invalid whence")
	}

	if newR < 0 {
		return 0, errors.New("textreader: negative position")
	}

	rel := newR - int64(t.r)
	if rel == 0 {
		// A seek that does not move the cursor is a no-op: it must not settle a
		// pending partial UTF-8 sequence or otherwise change the position.
		return int64(t.pos.Offset()), nil
	}

	// A seek that does move is a position-changing operation, so pending partial
	// UTF-8 accounting is settled before the buffer is re-pointed.
	t.flushCarry()

	// Check bounds before converting to int for buffer operations
	if newR > int64(len(t.buf)) {
		return 0, ErrSeekOutOfBuffer
	}
	newRInt := int(newR)
	relInt := int(rel)

	if rel > 0 { // Seeking Forward
		if headroom := t.retentionHeadroom(); headroom >= 0 && relInt > headroom {
			return 0, ErrRetentionExceeded
		}
		if relInt > len(t.buf) {
			return 0, ErrSeekOutOfBuffer
		}

		bytesAvailable := t.w - t.r
		if relInt > bytesAvailable {
			if _, err := t.fillAtLeast(relInt); err != nil && !errors.Is(err, io.EOF) {
				return 0, fmt.Errorf("fillAtLeast: %w", err)
			}
		}

		if t.r+relInt > t.w {
			return 0, ErrSeekOutOfBuffer
		}

		t.scanLocked(t.buf[t.r : t.r+relInt])
		t.runeOffset += t.runeCount(t.buf[t.r : t.r+relInt])
		t.r += relInt

	} else { // Seeking Backward
		if newRInt < 0 {
			return 0, ErrSeekOutOfBuffer
		}
		// Count runes in the slice we're rewinding over
		runeCount := t.runeCount(t.buf[newRInt:t.r])
		if err := t.pos.Rewind(-relInt, runeCount); err != nil {
			return 0, fmt.Errorf("pos.Rewind: %w", err)
		}
		t.runeOffset -= runeCount
		if t.runeOffset < 0 {
			t.runeOffset = 0
		}
		t.r += relInt
	}

	t.lastRuneSize = -1

	return int64(t.pos.Offset()), nil
}

// runeCount reports how many runes the reader counts in b: one per byte in byte
// mode, the UTF-8 rune count otherwise.
func (t *TextReader) runeCount(b []byte) int {
	if t.byteMode {
		return len(b)
	}
	return utf8.RuneCount(b)
}

// Pos returns a copy of the reader's current position (line, column, and
// offset).  Modifying the returned Position will not affect the reader's
// state.
//
// Pos reports the logical next-read position: bytes buffered for look-ahead are
// never reflected in it.
//
// The reader drops the line positions of input behind the start of its buffer
// (see position.Position.TrimBefore), and the returned copy holds the same line
// positions. Rewind on the copy is exact for any target at or after the buffer
// start; a target further back can return an error. A Rewind to offset 0 still
// resets the copy.
func (t *TextReader) Pos() *position.Position {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.pos.Copy()
}

// Cursor returns the immutable logical position of the next read. It is the
// value form of Pos.
func (t *TextReader) Cursor() Pos {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.cursorLocked()
}

func (t *TextReader) cursorLocked() Pos {
	line, column, offset := t.pos.State()

	return Pos{
		byteOffset: offset,
		runeOffset: t.runeOffset,
		line:       line,
		column:     column,
	}
}

// projectedCursorLocked reports the logical cursor as the next settling
// operation would leave it. Bytes of an incomplete UTF-8 sequence that a chunk
// carried over already count toward the byte offset; their rune and column
// contribution is projected here on a copy, so inspecting a peek can describe
// the rune the next read will return without moving the stored cursor.
func (t *TextReader) projectedCursorLocked() Pos {
	pos := t.cursorLocked()
	if t.utf8CarryLen == 0 {
		return pos
	}

	carried := t.utf8Carry[:t.utf8CarryLen]

	projected := t.pos.Copy()
	projected.ScanRunes(carried)
	line, column, offset := projected.State()

	return Pos{
		byteOffset: offset,
		runeOffset: t.runeOffset + utf8.RuneCount(carried),
		line:       line,
		column:     column,
	}
}
