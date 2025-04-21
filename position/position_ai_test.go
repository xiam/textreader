package position_test // Use _test package convention

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/textreader/position"
)

// Helper function to check position state
func assertPositionState(t *testing.T, p *position.Position, expectedLine, expectedCol, expectedOffset int, msgAndArgs ...interface{}) {
	t.Helper()
	assert.Equal(t, expectedLine, p.Line(), "Line mismatch: %v", msgAndArgs)
	assert.Equal(t, expectedCol, p.Column(), "Column mismatch: %v", msgAndArgs)
	assert.Equal(t, expectedOffset, p.Offset(), "Offset mismatch: %v", msgAndArgs)
	assert.Equal(t, fmt.Sprintf("%d:%d", expectedLine, expectedCol), p.String(), "String() mismatch: %v", msgAndArgs)
}

func TestNew(t *testing.T) {
	p := position.New()
	require.NotNil(t, p)
	assertPositionState(t, p, 1, 0, 0, "Initial state")
}

func TestScan_Empty(t *testing.T) {
	p := position.New()
	p.Scan([]byte{})
	assertPositionState(t, p, 1, 0, 0, "After scanning empty slice")
}

func TestScan_SingleLineNoNewline(t *testing.T) {
	p := position.New()
	data := []byte("hello")
	p.Scan(data)
	assertPositionState(t, p, 1, 5, 5, "After scanning 'hello'")
}

func TestScan_SingleLineWithNewline(t *testing.T) {
	p := position.New()
	data := []byte("hello\n")
	p.Scan(data)
	// After the newline, we are on the *next* line (line 2), at column 0
	assertPositionState(t, p, 2, 0, 6, "After scanning 'hello\\n'")
}

func TestScan_MultipleLines(t *testing.T) {
	p := position.New()
	data := []byte("line1\nline22\nli3")
	p.Scan(data)
	// After 'li3', we are on line 3, column 3
	assertPositionState(t, p, 3, 3, 16, "After scanning multiple lines")
}

func TestScan_MultipleCalls(t *testing.T) {
	p := position.New()
	p.Scan([]byte("abc\n"))
	assertPositionState(t, p, 2, 0, 4, "After first scan")
	p.Scan([]byte("de"))
	assertPositionState(t, p, 2, 2, 6, "After second scan")
	p.Scan([]byte("f\nghi"))
	assertPositionState(t, p, 3, 3, 11, "After third scan")
}

func TestReset(t *testing.T) {
	p := position.New()
	p.Scan([]byte("some\ndata\nhere"))
	assertPositionState(t, p, 3, 4, 14, "Before reset")

	p.Reset()
	assertPositionState(t, p, 1, 0, 0, "After reset")

	// Ensure it works after reset
	p.Scan([]byte("new"))
	assertPositionState(t, p, 1, 3, 3, "After scan post-reset")
}

func TestCopy(t *testing.T) {
	p1 := position.New()
	p1.Scan([]byte("copy\nthis\ndata"))
	assertPositionState(t, p1, 3, 4, 14, "Original before copy")

	p2 := p1.Copy() // Note: Copy returns a value, not a pointer

	// Verify p2 is a copy of p1's state *at the time of copy*
	// We need to access p2's state. Since it's not a pointer, we can't call methods directly
	// on 'p2' if they have pointer receivers. Let's test the state by scanning more on p1.
	// Or, more directly, let's make the getters work on values too (or test fields if exported, but they aren't).
	// Let's assume we want to compare the state via getters. We need a pointer to p2.
	p2Ptr := &p2
	assertPositionState(t, p2Ptr, 3, 4, 14, "Copy initial state")

	// Modify original (p1)
	p1.Scan([]byte("more"))
	assertPositionState(t, p1, 3, 8, 18, "Original after modification")

	// Verify copy (p2) remains unchanged
	assertPositionState(t, p2Ptr, 3, 4, 14, "Copy after original modified")

	// Modify copy (p2) - need Scan to work on value receiver or use pointer
	// Let's assume Scan works on pointer receiver (as it modifies state)
	p2Ptr.Scan([]byte(" appended"))                                     // Need to use the pointer to modify
	assertPositionState(t, p2Ptr, 3, 13, 23, "Copy after modification") // 14 + 9 = 23

	// Verify original (p1) remains unchanged by copy modification
	assertPositionState(t, p1, 3, 8, 18, "Original after copy modified")
}

// --- Rewind Tests ---

func TestRewind_Negative(t *testing.T) {
	p := position.New()
	p.Scan([]byte("abc"))
	err := p.Rewind(-1)
	assert.Error(t, err, "Should return error for negative rewind")
	assertPositionState(t, p, 1, 3, 3, "State should be unchanged after negative rewind attempt")
}

