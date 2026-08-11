package version

import (
	"regexp"
	"strings"
)

// ProductID identifies an updatable product Sidecar knows how to check.
type ProductID string

const (
	// ProductSidecar is Sidecar itself.
	ProductSidecar ProductID = "sidecar"
	// ProductTd is the td issue tracker CLI.
	ProductTd ProductID = "td"
	// ProductTasks is the standalone Tasks command suite. Sidecar embeds the
	// Tasks TUI library at build time; this target is the separately installed
	// tasks/tasks-tui/tasks-api commands, which only their own package manager
	// can refresh.
	ProductTasks ProductID = "tasks"
)

// Descriptor describes how to discover and update one product. Everything that
// differs between products lives here so the checking, planning, install, and
// verification code stays product-neutral.
type Descriptor struct {
	Product     ProductID
	DisplayName string

	RepoOwner string
	RepoName  string

	// Executable is the command used to read the installed version and to
	// resolve install provenance.
	Executable string
	// SuiteBinaries lists every binary the release ships. A product is only
	// verified as updated when all of them resolve to the released version;
	// for Tasks that is the distribution contract.
	SuiteBinaries []string
	// VersionArgs are the arguments that make Executable print its version.
	VersionArgs []string

	// Formula is the fully qualified Homebrew formula name.
	Formula string
	// GoPackages are the packages an automated `go install` update must build.
	// Empty means a Go install cannot be expressed safely and must be manual.
	GoPackages []string
	// GoLdflags stamps the version into main.Version (Sidecar's release does
	// this; the other products embed their version at tag time).
	GoLdflags bool

	// CacheFile is the per-product release-check cache file name. Distinct per
	// product so similarly numbered releases cannot collide.
	CacheFile string

	ReleasesURL string
}

// SidecarDescriptor describes Sidecar itself.
func SidecarDescriptor() Descriptor {
	return Descriptor{
		Product:       ProductSidecar,
		DisplayName:   "Sidecar",
		RepoOwner:     repoOwner,
		RepoName:      repoName,
		Executable:    "sidecar",
		SuiteBinaries: []string{"sidecar"},
		VersionArgs:   []string{"--version"},
		Formula:       "marcus/tap/sidecar",
		GoPackages:    []string{"github.com/marcus/sidecar/cmd/sidecar"},
		GoLdflags:     true,
		CacheFile:     cacheFile,
		ReleasesURL:   "https://github.com/marcus/sidecar/releases",
	}
}

// TdDescriptor describes the td CLI.
func TdDescriptor() Descriptor {
	return Descriptor{
		Product:       ProductTd,
		DisplayName:   "td",
		RepoOwner:     tdRepoOwner,
		RepoName:      tdRepoName,
		Executable:    "td",
		SuiteBinaries: []string{"td"},
		VersionArgs:   []string{"version", "--short"},
		Formula:       "marcus/tap/td",
		GoPackages:    []string{"github.com/marcus/td"},
		CacheFile:     tdCacheFile,
		ReleasesURL:   "https://github.com/marcus/td/releases",
	}
}

// TasksDescriptor describes the standalone Tasks command suite.
func TasksDescriptor() Descriptor {
	return Descriptor{
		Product:       ProductTasks,
		DisplayName:   "Tasks",
		RepoOwner:     tasksRepoOwner,
		RepoName:      tasksRepoName,
		Executable:    "tasks",
		SuiteBinaries: []string{"tasks", "tasks-tui", "tasks-api"},
		VersionArgs:   []string{"--version"},
		Formula:       "marcus/tap/tasks",
		GoPackages: []string{
			"github.com/marcus/tasks/cmd/tasks",
			"github.com/marcus/tasks/cmd/tasks-tui",
			"github.com/marcus/tasks/cmd/tasks-api",
		},
		CacheFile:   tasksCacheFile,
		ReleasesURL: "https://github.com/marcus/tasks/releases",
	}
}

// DescriptorFor returns the descriptor for a product id.
func DescriptorFor(id ProductID) (Descriptor, bool) {
	switch id {
	case ProductSidecar:
		return SidecarDescriptor(), true
	case ProductTd:
		return TdDescriptor(), true
	case ProductTasks:
		return TasksDescriptor(), true
	}
	return Descriptor{}, false
}

// InstallHint is the supported way to install a product that is missing.
// Sidecar never runs these: turning an update confirmation into a new product
// installation is a separate decision.
func (d Descriptor) InstallHint() string {
	if d.Formula == "" {
		return d.ReleasesURL
	}
	return "brew install " + d.Formula
}

var semverPattern = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// NormalizeVersion extracts a comparable version from arbitrary `--version`
// output (e.g. "tasks version 1.5.0" or "v1.5.0"). It returns "" when the
// output carries no release version, which is how development builds and
// unparsable output are told apart from a real release.
func NormalizeVersion(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// A development build may still contain digits (e.g. a short sha), so only
	// a dotted numeric version counts.
	return semverPattern.FindString(s)
}

// SameVersion reports whether two version strings name the same release.
func SameVersion(a, b string) bool {
	na, nb := NormalizeVersion(a), NormalizeVersion(b)
	if na == "" || nb == "" {
		return false
	}
	return na == nb
}
