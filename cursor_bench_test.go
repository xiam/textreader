package textreader_test

import (
	"io"
	"strings"
	"testing"

	"github.com/xiam/textreader"
)

var (
	asciiDoc = strings.Repeat("the quick brown fox jumps over the lazy dog\n", 512)
	utf8Doc  = strings.Repeat("αβγ 世界 🦄 ζηθ\n", 512)
)

func BenchmarkReadRuneASCII(b *testing.B) {
	b.SetBytes(int64(len(asciiDoc)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tr := textreader.New(strings.NewReader(asciiDoc))
		for {
			if _, _, err := tr.ReadRune(); err != nil {
				break
			}
		}
	}
}

func BenchmarkReadRuneUTF8(b *testing.B) {
	b.SetBytes(int64(len(utf8Doc)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tr := textreader.New(strings.NewReader(utf8Doc))
		for {
			if _, _, err := tr.ReadRune(); err != nil {
				break
			}
		}
	}
}

func BenchmarkReadLocatedRuneUTF8(b *testing.B) {
	b.SetBytes(int64(len(utf8Doc)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tr := textreader.New(strings.NewReader(utf8Doc))
		for {
			if _, err := tr.ReadLocatedRune(); err != nil {
				break
			}
		}
	}
}

// BenchmarkCheckpointReplay measures the speculative read-ahead pattern a lexer
// uses: mark, read a short window, reset, repeat.
func BenchmarkCheckpointReplay(b *testing.B) {
	const window = 16

	b.SetBytes(int64(len(asciiDoc)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tr := textreader.New(strings.NewReader(asciiDoc))
		for {
			cp := tr.Checkpoint()
			n := 0
			for n < window {
				if _, err := tr.ReadLocatedRune(); err != nil {
					break
				}
				n++
			}
			if n == 0 {
				_ = cp.Release()
				break
			}
			_ = cp.Reset()
			for j := 0; j < n; j++ {
				if _, err := tr.ReadLocatedRune(); err != nil {
					b.Fatal(err)
				}
			}
			_ = cp.Commit()
		}
	}
}

func BenchmarkSpanText(b *testing.B) {
	tr := textreader.New(strings.NewReader(asciiDoc))
	cp := tr.Checkpoint()
	defer func() { _ = cp.Release() }()

	var spans []textreader.Span
	for {
		lr, err := tr.ReadLocatedRune()
		if err != nil {
			break
		}
		spans = append(spans, lr.Span())
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		s := spans[i%len(spans)]
		if _, err := tr.SpanText(s); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLongTokenRetention measures retaining one token that is far larger
// than the reader's capacity, which is what a maximal-munch lexer does.
func BenchmarkLongTokenRetention(b *testing.B) {
	const tokenSize = 1 << 20
	input := strings.Repeat("x", tokenSize) + "\n"

	b.SetBytes(int64(len(input)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tr := textreader.NewReader(strings.NewReader(input), textreader.WithCapacity(4096))
		cp := tr.Checkpoint()
		n := 0
		for {
			if _, err := tr.ReadLocatedRune(); err != nil {
				break
			}
			n++
		}
		if n != tokenSize+1 {
			b.Fatalf("read %d runes, want %d", n, tokenSize+1)
		}
		_ = cp.Reset()
		_ = cp.Release()
	}
}

func BenchmarkRuneReaderCursor(b *testing.B) {
	runes := []rune(utf8Doc)
	b.SetBytes(int64(len(utf8Doc)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tr := textreader.NewRuneReader(&sliceRuneSource{runes: runes})
		for {
			if _, err := tr.ReadLocatedRune(); err != nil {
				break
			}
		}
	}
}

type sliceRuneSource struct {
	runes []rune
	pos   int
}

func (s *sliceRuneSource) ReadRune() (rune, int, error) {
	if s.pos >= len(s.runes) {
		return 0, 0, io.EOF
	}
	r := s.runes[s.pos]
	s.pos++
	return r, len(string(r)), nil
}
