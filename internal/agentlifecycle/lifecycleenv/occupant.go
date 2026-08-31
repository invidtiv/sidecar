package lifecycleenv

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// LiveProviderGeneration reports the generation of the process that occupies the
// pane right now.
//
// This is deliberately not derived the way [Context.ProcessGeneration] is.
// That one walks *up* from the reporting hook to find the provider that spawned
// it, which answers "who is talking". This one asks the process table which
// process is currently the pane's provider, which answers "who is running".
//
// The two questions have the same answer while a provider is alive and
// reporting normally, and different answers in exactly the case the generation
// fence exists for: a hook whose provider has already exited or been replaced
// still knows its own ancestry, so a fence built only from the report's own view
// would let that late report win. Comparing against a value re-derived from live
// process state at write time is what makes the fence decisive — the same lesson
// the lifecycle store learned when a stored identity was compared with a copy of
// itself and every mismatch check became unreachable.
//
// When the pane has no provider running, the pane's own root process is
// returned. That is a real generation and it will not match any provider's, so
// a late report loses rather than being accepted for want of something to
// compare against.
func LiveProviderGeneration(panePID int) (string, error) {
	if panePID <= 0 {
		return "", errors.New("pane has no root process")
	}
	self := os.Getpid()
	children, err := childrenOf(panePID)
	if err != nil {
		// Without a process table there is no independent answer. Refusing is
		// correct: silently falling back to the reporter's own claim would turn
		// the fence off precisely when it cannot be evaluated.
		return "", err
	}

	// Exclude this process. A hook invoked directly by the pane's shell is
	// itself a direct child, and counting it would make the reporter its own
	// live occupant — the self-comparison this function exists to avoid.
	var candidates []int
	for _, pid := range children {
		if pid == self {
			continue
		}
		candidates = append(candidates, pid)
	}
	switch len(candidates) {
	case 0:
		// Nothing runs under the shell, so the shell itself occupies the pane.
		return generationString(panePID), nil
	case 1:
		return generationString(candidates[0]), nil
	}

	// More than one child is unusual (a provider plus a stray background job).
	// Prefer the ancestor of this process when it is among them, because that
	// is provably the provider that produced this report and provably still
	// alive; otherwise take the lowest pid for a stable, order-independent
	// answer rather than an arbitrary one.
	ancestors := map[int]bool{}
	for pid, i := self, 0; pid > 1 && i < maxAncestry; i++ {
		ancestors[pid] = true
		parent, err := parentPID(pid)
		if err != nil {
			break
		}
		pid = parent
	}
	best := 0
	for _, pid := range candidates {
		if ancestors[pid] {
			return generationString(pid), nil
		}
		if best == 0 || pid < best {
			best = pid
		}
	}
	return generationString(best), nil
}

// childrenOf lists the pids whose parent is ppid.
func childrenOf(ppid int) ([]int, error) {
	out, err := exec.Command("ps", "-Ao", "pid=,ppid=").Output()
	if err != nil {
		return nil, err
	}
	var kids []int
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		parent, err := strconv.Atoi(fields[1])
		if err != nil || parent != ppid {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		kids = append(kids, pid)
	}
	return kids, nil
}
