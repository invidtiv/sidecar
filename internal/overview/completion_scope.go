package overview

// completionScope belongs to a configured project set, not a refresh poll.
// Create replies additionally belong to the dialog that issued them. Existing
// Project/WorkspaceID/host-incarnation fields still route each result to its
// owner; these fences stop old replies altering a replacement projection.
type completionScope struct {
	Configuration uint64
	Create        uint64
	Rename        uint64
	Delete        uint64
	Scoped        bool
}

func (s completionScope) getCompletionScope() completionScope { return s }

func (m *Model) completionScope() completionScope {
	return completionScope{Configuration: m.configurationGeneration, Scoped: true}
}

func (m *Model) createCompletionScope() completionScope {
	scope := m.completionScope()
	scope.Create = m.createGeneration
	return scope
}

func (m *Model) completionCurrent(scope completionScope) bool {
	return !scope.Scoped || (scope.Configuration == m.configurationGeneration &&
		(scope.Create == 0 || scope.Create == m.createGeneration) &&
		(scope.Rename == 0 || scope.Rename == m.renameGeneration) &&
		(scope.Delete == 0 || scope.Delete == m.deleteGeneration))
}

func (m *Model) renameCompletionScope() completionScope {
	scope := m.completionScope()
	scope.Rename = m.renameGeneration
	return scope
}
func (m *Model) deleteCompletionScope() completionScope {
	scope := m.completionScope()
	scope.Delete = m.deleteGeneration
	return scope
}
