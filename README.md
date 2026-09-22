# TextReader

The `TextReader` package reads Unicode text files while tracking the position
of the current read and the surrounding text. This is useful for parsers,
lexers, and any application that benefits from providing detailed context
around the current read position.

## Features

- **Position Tracking**: Automatically tracks line, column (in runes), and byte
  offset as you read
- **Unicode Support**: Properly handles UTF-8 encoded text including multi-byte
  characters. Column counts characters (runes), not bytes, so a line with
  `"hello 🌍"` reports column 7 after reading the emoji, not column 10.
- **Multiple Read Methods**: Read by rune or arbitrary byte chunks
- **Unread Support**: Single-level unread for runes via `UnreadRune()`
- **Seeking**: Navigate to specific positions within the buffered data using `Seek()`
- **Positioned cursor**: Immutable positions, spans, and located runes; logical
  checkpoints with exact replay; and non-mutating access to the retained text —
  see [Positioned cursor](#positioned-cursor)

## Use Cases

This package is ideal for:
- **Text parsers and lexers** that need precise error location reporting
- **Configuration file readers** where you need to report line/column of syntax errors
- **Template processors** that need to track position for error messages

This package is **not suitable** for:
- Random access to large files (use `os.File` instead)
- High-performance streaming where position tracking isn't needed
- Binary data processing (designed for UTF-8 text)

## Code Example

```go
package main

import (
    "fmt"
    "io"
    "log"
    "strings"

    "github.com/xiam/textreader"
)

func main() {
    data := "Hello\nWorld 🌍"
    reader := textreader.New(strings.NewReader(data))

    for {
        r, _, err := reader.ReadRune()
        if err != nil {
            if err == io.EOF {
                break // We've reached the end of the file
            }
            log.Fatalf("Error reading rune: %v", err) // Handle unexpected errors
        }

        pos := reader.Pos()
        fmt.Printf("Rune: %c at line %d, column %d\n",
            r, pos.Line(), pos.Column())
    }
}
```

## Positioned cursor

The cursor layer is for callers that must read ahead before they commit to what
they read: lexers running rules in parallel, maximal-munch scanners, or any
parser that needs to peek beyond the current token. It is built on the same
buffered reader as the basic layer, so `Read`, `ReadRune`, `UnreadRune`, `Seek`,
and `Pos` keep working unchanged.

```go
tr := textreader.New(strings.NewReader("hello 世界\n"))

mark := tr.Checkpoint()          // start retaining input from here
defer mark.Release()

first, _ := tr.ReadLocatedRune() // lr is a rune plus the position it came from
last := first
for last.Rune() != '\n' {
    last, _ = tr.ReadLocatedRune()
}

span := textreader.NewSpan(first.Pos(), last.End())
text, _ := tr.SpanText(span)          // "hello 世界\n" — the cursor has not moved
around, _ := tr.Context(span, 4, 4)   // a little text on each side

_ = mark.Reset()                      // replay the same runes exactly
```

### What the cursor layer adds

- **`Pos`** — an immutable position: byte offset, rune offset, 1-based line, and
  0-based rune column. `Cursor()` returns the logical next-read position.
- **`Span`** — an immutable half-open range, built with `NewSpan`.
- **`LocatedRune`** — a rune with the position it was read from, returned by
  `ReadLocatedRune`. `PeekRune` reports the rune at the cursor without consuming
  it.
- **`Checkpoint`** — `Checkpoint()` marks the logical position. `Reset()` returns
  the cursor to it and replays the input exactly, including across multi-byte
  runes, newlines, and end of input. `Commit()` accepts the current position and
  drops the retained input; `Release()` abandons the checkpoint whatever the
  cursor is. A checkpoint may be reset repeatedly until it is committed or
  released; after that, every method on it returns `ErrCheckpointReleased`.
- **`SpanText` / `Context`** — non-mutating access to the retained bytes, for
  diagnostics. Neither moves the cursor.
- **`Cursor()` / `Pos()`** — the logical position, never physical read-ahead.
  Bytes pulled into the buffer for look-ahead are invisible until they are
  actually read.

### Checkpoints, retention, and invalidation

While at least one checkpoint is active the reader retains every byte from the
oldest active checkpoint onward, so a reset can always replay. That has three
consequences worth knowing:

- **Nested checkpoints nest their retention.** The reader keeps input from the
  oldest active checkpoint; releasing an inner checkpoint does not release the
  input an outer one still needs.
- **The cursor may not seek behind the oldest active checkpoint's data.** `Seek`
  still only moves within buffered data, and buffered data now starts at the
  oldest checkpoint.
- **Retention ends when the last relevant checkpoint is committed or released.**
  Up to that point, spans from the retained region stay readable by `SpanText`
  and `Context`. Afterwards those bytes are no longer retained and the same call
  returns `ErrPositionOutOfBuffer` — deterministically, even if the bytes happen
  to still sit in the buffer. Recoverable spans are the ones built from located
  runes read while a checkpoint was open.

`Commit` additionally refuses when the logical cursor is behind the mark
(`ErrCheckpointRewound`), because the replayed range has not been decided yet;
`Release` has no such precondition.

### Bounding retained input

`WithMaxRetained(n)` caps the bytes held for checkpoint replay:

```go
tr := textreader.NewReader(src,
    textreader.WithCapacity(4096),
    textreader.WithMaxRetained(1<<20),
)
```

- The limit applies **only while a checkpoint is active**. A basic reader with
  no checkpoint retains nothing extra and pays no cost, which is why the default
  is unbounded.
- It bounds the **retained region**: the bytes from the oldest active checkpoint
  to the logical cursor, which is exactly what a `Reset` has to replay. It is
  enforced where the cursor would advance, so a read that would exceed the limit
  fails with `ErrRetentionExceeded`, moves the cursor by nothing, and consumes
  nothing. The budget is judged against the bytes that would really advance the
  cursor, not the size of the caller's destination, so a large `Read` still
  returns the few bytes that are available; only a request that would truly
  advance past the budget is refused whole. A rune that cannot fit the remaining
  budget is reported whole — never half-decoded.
- Read-ahead **in front of** the cursor is bounded by the buffer capacity rather
  than by this limit, and it stays bounded while the cursor cannot advance.
- Without a limit, a checkpoint held across a large read-ahead grows the buffer
  as needed, which is what makes a long token recoverable.

### Rune sources

`NewRuneReader` wraps an `io.RuneReader` in the same cursor. The runes are
encoded back into the reader's buffer so byte offsets remain meaningful; no
bytes are decoded, because the caller's rune reader already did that. Existing
`textlexer`-style callers that only implement `io.RuneReader` keep working this
way, and byte-stream callers use `New`/`NewReader` and never pay for a second
decoder.

A reader failure from the underlying source is sticky: replaying from a
checkpoint reproduces the same error instead of whatever the source would report
on a second pass.

See `_examples/positioned-cursor` for a runnable program.

## Important Limitations

- **Seeking is limited to buffered data only.** Unlike `os.File.Seek()`, this
  implementation cannot seek to arbitrary positions in the underlying stream.
  It can only move within the data currently held in the reader's buffer.
- **`Seek(0, io.SeekStart)` may fail** if the beginning of the stream has
  already been read and discarded from the buffer.
- Seeking **does not affect the underlying `io.Reader`**.
- **Only single-level unread operations are supported.** You can only unread
  the most recently read rune via `UnreadRune()`. Calling it twice in a row
  without an intermediate read will result in an error. Use `Seek()` for more
  flexible backward navigation.
- **Position tracking assumes UTF-8 encoded text.** While the reader can
  process any byte stream, the line and column counts will only be accurate for
  valid UTF-8 text.
- **Column counts runes, Offset counts bytes.** `Column()` returns the number of
  Unicode characters (runes) since the last newline. `Offset()` returns the
  total number of bytes read from the stream.

## License

MIT License
