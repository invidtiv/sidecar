package agentcatalog

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

type helpIdentity struct {
	path      string
	directory string
	size      int64
	modified  int64
}

var helpCapabilities = struct {
	sync.Mutex
	text map[helpIdentity]string
}{text: make(map[helpIdentity]string)}

// supportedHelpArgs runs before the caller starts the provider, never inside
// its readiness grace period. Missing or older commands keep the existing
// argv; a failed probe refuses an unsafe guess and is not cached. The cache
// follows the executable rather than a version.
func (f Family) supportedHelpArgs(workDir string, extra []string) ([]string, error) {
	if len(f.HelpSupportedArgs) == 0 {
		return nil, nil
	}
	workDir, err := filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("resolve provider working directory: %w", err)
	}
	path, err := exec.LookPath(f.Command)
	if err != nil {
		return nil, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect provider %q executable: %w", f.Command, err)
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve provider %q executable: %w", f.Command, err)
	}
	identity := helpIdentity{path: realPath, directory: workDir, size: info.Size(), modified: info.ModTime().UnixNano()}
	// A shim (mise on this machine) can change what it dispatches without
	// changing itself. Script wrappers and differently named symlink targets
	// are probed each time; native provider binaries can cache their help.
	cacheable := filepath.Base(realPath) == filepath.Base(f.Command) && nativeHelpExecutable(realPath)
	helpCapabilities.Lock()
	defer helpCapabilities.Unlock()
	help, cached := helpCapabilities.text[identity]
	cached = cached && cacheable
	if !cached {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, path, "--help")
		command.Dir = workDir
		command.WaitDelay = 100 * time.Millisecond
		output, err := command.Output()
		if err != nil {
			return nil, fmt.Errorf("probe provider %q capabilities: %w", f.Command, err)
		}
		help = string(output)
		if cacheable {
			helpCapabilities.text[identity] = help
		}
	}
	words := strings.Fields(help)
	var args []string
	for _, arg := range f.HelpSupportedArgs {
		if slices.Contains(extra, arg) || conflictingHelpArg(extra, f.HelpArgConflicts[arg]) {
			continue
		}
		for _, word := range words {
			if word == arg {
				args = append(args, arg)
				break
			}
		}
	}
	return args, nil
}

func conflictingHelpArg(extra, conflicts []string) bool {
	for _, arg := range extra {
		for _, conflict := range conflicts {
			if arg == conflict || strings.HasPrefix(arg, conflict+"=") {
				return true
			}
		}
	}
	return false
}

func nativeHelpExecutable(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	var prefix [2]byte
	_, err = file.Read(prefix[:])
	return err == nil && string(prefix[:]) != "#!"
}
