package authoring

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Raw capture of a session's terminal traffic, for diagnosing rendering faults.
//
// A garbled terminal has two possible causes and they need opposite fixes: the
// agent drew the wrong thing because it was told the wrong size, or it drew the
// right thing and something between the PTY and the screen lost it. Watching the
// window cannot tell those apart. The bytes can — replayed into a terminal of
// the size the agent was actually given, a clean render means the fault is
// downstream of here, and a garbled one means it is upstream.
//
// Off unless LOOP_PTY_CAPTURE names a directory. It writes everything the agent
// says, so it is a diagnostic the user opts into, never a default.

// captureDirEnv names the directory to write captures to.
const captureDirEnv = "LOOP_PTY_CAPTURE"

// capture records one session's output alongside the sizes it was given.
type capture struct {
	mu     sync.Mutex
	raw    *os.File
	log    *os.File
	offset int64
	start  time.Time
}

// newCapture opens a capture for id, or returns nil when capturing is off.
//
// A capture that cannot be opened is not an error worth failing a session over:
// the session is the point and the capture is an aid, so it stays nil and
// everything below silently does nothing.
func newCapture(id string) *capture {
	dir := os.Getenv(captureDirEnv)
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}

	raw, err := os.Create(filepath.Join(dir, id+".raw"))
	if err != nil {
		return nil
	}
	log, err := os.Create(filepath.Join(dir, id+".log"))
	if err != nil {
		_ = raw.Close()
		return nil
	}
	return &capture{raw: raw, log: log, start: time.Now()}
}

// writeChunk records output exactly as it came off the PTY.
func (c *capture) writeChunk(chunk []byte) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n, _ := c.raw.Write(chunk)
	c.offset += int64(n)
}

// note records something that happened to the terminal rather than in it —
// a resize, the initial size — against the byte offset it happened at, so a
// replay can apply it at the same point in the stream.
func (c *capture) note(what string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	elapsed := time.Since(c.start).Milliseconds()
	fmt.Fprintf(c.log, "%d\t%d\t%s\n", elapsed, c.offset, what)
}

// noteSize records a size the PTY was set to.
func (c *capture) noteSize(label string, cols, rows int) {
	c.note(fmt.Sprintf("%s cols=%d rows=%d", label, cols, rows))
}

func (c *capture) close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.raw.Close()
	_ = c.log.Close()
}
