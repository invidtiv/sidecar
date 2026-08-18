package app

import (
	"os"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/testenv"
)

// TestMain pins the zone the package's tests render in. Issue timestamps are
// shown in the reader's own zone (issueview.formatCreated calls Local), which
// is right for the product and fatal for a golden: the same fixture renders
// 18:42 on the machine that generated internal/app/testdata and 01:42 the next
// day on a UTC CI runner. Pinning to the fixtures' own -07:00 offset keeps the
// committed goldens byte-identical wherever they are checked.
// It also isolates tmux: this package builds full plugin models, which reach
// internal/tty and from there shell out to tmux (td-4d99ae).
func TestMain(m *testing.M) {
	time.Local = time.FixedZone("MST", -7*60*60)
	os.Exit(testenv.Main(m))
}
