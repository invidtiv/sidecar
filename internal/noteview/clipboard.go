package noteview

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/clip"
	"github.com/marcus/sidecar/internal/msg"
)

// FormatMarkdown renders one note as a heading plus its body.
func FormatMarkdown(d *Data) string {
	if d == nil {
		return ""
	}
	var sb strings.Builder
	title := d.Title
	if title == "" {
		title = d.ID
	}
	fmt.Fprintf(&sb, "# %s\n\n", title)
	fmt.Fprintf(&sb, "**ID:** `%s`\n", d.ID)
	if d.Pinned {
		sb.WriteString("**Pinned**\n")
	}
	if d.Archived {
		sb.WriteString("**Archived**\n")
	}
	if d.Content != "" {
		sb.WriteByte('\n')
		sb.WriteString(d.Content)
		if !strings.HasSuffix(d.Content, "\n") {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// CopyMarkdown copies the note as markdown.
func CopyMarkdown(d *Data) tea.Cmd {
	if d == nil {
		return nil
	}
	return copyText(FormatMarkdown(d), "Yanked note")
}

// CopyID copies just the note id.
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
		return msg.FlashMsg{Text: r.Message(ok)}
	})
}
