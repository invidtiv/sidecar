package shellstate

// ObserveLiveCountWrite is the shells.json writer-boundary hook. Every path
// that replaces the file calls it after computing the next live list and
// before the rename. before and after are len(shells), not tombstones.
// identityRemoval is true only for RemoveAtPath, RemoveIfUnchangedAtPath, and
// the workspace RemoveShell equivalent — the only functions allowed to shrink
// the live list.
//
// Tests wrap this instead of grepping call sites.
var ObserveLiveCountWrite = func(path string, before, after int, identityRemoval bool) {}
