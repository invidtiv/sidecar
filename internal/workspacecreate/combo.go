package workspacecreate

import (
	"strings"

	"github.com/marcus/sidecar/internal/modal"
)

func comboExactOrAllFilter(items []modal.DropdownItem) modal.ComboFilterFunc {
	return func(query string, item modal.DropdownItem) bool {
		if query == "" || comboQueryMatchesItemExactly(query, items) {
			return true
		}
		q := strings.ToLower(query)
		if strings.Contains(strings.ToLower(item.Label), q) {
			return true
		}
		if item.Value != "" && strings.Contains(strings.ToLower(item.Value), q) {
			return true
		}
		if item.Desc != "" && strings.Contains(strings.ToLower(item.Desc), q) {
			return true
		}
		return false
	}
}

func comboQueryMatchesItemExactly(query string, items []modal.DropdownItem) bool {
	for _, it := range items {
		if query == it.Value || query == it.Label {
			return true
		}
	}
	return false
}
