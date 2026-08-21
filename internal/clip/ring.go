package clip

import "sync"

// recentLimit is how many recent copies the session ring keeps. Old entries
// fall off the far end; nothing here outlives the process.
const recentLimit = 10

var (
	recentMu sync.Mutex
	recent   []string
)

// recordRecent moves text to the front of the session ring, dropping an
// earlier copy of the same text so repeated copies do not crowd the ring.
func recordRecent(text string) {
	if text == "" {
		return
	}
	recentMu.Lock()
	defer recentMu.Unlock()
	for i, seen := range recent {
		if seen == text {
			recent = append(recent[:i], recent[i+1:]...)
			break
		}
	}
	recent = append([]string{text}, recent...)
	if len(recent) > recentLimit {
		recent = recent[:recentLimit]
	}
}

// LastCopied returns the text of the most recent copy any surface made this
// session — a yank in one plugin, a cut in another. It is what paste reaches
// for when the system clipboard is out of reach, as it is over SSH, and it
// never fails on a machine with no clipboard utility because no clipboard is
// involved: the copy already passed through here.
func LastCopied() (string, bool) {
	recentMu.Lock()
	defer recentMu.Unlock()
	if len(recent) == 0 {
		return "", false
	}
	return recent[0], true
}

// ResetRecent empties the session ring.
func ResetRecent() {
	recentMu.Lock()
	defer recentMu.Unlock()
	recent = nil
}