func TestRewind_Zero(t *testing.T) {
	p := position.New()
	p.Scan([]byte("abc"))
	err := p.Rewind(0)
	assert.NoError(t, err)
	assertPositionState(t, p, 1, 3, 3, "State should be unchanged after rewind 0")
}

func TestRewind_WithinLine(t *testing.T) {
	p := position.New()
	p.Scan([]byte("abcdef")) // L1, C6, O6
	err := p.Rewind(2)       // Rewind to 'd'
	assert.NoError(t, err)
	assertPositionState(t, p, 1, 4, 4, "Rewind within line")
}

func TestRewind_ToStartOfLine(t *testing.T) {
	p := position.New()
	p.Scan([]byte("abcdef")) // L1, C6, O6
	err := p.Rewind(6)       // Rewind to beginning
	assert.NoError(t, err)
	assertPositionState(t, p, 1, 0, 0, "Rewind to start of first line")

	p.Reset()
	p.Scan([]byte("line1\nline2")) // L2, C5, O11
	assertPositionState(t, p, 2, 5, 11, "Initial state")

	err = p.Rewind(5) // Rewind to start of line 2
	assert.NoError(t, err)
	assertPositionState(t, p, 2, 0, 6, "Rewind to start of second line")
}

func TestRewind_AcrossOneNewline(t *testing.T) {
	p := position.New()
	p.Scan([]byte("line1\nline2")) // L2, C5, O11
	err := p.Rewind(7)             // Rewind 5 for 'line2', 1 for '\n', 1 into 'line1' -> should be at 'e'
	assert.NoError(t, err)
	assertPositionState(t, p, 1, 4, 4, "Rewind across one newline")
}

func TestRewind_AcrossMultipleNewlines(t *testing.T) {
	p := position.New()
	// L1: "abc" (3) + \n (1) = 4
	// L2: "de"  (2) + \n (1) = 7
	// L3: "fgh" (3)       = 10
	p.Scan([]byte("abc\nde\nfgh")) // L3, C3, O10
	err := p.Rewind(6)             // Rewind 3 ('fgh'), 1 ('\n'), 2 ('de') -> should be at start of L2
	assert.NoError(t, err)
	assertPositionState(t, p, 2, 0, 4, "Rewind across multiple newlines (to start of L2)")

	p.Reset()
	p.Scan([]byte("abc\nde\nfgh")) // L3, C3, O10
	err = p.Rewind(8)              // Rewind 3 ('fgh'), 1 ('\n'), 2 ('de'), 1 ('\n'), 1 ('c') -> should be at 'b' on L1
	assert.NoError(t, err)
	assertPositionState(t, p, 1, 2, 2, "Rewind across multiple newlines (into L1)")
}

func TestRewind_PastBeginning(t *testing.T) {
	p := position.New()
	p.Scan([]byte("abc")) // L1, C3, O3
	assertPositionState(t, p, 1, 3, 3)

	err := p.Rewind(5) // Rewind more than available
	assert.Error(t, err)
	assertPositionState(t, p, 1, 3, 3)

	p.Reset()
	p.Scan([]byte("abc\ndef")) // L2, C3, O7
	err = p.Rewind(10)         // Rewind more than available
	assert.Error(t, err)
	assertPositionState(t, p, 2, 3, 7)
}

func TestRewind_Sequential(t *testing.T) {
	p := position.New()
	p.Scan([]byte("line1\nline22\nline333")) // L3, C7, O20
	assertPositionState(t, p, 3, 7, 20, "Initial state")

	err := p.Rewind(3) // Rewind within L3
	assert.NoError(t, err)
	assertPositionState(t, p, 3, 4, 17, "After first rewind (within L3)")

	err = p.Rewind(6) // Rewind 4 (L3), 1 (\n), 1 (L2) -> 'e' on L2
	assert.NoError(t, err)
	assertPositionState(t, p, 2, 5, 11, "After second rewind (into L2)")

	err = p.Rewind(10) // Rewind 5 (L2), 1 (\n), 4 (L1) -> 'e' on L1
	assert.NoError(t, err)
	assertPositionState(t, p, 1, 1, 1, "After third rewind (into L1)")

	err = p.Rewind(5) // Rewind past beginning
	assert.Error(t, err)
	assertPositionState(t, p, 1, 1, 1, "State should be unchanged after error")
}

// --- Concurrency Test ---

