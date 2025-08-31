package main

import (
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/xiam/textreader"
)

const contextBytes = 20

const document = `This is a simple document.
It has multiple lines of standard text.
Our goal is to find the special marker * right here.
The rest of the document follows.`

func main() {
	reader := textreader.New(strings.NewReader(document))

	fmt.Println("Searching for marker '*' in the document...")

	// Read rune by rune until we find our marker or reach the end.
	for {
		r, _, err := reader.ReadRune()
		if err != nil {
			if err == io.EOF {
				fmt.Println("Marker not found.")
				break
			}
			log.Fatalf("Error reading rune: %v", err)
		}

		// We found the marker.
		if r == '*' {
			markerPos := reader.Pos()
			fmt.Printf("Found marker at Line %d, Column %d (Offset: %d)\n",
				markerPos.Line(), markerPos.Column(), markerPos.Offset())

			_, err := reader.Seek(-contextBytes, io.SeekCurrent)
			if err != nil {
				// If the marker is too close to the start, seek might fail.
				// In that case, we just go to the very beginning of the
				// stream.
				_, _ = reader.Seek(0, io.SeekStart)
			}

			contextBuffer := make([]byte, contextBytes*2)
			n, _ := reader.Read(contextBuffer)

			fmt.Printf(
				"Context around marker: %q\n",
				contextBuffer[:n],
			)

			return
		}
	}
}
