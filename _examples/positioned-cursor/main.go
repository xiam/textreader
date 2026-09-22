// Command positioned-cursor shows the cursor layer of textreader: reading a
// bounded window ahead of the logical cursor, replaying it exactly, and lifting
// diagnostic context out of the retained input without moving the cursor.
package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/xiam/textreader"
)

const document = `theme: dark
name: 世界
broken: 🦄
`

// readRule is the shape a tiny line-oriented scanner needs: read ahead until
// the newline, but do not commit to the bytes until the whole line is known.
func readRule(tr *textreader.TextReader) (textreader.Span, string, error) {
	mark := tr.Checkpoint()
	defer func() { _ = mark.Release() }()

	var (
		first textreader.LocatedRune
		last  textreader.LocatedRune
		seen  bool
	)

	for {
		lr, err := tr.ReadLocatedRune()
		if err != nil {
			if errors.Is(err, io.EOF) && seen {
				return spanOf(first, last), "", nil
			}
			return textreader.Span{}, "", err
		}
		if !seen {
			first, seen = lr, true
		}
		last = lr

		if lr.Rune() == '\n' {
			break
		}
	}

	return spanOf(first, last), "", nil
}

func spanOf(first, last textreader.LocatedRune) textreader.Span {
	return textreader.NewSpan(first.Pos(), last.End())
}

func main() {
	tr := textreader.NewReader(strings.NewReader(document))

	for {
		// Hold a checkpoint across the whole read-ahead so the span stays
		// recoverable after the cursor has moved past it.
		mark := tr.Checkpoint()

		start := tr.Cursor()
		span, _, err := readRule(tr)
		if err != nil {
			_ = mark.Release()
			if errors.Is(err, io.EOF) {
				break
			}
			panic(err)
		}

		text, err := tr.SpanText(span)
		if err != nil {
			panic(err)
		}

		context, err := tr.Context(span, 4, 4)
		if err != nil {
			panic(err)
		}

		end := tr.Cursor()
		fmt.Printf("line starting at %s: %q\n", start, strings.TrimRight(text, "\n"))
		fmt.Printf("  span: bytes %d..%d, runes %d..%d, %s>%s\n",
			span.Start().ByteOffset(), span.End().ByteOffset(),
			span.Start().RuneOffset(), span.End().RuneOffset(),
			span.Start(), span.End())
		fmt.Printf("  context: %q\n", strings.ReplaceAll(context, "\n", "\\n"))
		fmt.Printf("  next read is at %s (byte %d)\n\n", end, end.ByteOffset())

		// Release once the span is no longer needed. Until this point the
		// bytes behind the cursor were retained; afterwards the reader
		// compacts them away.
		_ = mark.Release()
	}

	fmt.Println("done")
}
