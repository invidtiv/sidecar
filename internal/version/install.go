package version

// InstallMethod represents how a product was installed.
type InstallMethod string

const (
	InstallMethodHomebrew InstallMethod = "homebrew"
	InstallMethodGo       InstallMethod = "go"
	// InstallMethodBinary covers anything Sidecar does not manage: a
	// downloaded binary, a packaged build, or an active local development
	// selector. These are never overwritten automatically.
	InstallMethodBinary InstallMethod = "binary"
)

// String returns the human-readable name of an install method.
func (m InstallMethod) String() string {
	switch m {
	case InstallMethodHomebrew:
		return "Homebrew"
	case InstallMethodGo:
		return "go install"
	default:
		return "unmanaged"
	}
}
