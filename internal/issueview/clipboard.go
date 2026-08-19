package issueview

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/clip"
	"github.com/marcus/sidecar/internal/msg"
)

// CopyMarkdown copies the issue as markdown. A missing issue is a no-op.
func CopyMarkdown(d *Data) tea.Cmd {
	if d == nil {
		return nil
	}
	return copyText(FormatMarkdown(d), "Yanked issue details")
}

// CopyID copies just the issue id. A missing issue is a no-op.
func CopyID(d *Data) tea.Cmd {
	if d == nil || d.ID == "" {
		return nil
	}
	return copyText(d.ID, "Yanked: "+d.ID)
}

func copyText(text, ok string) tea.Cmd {
	if text == "" {
		return nil
	}
	return clip.Copy(text, func(r clip.Result) tea.Msg {
		return msg.ToastMsg{Message: r.Message(ok), Duration: 2 * time.Second}
	})
}
