package omp

import (
	"os"
	"path/filepath"

	"github.com/marcus/sidecar/internal/adapter"
	"github.com/marcus/sidecar/internal/adapter/piagent"
)

func init() {
	adapter.RegisterFactory(func() adapter.Adapter {
		home, _ := os.UserHomeDir()
		return piagent.NewCustom(
			filepath.Join(home, ".omp", "agent", "sessions"),
			"omp",
			"OMP",
			"Ω",
		)
	})
}