func TestConcurrency(t *testing.T) {
	// Seed random number generator for potentially varied inputs across runs
	// Use a fixed seed if you need deterministic failure reproduction
	// rand.Seed(time.Now().UnixNano())
	rand.New(rand.NewSource(time.Now().UnixNano()))

	p := position.New()
	numGoroutines := 50
	opsPerGoroutine := 100
	var wg sync.WaitGroup

	// Sample data chunks for Scan operations
	scanData := [][]byte{
		[]byte("hello"),
		[]byte(" world\n"),
		[]byte("another line\n"),
		[]byte("data"),
		[]byte("\n"),
		[]byte("concurrent access "),
		[]byte("is tricky\n"),
	}

	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				op := rand.Intn(100) // Biased towards Scan/Read

				switch {
				case op < 50: // Scan (50% chance)
					data := scanData[rand.Intn(len(scanData))]
					p.Scan(data)
				case op < 70: // Read (20% chance)
					_ = p.Line()
					_ = p.Column()
					_ = p.Offset()
					_ = p.String()
				case op < 80: // Rewind (10% chance)
					// Rewind a small amount to avoid excessive complexity/errors
					// Be careful: Rewinding based on current offset read concurrently
					// could be problematic itself. Rewind small fixed/random amounts.
					rewindAmount := rand.Intn(5) + 1 // Rewind 1 to 5 chars
					_ = p.Rewind(rewindAmount)       // Ignore error for simplicity in concurrent test
				case op < 85: // Copy (5% chance)
					_ = p.Copy() // Just perform the copy, don't use result extensively here
				case op < 90: // Reset (5% chance)
					p.Reset()
				default: // Read again (remaining chance)
					_ = p.Line()
					_ = p.Column()
					_ = p.Offset()
				}
				// Optional: Short sleep to increase chance of interleaving
				// time.Sleep(time.Microsecond)
			}
		}(i)
	}

	wg.Wait()

	// --- Verification ---
	// 1. The MOST IMPORTANT check is running this test with the race detector:
	//    go test -race ./...
	//    If the race detector reports any data races, the test fails, indicating
	//    the locking is insufficient or incorrect.

	// 2. Basic Sanity Checks (Optional, as exact state is non-deterministic):
	//    After all operations, the state should still be somewhat valid.
	//    We can't predict the exact line/col/offset due to Rewind/Reset races,
	//    but we can check for impossible states.
	finalOffset := p.Offset()
	finalLine := p.Line()
	finalCol := p.Column()

	assert.GreaterOrEqual(t, finalOffset, 0, "Final offset should not be negative")
	assert.GreaterOrEqual(t, finalLine, 1, "Final line should be at least 1")
	assert.GreaterOrEqual(t, finalCol, 0, "Final column should not be negative")

	// 3. Consistency Check (More involved, might fail non-deterministically if Reset/Rewind are frequent)
	//    Try to reconstruct the position based *only* on the final offset and check if line/col match.
	//    This is hard because we don't have the original text.
	//    A simpler check: If we *only* did Scans, the offset should match the total bytes scanned.
	//    Since we mix operations, this isn't feasible here. The race detector is key.

	t.Logf("Concurrency test finished. Final state: %s, Offset: %d", p.String(), p.Offset())
	t.Log("Ensure this test is run with 'go test -race ./...' to detect data races.")
}

// Example test showing Copy creates independent state
func TestCopyIndependence(t *testing.T) {
	p1 := position.New()
	p1.Scan([]byte("abc\n"))

	p2 := p1.Copy() // p2 is a value
	p2Ptr := &p2    // Get a pointer to call modifying methods like Scan

	p1.Scan([]byte("def"))
	p2Ptr.Scan([]byte("xyz\n123"))

	// Check p1 state
	assertPositionState(t, p1, 2, 3, 7, "p1 state after its own scan") // abc\ndef -> L2, C3, O7

	// Check p2 state (based on its *own* scan after copy)
	// p2 started at L2, C0, O4. Scanned "xyz\n123" (7 chars)
	// xyz -> L2, C3, O7
	// \n  -> L3, C0, O8
	// 123 -> L3, C3, O11
	assertPositionState(t, p2Ptr, 3, 3, 11, "p2 state after its own scan")
}

// Helper function to simulate reading a buffer and tracking position
func simulateRead(t *testing.T, data []byte, chunkSize int) *position.Position {
	p := position.New()
	reader := bytes.NewReader(data)
	buf := make([]byte, chunkSize)

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			p.Scan(buf[:n])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err, "Read error during simulation")
		}
	}
	return p
}

func TestSimulation(t *testing.T) {
	line1 := "This is line one."
	line2 := "This is line two."
	line3 := "And finally line three."

	data := []byte(strings.Join([]string{line1, line2, line3}, "\n"))

	expectedLine := 3
	expectedCol := len(line3)
	expectedOffset := len(data)

	// Simulate reading in chunks
	p := simulateRead(t, data, 10) // Use a chunk size smaller than lines

	assertPositionState(t, p, expectedLine, expectedCol, expectedOffset, "Simulation with chunk size 10")

	p = simulateRead(t, data, 128) // Use a chunk size larger than buffer
	assertPositionState(t, p, expectedLine, expectedCol, expectedOffset, "Simulation with chunk size 128")
}
