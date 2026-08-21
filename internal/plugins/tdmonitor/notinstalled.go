package tdmonitor

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/installui"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/version"
)

// Stallion ASCII art - a galloping horse
const stallionArt = "" +
	"                 >\\/7\n" +
	"             _.-(6'  \\\n" +
	"            (=___._/` \\\n" +
	"                 )  \\ |\n" +
	"                /   / |\n" +
	"               /    > /\n" +
	"              j    < _\\\n" +
	"          _.-' :      ``.\n" +
	"          \\ r=._\\        `.\n" +
	"         <`\\\\_  \\         . `-. \n" +
	"          \\ r-7  `-. ._  ' .  `\\\n" +
	"           \\`,      `-.`7  7)   )\n" +
	"            \\/         \\|  \\'  / `-._\n" +
	"                       ||    .'\n" +
	"                        \\\\  (\n" +
	"                         >\\  >\n" +
	"                     ,.-' >.'\n" +
	"                    <.'_.''\n" +
	"                      <'\n"

// getThemeAnimationColors returns the current theme's animation colors as RGB.
// Uses Primary (purple), Secondary (blue), and Accent (amber/orange) from the theme.
func getThemeAnimationColors() (RGB, RGB, RGB) {
	theme := styles.GetCurrentTheme()
	return hexToRGB(theme.Colors.Primary),
		hexToRGB(theme.Colors.Secondary),
		hexToRGB(theme.Colors.Accent)
}

// RGB represents a color in RGB space for interpolation.
type RGB struct {
	R, G, B float64
}

// hexToRGB converts a hex color string to RGB.
func hexToRGB(hex string) RGB {
	hex = strings.TrimPrefix(hex, "#")
	var r, g, b uint8
	if _, err := fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b); err != nil {
		// Fallback to default dark gray on parse failure
		r, g, b = 55, 65, 81
	}
	return RGB{float64(r), float64(g), float64(b)}
}

// toANSI returns raw ANSI escape code for the color.
func (c RGB) toANSI() string {
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", int(c.R), int(c.G), int(c.B))
}

const ansiReset = "\x1b[0m"

// lerpRGB linearly interpolates between two colors.
func lerpRGB(c1, c2 RGB, t float64) RGB {
	return RGB{
		R: c1.R + (c2.R-c1.R)*t,
		G: c1.G + (c2.G-c1.G)*t,
		B: c1.B + (c2.B-c1.B)*t,
	}
}

// NotInstalledModel handles the animated "td not installed" view.
type NotInstalledModel struct {
	startTime    time.Time
	width        int
	height       int
	installer    *installui.Model
	mouseHandler *mouse.Handler
}

// NewNotInstalledModel creates a new not-installed view model.
func NewNotInstalledModel() *NotInstalledModel {
	return NewNotInstalledModelWithEnv(nil)
}

// NewNotInstalledModelWithEnv creates the view against a described machine so
// tests never invoke a real package manager.
func NewNotInstalledModelWithEnv(env *version.Environment) *NotInstalledModel {
	return &NotInstalledModel{
		startTime:    time.Now(),
		installer:    installui.New(version.TdDescriptor(), env),
		mouseHandler: mouse.NewHandler(),
	}
}

// StallionTickMsg is sent to update the animation frame.
type StallionTickMsg time.Time

// StallionTick returns a command that ticks for animation.
func StallionTick() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg {
		return StallionTickMsg(t)
	})
}

// Init returns the initial command (starts animation).
func (m *NotInstalledModel) Init() tea.Cmd {
	return StallionTick()
}

// Update handles messages for the not-installed view.
func (m *NotInstalledModel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case StallionTickMsg:
		if m.installer != nil {
			m.installer.Tick()
		}
		return StallionTick()
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyPressMsg:
		if m.installer != nil {
			return m.installer.HandleKey(msg)
		}
	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return nil
}

