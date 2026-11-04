package panel

import (
	"time"

	"github.com/unxed/f4/internal/sysinfo"
)

// freeSpaceTTL is how long the free space on a panel's info line is shown
// before statfs is asked again. The line is drawn on every frame, and every
// frame used to ask: a statfs per panel per keystroke and per line of
// terminal output.
const freeSpaceTTL = time.Second

// queryFreeBytes is the free space query; a variable so that tests count it.
var queryFreeBytes = sysinfo.FreeBytes

// freeSpaceCache remembers the free space of one folder for freeSpaceTTL.
// A reread of the folder drops it (reset), so a copy or a delete shows its
// effect on the next frame rather than up to a second later, which in an
// idle window could have been never.
type freeSpaceCache struct {
	path string
	at   time.Time
	free uint64
	ok   bool
}

func (c *freeSpaceCache) get(path string, now time.Time) (uint64, bool) {
	if c.at.IsZero() || path != c.path || now.Sub(c.at) >= freeSpaceTTL || now.Before(c.at) {
		c.free, c.ok = queryFreeBytes(path)
		c.path, c.at = path, now
	}
	return c.free, c.ok
}

func (c *freeSpaceCache) reset() {
	*c = freeSpaceCache{}
}
