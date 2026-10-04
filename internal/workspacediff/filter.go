package workspacediff

// ReadFilter narrows an API diff before Git or an untracked-file reader emits
// its contents. A nil filter preserves the desktop's ordinary Git view.
type ReadFilter struct {
	// ExcludePaths are Git-ready exclusion pathspecs, including any magic flags.
	ExcludePaths []string
	// AllowPath runs before an untracked path is inspected or opened.
	AllowPath func(string) bool
}

func filteredGitArgs(args []string, filter *ReadFilter) []string {
	if filter == nil {
		return args
	}
	out := append(append([]string(nil), args...), "--", ".")
	return append(out, filter.ExcludePaths...)
}
