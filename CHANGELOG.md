# Changelog

This file documents the notable changes to `textreader`. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). This project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

## v0.2.1 - 2026-09-25

No library changes. Repository maintenance only.

## v0.2.0 - 2026-09-22

### Added

- Add a positioned cursor layer on top of the existing buffered reader:
  immutable `Pos`, `Span`, and `LocatedRune` values, `ReadLocatedRune` and
  `PeekRune`, non-mutating `SpanText` and `Context` access, and `Cursor` for the
  logical next-read position.
- Add checkpoints. `Checkpoint` marks a logical position; `Reset` restores it and
  replays the input exactly across multibyte UTF-8, newlines, and end of input;
  `Commit` and `Release` end the retention the checkpoint required. Checkpoints
  nest, and the reader retains input from the oldest active one.
- Add `NewReader` and `NewRuneReader`, with `WithCapacity` and `WithMaxRetained`
  options. `WithMaxRetained` bounds the input retained for checkpoint replay and
  fails a read with `ErrRetentionExceeded` instead of silently dropping data. A
  reader with no active checkpoint pays no retention cost.
- Add `ErrRetentionExceeded`, `ErrCheckpointReleased`, `ErrCheckpointRewound`,
  and `ErrPositionOutOfBuffer`.
- Add `position.Position.AdvanceBytes`, which moves the byte offset without
  touching the line or rune column, for bytes whose rune contribution is not yet
  known.
- Add `position.Position.State`, and make `position.Position.Scan` return the
  line, column, and offset it started from, so a caller that advances a position
  can read where it was in the same step.
- Add the `_examples/positioned-cursor` program, cursor tests, and benchmarks for
  sequential reads, checkpoint replay, retained long tokens, and rune sources.

### Changed

- `Read`, `ReadRune`, `UnreadRune`, `Seek`, and `Pos` keep their existing
  behavior and signatures. The cursor layer is additive, and a reader with no
  active checkpoint compacts exactly as before.
- A failure from the underlying source is sticky: replaying from a checkpoint
  reproduces the same error instead of reading whatever the source reports on a
  second pass.
- `ReadRune` and `PeekRune` now serve buffered bytes before reporting a
  non-EOF error from the underlying source, so an error is observed once the
  buffer drains rather than mid-buffer.
- `Read` keeps rune and column coordinates exact when a chunk ends inside a
  multi-byte rune: the byte offset counts every byte immediately, and the
  incomplete sequence's rune contribution is credited once the sequence
  completes.
- A rune that does not fit the reader's remaining room is held and encoded on
  the next fill instead of being dropped, so a rune source loses nothing at a
  buffer boundary.
- The retention limit is enforced against the next complete rune rather than a
  four-byte look-ahead reserve, so a limit large enough for the next rune is
  honored; the limit error is reported only when the caller needs more than the
  reader may retain.
- The retention limit is enforced where the logical cursor advances, so it also
  bounds bytes that were buffered before a checkpoint started. A rune larger
  than the remaining budget, and a request that would truly advance past it, now
  report `ErrRetentionExceeded` without moving the cursor or returning partial
  data; the budget is judged against the bytes that would really advance the
  cursor, so a large `Read` still returns the few bytes available. Read-ahead
  ahead of the cursor is bounded by the buffer capacity.
- `Context` clamps both edges by comparison rather than by addition, so a very
  large but valid context width no longer overflows and panics.
- The direct bulk-read path now shares the sticky source-error bookkeeping of the
  buffered path, so an error returned together with data reaches the caller and
  is remembered by later reads, as `io.Reader` requires.
- `PeekRune` settles carried bytes of an incomplete UTF-8 sequence before
  reporting a location, so a peek always describes the same located rune the next
  read returns, and it ends the single-level `UnreadRune` authority rather than
  leaving an earlier `ReadRune` available to undo.
- Non-consuming operations no longer move the logical cursor. `PeekRune`
  projects a pending partial UTF-8 sequence onto the location it reports without
  storing it, `Checkpoint` records the cursor exactly as it stands, and a zero
  relative `Seek` is a true no-op, so a position saved before any of them equals
  the position after and equals the mark. Added `position.Position.ScanRunes` for
  that projection.

## v0.1.4 - 2026-09-08

No library changes. Repository maintenance only.

## v0.1.3 - 2026-08-09

No library changes. Repository maintenance only.

## v0.1.2 - 2026-08-08

### Changed

- Document every exported symbol in the `position` package, so godoc describes
  the full API.

## v0.1.1 - 2026-08-01

`v0.1.1` and `v0.1.0` name the same commit. The published files are identical
between the two tags. This release changes nothing.

## v0.1.0 - 2026-07-28

Initial release.

### Added

- Add `TextReader`, a buffered reader that tracks the line, column, and byte
  offset of the current read.
- Add `New()` and `NewWithCapacity()` to create a reader over any `io.Reader`.
- Add `ReadRune()` to read one UTF-8 rune and report its size in bytes.
- Add `Read()` to read an arbitrary byte chunk.
- Add `UnreadRune()` to unread the last rune. The reader supports one level of
  unread.
- Add `Seek()` to move the read position inside the buffered data.
- Add `Pos()` to return a copy of the current position.
- Add the `position` package, which tracks the line, column, and offset of a
  UTF-8 stream.
- Add rune-based column counting, so a multi-byte character reports its
  character position. `Offset()` still counts bytes.
