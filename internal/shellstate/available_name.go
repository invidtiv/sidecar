package shellstate

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// EnsureWithAvailableNameAtPath records a new identity using the preferred
// display name, or its first available numbered variant. Existing identities
// retain their name and metadata. Allocation and insertion share the manifest
// lock so concurrent automatic creations cannot claim the same name.
func EnsureWithAvailableNameAtPath(path string, def Definition) error {
	if strings.TrimSpace(def.TmuxName) == "" {
		return &Error{Kind: KindValidation, Msg: "shell session name is required"}
	}
	name, err := NormalizeName(def.DisplayName)
	if err != nil {
		return err
	}
	return mutateManifest(path, func(m *manifest) error {
		used := make(map[string]bool, len(m.Shells))
		for _, existing := range m.Shells {
			if existing.TmuxName == def.TmuxName && sameNamespace(existing.Namespace, def.Namespace) {
				return nil
			}
			used[existing.DisplayName] = true
		}
		def.DisplayName = name
		for n := 2; used[def.DisplayName]; n++ {
			suffix := fmt.Sprintf(" %d", n)
			base := name
			if len(base)+len(suffix) > MaxNameBytes {
				base = base[:MaxNameBytes-len(suffix)]
				for !utf8.ValidString(base) {
					base = base[:len(base)-1]
				}
			}
			def.DisplayName = strings.TrimSpace(base) + suffix
		}
		m.Shells = append(m.Shells, def)
		m.Tombstones = dropTombstone(m.Tombstones, Identity{TmuxName: def.TmuxName, Namespace: def.Namespace})
		return nil
	})
}