func (m *NotInstalledModel) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if m.mouseHandler == nil {
		return nil
	}
	switch mm := msg.(type) {
	case tea.MouseMotionMsg:
		hit := m.mouseHandler.HitMap.Test(mm.X, mm.Y)
		m.installerHover(hit != nil && hit.ID == installui.RegionInstall)
	case tea.MouseClickMsg:
		if mm.Button != tea.MouseLeft {
			return nil
		}
		hit := m.mouseHandler.HandleClick(mm.X, mm.Y)
		if hit.Region != nil && hit.Region.ID == installui.RegionInstall {
			return m.installer.HandleClick()
		}
	}
	return nil
}

func (m *NotInstalledModel) installerHover(hover bool) {
	if m.installer != nil {
		m.installer.Hover = hover
	}
}

// gradientColorAt returns the color for a character based on its position and time.
// Creates a smooth rolling wave effect across the image.
func (m *NotInstalledModel) gradientColorAt(charIndex, totalChars int) RGB {
	elapsed := time.Since(m.startTime).Seconds()
	cycleDuration := 8.0 // seconds for one full color cycle

	// Character's position in the art (0 to 1)
	charPos := float64(charIndex) / float64(totalChars)

	// Create a smooth rolling phase based on position and time
	// The wave travels through the art over time
	phase := math.Mod(charPos-elapsed/cycleDuration, 1.0)
	if phase < 0 {
		phase += 1.0
	}

	// Get current theme colors for animation
	colorPrimary, colorSecondary, colorAccent := getThemeAnimationColors()

	// Smooth three-color gradient: primary -> secondary -> accent -> primary
	// Using sine-based interpolation for smoother transitions
	return threewayGradient(phase, colorPrimary, colorSecondary, colorAccent)
}

// threewayGradient smoothly interpolates between three colors in a cycle.
func threewayGradient(t float64, c1, c2, c3 RGB) RGB {
	// t is 0-1, we divide into three segments with smooth transitions
	t = math.Mod(t, 1.0)
	if t < 0 {
		t += 1.0
	}

	// Use cosine interpolation for smoother transitions
	if t < 1.0/3.0 {
		// c1 -> c2
		blend := smoothstep(t * 3.0)
		return lerpRGB(c1, c2, blend)
	} else if t < 2.0/3.0 {
		// c2 -> c3
		blend := smoothstep((t - 1.0/3.0) * 3.0)
		return lerpRGB(c2, c3, blend)
	} else {
		// c3 -> c1
		blend := smoothstep((t - 2.0/3.0) * 3.0)
		return lerpRGB(c3, c1, blend)
	}
}

// smoothstep provides smooth easing (ease-in-out).
func smoothstep(t float64) float64 {
	if t < 0 {
		return 0
	}
	if t > 1 {
		return 1
	}
	return t * t * (3 - 2*t)
}

// renderStallion returns the stallion art with animated gradient sweep.
func (m *NotInstalledModel) renderStallion() string {
	lines := strings.Split(stallionArt, "\n")

	// Count total visible characters for position calculation
	var totalChars int
	for _, line := range lines {
		for _, ch := range line {
			if ch != ' ' && ch != '\t' {
				totalChars++
			}
		}
	}

	// Render each character with its gradient color using raw ANSI codes
	// (lipgloss per-character styling causes width calculation issues)
	var result strings.Builder
	charIndex := 0

	for _, line := range lines {
		for _, ch := range line {
			if ch == ' ' || ch == '\t' {
				result.WriteRune(ch)
			} else {
				color := m.gradientColorAt(charIndex, totalChars)
				result.WriteString(color.toANSI())
				result.WriteRune(ch)
				result.WriteString(ansiReset)
				charIndex++
			}
		}
		result.WriteRune('\n')
	}

	return result.String()
}

