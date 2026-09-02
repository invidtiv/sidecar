package main

import (
	"fmt"

	"github.com/marcus/sidecar/internal/agentactivity/manifests"
)

// ancestry is how two commits sit in upstream's history, in the vocabulary
// GitHub's compare API uses for base...head.
type ancestry string

const (
	// ancestryIdentical means base and head are the same commit.
	ancestryIdentical ancestry = "identical"
	// ancestryAhead means head descends from base.
	ancestryAhead ancestry = "ahead"
	// ancestryBehind means head is an ancestor of base.
	ancestryBehind ancestry = "behind"
	// ancestryDiverged means neither commit contains the other.
	ancestryDiverged ancestry = "diverged"
)

// pinDecision is what the rollback guard concluded about the ref a sync
// resolved. It is nil when there was nothing to compare against or when the
// resolved commit moves the pin forward, which is the ordinary case and needs
// no note anywhere.
type pinDecision struct {
	// requestedRef is the ref the run asked for, held is the commit that was
	// vendored, and resolved is the commit requestedRef names.
	requestedRef string
	resolved     string
	held         string
	state        ancestry

	// keptPin is true when the guard overrode the resolved commit and vendored
	// the one the previous lock recorded.
	keptPin bool

	// lockNote goes in upstream.lock.json, which is committed, so it is the
	// durable record that a pin is where it is on purpose. The report renders
	// every lock note, so this is also the report's first line about it.
	lockNote string
	// reportNote is what to do about it, for the report and the summary only.
	reportNote string
}

// holdPin keeps a sync from moving the vendored pin backwards.
//
// The sync's default ref is a moving branch, so the commit it resolves is
// normally a descendant of the one the lock records and this returns the source
// untouched. When it is not -- an upstream release tag cut behind the tree, a
// force-push, a hand-typed ref -- vendoring it would quietly replace the
// vendored tree with an older one. The manifests are only half of what a sync
// extracts: aliases.upstream.json and authority.upstream.json come out of
// Herdr's source and move independently of the manifest files, so "no manifest
// changed" is no evidence that nothing moved backwards. That is exactly how
// pull request 323 dropped Muse from the authority table while every vendored
// manifest stayed byte-identical.
//
// This is the rule chooseManifests already applies to a published manifest that
// is older than the bundled one, lifted from one agent to the whole tree: keep
// the newer copy, and record which won and why.
//
// A ref the maintainer typed is obeyed. Pinning an older ref by hand is a
// rehearsal or a bisect, and a flag that silently vendors something other than
// what it names would be worse than the rollback; the guard warns loudly in the
// report and in the lock instead. Where the ref came from the default, an
// ancestry that cannot be established -- a compare call that failed, a history
// that was rewritten -- refuses the sync rather than guessing, because both
// guesses are bad: vendoring anyway is the bug this guard exists for, and
// silently keeping the pin writes a tree identical to the committed one, which
// opens no pull request and so tells nobody.
func holdPin(src source, previous *manifests.Lock, requestedRef string, explicitRef bool) (source, *pinDecision, error) {
	if previous == nil || previous.Herdr.Commit == "" {
		return src, nil, nil
	}
	locked := previous.Herdr.Commit
	resolved := src.commit()
	if resolved == "" || resolved == locked {
		return src, nil, nil
	}

	decision := &pinDecision{requestedRef: requestedRef, resolved: resolved, held: resolved}
	state, err := src.compare(resolved, locked)
	if err != nil {
		if !explicitRef {
			return nil, nil, fmt.Errorf(
				"cannot tell whether ref %s (%s) is behind the commit the lock records (%s): %w. "+
					"A sync that cannot prove it is moving forward could roll the vendored tree back "+
					"without a single manifest byte changing; pass --ref %s to vendor it anyway",
				requestedRef, shortSHA(resolved), shortSHA(locked), err, requestedRef)
		}
		decision.state = ""
		decision.lockNote = fmt.Sprintf(
			"Could not check whether ref %s (%s) is behind the previously vendored commit %s: %v. "+
				"It was vendored anyway because --ref asked for it.",
			requestedRef, shortSHA(resolved), shortSHA(locked), err)
		decision.reportNote = "Read the ref and commit above before merging: the rollback guard could not run."
		return src, decision, nil
	}
	decision.state = state

	switch state {
	case ancestryIdentical, ancestryBehind:
		// Behind means the locked commit is an ancestor of the resolved one:
		// the pin moves forward, which is the whole point of a sync.
		return src, nil, nil

	case ancestryDiverged:
		if !explicitRef {
			return nil, nil, fmt.Errorf(
				"ref %s (%s) and the commit the lock records (%s) have diverged; neither contains the other, "+
					"so this sync cannot show it is moving forward. Upstream history was rewritten, or the ref "+
					"names another line of development; pass --ref %s to vendor it anyway",
				requestedRef, shortSHA(resolved), shortSHA(locked), requestedRef)
		}
		decision.lockNote = fmt.Sprintf(
			"Ref %s (%s) has diverged from the previously vendored commit %s; neither contains the other. "+
				"It was vendored anyway because --ref asked for it.",
			requestedRef, shortSHA(resolved), shortSHA(locked))
		decision.reportNote = "Read the vendored diff before merging: this sync is not a fast-forward."
		return src, decision, nil

	case ancestryAhead:
		// The locked commit descends from the resolved one, so vendoring the
		// resolved one is a rollback.
		if explicitRef {
			decision.lockNote = fmt.Sprintf(
				"Ref %s (%s) is behind the previously vendored commit %s, so this sync moved the pin backwards. "+
					"It was vendored anyway because --ref asked for it.",
				requestedRef, shortSHA(resolved), shortSHA(locked))
			decision.reportNote = "Do not merge this as a routine sync: it takes upstream files from before " +
				"the ones already vendored."
			return src, decision, nil
		}
		pinned, err := src.pinTo(locked)
		if err != nil {
			return nil, nil, fmt.Errorf("hold the vendored pin at %s: %w", shortSHA(locked), err)
		}
		decision.keptPin = true
		decision.held = locked
		decision.lockNote = fmt.Sprintf(
			"The pin stayed at %s: ref %s resolves to %s, which %s already descends from, so vendoring it "+
				"would have moved the vendored tree backwards.",
			shortSHA(locked), requestedRef, shortSHA(resolved), shortSHA(locked))
		decision.reportNote = fmt.Sprintf(
			"Nothing was taken from %s. Pass --ref %s to vendor it deliberately.",
			shortSHA(resolved), requestedRef)
		return pinned, decision, nil
	}
	return nil, nil, fmt.Errorf("compare %s...%s returned %q, which is not an ancestry this tool knows",
		resolved, locked, state)
}

// shortSHA trims a commit to the length git prints, and leaves anything that is
// not one alone.
func shortSHA(commit string) string {
	if len(commit) <= 12 {
		return commit
	}
	return commit[:12]
}
