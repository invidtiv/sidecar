package termpreview

// HostBackgroundMsg carries the terminal emulator's reported default
// background to every embedded terminal projection. ANSI is precomputed at the
// app boundary so terminal rows do not rebuild a style sequence while drawing.
type HostBackgroundMsg struct {
	ANSI string
}