// renderPitch returns the professional pitch copy.
func (m *NotInstalledModel) renderPitch() string {
	// Use theme-aware styles
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(styles.TextHighlight)

	mutedStyle := styles.Muted

	textStyle := lipgloss.NewStyle().
		Foreground(styles.TextSecondary)

	bulletStyle := lipgloss.NewStyle().
		Foreground(styles.TextPrimary)

	linkStyle := styles.Link

	// Syntax highlighted command styles
	kwStyle := lipgloss.NewStyle().
		Foreground(styles.Primary).
		Bold(true)

	argStyle := lipgloss.NewStyle().
		Foreground(styles.TextPrimary)

	commentStyle := lipgloss.NewStyle().
		Foreground(styles.TextMuted).
		Italic(true)

	codeBoxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(styles.BorderNormal).
		Padding(0, 2)

	// Build formatted code box
	var codeLines []string
	codeLines = append(codeLines, kwStyle.Render("brew install")+" "+argStyle.Render("marcus/tap/td"))
	codeLines = append(codeLines, commentStyle.Render("# or"))
	codeLines = append(codeLines, kwStyle.Render("go install")+" "+argStyle.Render("github.com/marcus/td@latest"))
	codeLines = append(codeLines, "")
	codeLines = append(codeLines, kwStyle.Render("td init"))
	installCode := strings.Join(codeLines, "\n")

	// Build content
	var b strings.Builder

	// Status notice
	b.WriteString(mutedStyle.Render("td is not installed on this system."))
	b.WriteString("\n\n")

	// Section Title
	b.WriteString(titleStyle.Render("External memory for AI sessions"))
	b.WriteString("\n\n")

	// Capabilities
	b.WriteString(textStyle.Render("td gives each session:"))
	b.WriteString("\n")
	b.WriteString(bulletStyle.Render("  • Durable task context that persists across AI sessions"))
	b.WriteString("\n")
	b.WriteString(bulletStyle.Render("  • Work tracking with progress logs and structured handoffs"))
	b.WriteString("\n")
	b.WriteString(bulletStyle.Render("  • Independent review and approval workflows"))
	b.WriteString("\n")
	b.WriteString(bulletStyle.Render("  • Fast, local SQLite storage"))
	b.WriteString("\n\n")

	// Installation instructions (above Learn more)
	b.WriteString(codeBoxStyle.Render(installCode))
	b.WriteString("\n\n")

	if m.installer != nil {
		if progress := m.installer.RenderProgress(); progress != "" {
			b.WriteString(progress)
			b.WriteString("\n")
			b.WriteString(mutedStyle.Render(m.installer.DisplayCommand()))
			b.WriteString("\n\n")
		} else if m.installer.CanInstall() {
			hover := m.installer.Hover
			b.WriteString(m.installer.RenderButton(true, hover))
			b.WriteString("\n")
			b.WriteString(mutedStyle.Render("Runs: " + m.installer.DisplayCommand()))
			b.WriteString("\n\n")
		}
		if problem := m.installer.RenderProblem(); problem != "" {
			b.WriteString(problem)
			b.WriteString("\n\n")
		}
	}

	// Website link
	b.WriteString(textStyle.Render("Learn more: ") + linkStyle.Render("https://marcus.github.io/td/"))

	return b.String()
}

// View renders the complete not-installed screen.
func (m *NotInstalledModel) View(width, height int) string {
	m.width = width
	m.height = height
	if m.mouseHandler != nil {
		m.mouseHandler.HitMap.Clear()
	}

	stallion := m.renderStallion()
	pitch := m.renderPitch()

	// Get stallion width to center pitch within it
	stallionWidth := lipgloss.Width(stallion)
	centeredPitch := lipgloss.PlaceHorizontal(stallionWidth, lipgloss.Center, pitch)

	// Combine vertically - use Left to preserve stallion's whitespace alignment
	// (PlaceHorizontal/Center on stallion causes ANSI width miscalculation issues)
	content := lipgloss.JoinVertical(lipgloss.Left, stallion, centeredPitch)

	placed := lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
	m.registerInstallHit(placed, width, height)
	return placed
}

func (m *NotInstalledModel) registerInstallHit(placed string, width, height int) {
	if m.mouseHandler == nil || m.installer == nil || !m.installer.CanInstall() || m.installer.Busy() {
		return
	}
	label := installui.ButtonLabel(version.TdDescriptor())
	lines := strings.Split(placed, "\n")
	for y, line := range lines {
		if y >= height {
			break
		}
		stripped := ansi.Strip(line)
		idx := strings.Index(stripped, label)
		if idx < 0 {
			continue
		}
		w := ansi.StringWidth(label)
		if idx+w > width {
			w = max(0, width-idx)
		}
		m.mouseHandler.HitMap.AddRect(installui.RegionInstall, idx, y, w, 1, nil)
		return
	}
}
