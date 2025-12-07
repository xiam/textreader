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
- **Multiple Read Methods**: Read by rune, byte, or arbitrary chunks
- **Unread Support**: Single-level unread operations for both runes and bytes
- **Seeking**: Navigate to specific positions within the buffered data

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

## Important Limitations

- **Seeking is limited to buffered data only.** Unlike `os.File.Seek()`, this
  implementation cannot seek to arbitrary positions in the underlying stream.
  It can only move within the data currently held in the reader's buffer.
- **`Seek(0, io.SeekStart)` may fail** if the beginning of the stream has
  already been read and discarded from the buffer.
- Seeking **does not affect the underlying `io.Reader`**.
- **Only single-level unread operations are supported.** You can only unread
  the most recently read rune or byte. Calling `UnreadRune` or `UnreadByte`
  twice in a row without an intermediate read will result in an error.
- **Position tracking assumes UTF-8 encoded text.** While the reader can
  process any byte stream, the line and column counts will only be accurate for
  valid UTF-8 text.
- **Column counts runes, Offset counts bytes.** `Column()` returns the number of
  Unicode characters (runes) since the last newline. `Offset()` returns the
  total number of bytes read from the stream.

## License

MIT License
