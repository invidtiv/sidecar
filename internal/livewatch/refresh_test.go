package livewatch

import "testing"

func TestRefresherIdleOffersNothing(t *testing.T) {
	var r Refresher
	if r.Begin(false) {
		t.Fatal("Begin() = true with no change observed; an idle pane must not re-read")
	}
	if r.Pending() {
		t.Fatal("Pending() = true before any Observe()")
	}
}

func TestRefresherObserveThenBegin(t *testing.T) {
	var r Refresher
	r.Observe()
	if !r.Pending() {
		t.Fatal("Pending() = false after Observe()")
	}
	if !r.Begin(false) {
		t.Fatal("Begin() = false with a change owed")
	}
	if r.Pending() {
		t.Fatal("Pending() = true after Begin() claimed the change")
	}
	if !r.InFlight() {
		t.Fatal("InFlight() = false after Begin()")
	}
}

func TestRefresherCoalescesBurst(t *testing.T) {
	var r Refresher
	for range 10 {
		r.Observe()
	}
	if !r.Begin(false) {
		t.Fatal("Begin() = false after a burst")
	}
	if r.Done() {
		t.Fatal("Done() = true; ten signals before any re-read owe one re-read, not two")
	}
}

func TestRefresherSingleFlight(t *testing.T) {
	var r Refresher
	r.Observe()
	if !r.Begin(false) {
		t.Fatal("first Begin() = false")
	}
	r.Observe()
	if r.Begin(false) {
		t.Fatal("Begin() = true while a re-read is in flight; re-reads must not stack")
	}
	if !r.Done() {
		t.Fatal("Done() = false; the change that arrived mid-flight is still owed")
	}
	if !r.Begin(false) {
		t.Fatal("Begin() = false after Done() released the slot")
	}
}

func TestRefresherSuppressedChangeIsDeferredNotDropped(t *testing.T) {
	var r Refresher
	r.Observe()
	if r.Begin(true) {
		t.Fatal("Begin(suppressed=true) = true; the host's veto must hold")
	}
	if !r.Pending() {
		t.Fatal("a suppressed change was dropped; it must stay owed until the veto lifts")
	}
	if !r.Begin(false) {
		t.Fatal("Begin() = false once the veto lifted")
	}
}

func TestRefresherChangedGatesUnchangedResults(t *testing.T) {
	var r Refresher
	if !r.Changed("a") {
		t.Fatal("Changed() = false on the very first result")
	}
	if r.Changed("a") {
		t.Fatal("Changed() = true for an identical result; this is the repaint flash")
	}
	if !r.Changed("b") {
		t.Fatal("Changed() = false for a genuinely different result")
	}
	if r.Changed("b") {
		t.Fatal("Changed() = true repeating the newly adopted result")
	}
}

func TestRefresherAdoptSuppressesTheFirstSignal(t *testing.T) {
	var r Refresher
	r.Adopt("loaded")
	if r.Changed("loaded") {
		t.Fatal("Changed() = true for the content the initial load already painted")
	}
}

func TestRefresherResetForgetsEverything(t *testing.T) {
	var r Refresher
	r.Observe()
	r.Begin(false)
	r.Changed("a")
	r.Reset()

	if r.Pending() || r.InFlight() {
		t.Fatal("Reset() left work owed or in flight")
	}
	if !r.Changed("a") {
		t.Fatal("Changed() = false after Reset(); a retargeted pane must always paint its first result")
	}
}

func TestFingerprintIsStableAndDiscriminating(t *testing.T) {
	type payload struct {
		Name     string
		Children []string
	}
	a := payload{"epic", []string{"one"}}
	b := payload{"epic", []string{"one"}}
	c := payload{"epic", []string{"one", "two"}}

	if Fingerprint(a) != Fingerprint(b) {
		t.Fatal("equal values fingerprinted differently; every refresh would repaint")
	}
	if Fingerprint(a) == Fingerprint(c) {
		t.Fatal("an added child did not change the fingerprint; the refresh would be invisible")
	}
}

func TestFingerprintUncomparableNeverRepeats(t *testing.T) {
	// A channel cannot be JSON-encoded. Such a value must never compare equal to
	// itself, so a caller that cannot be compared repaints rather than showing
	// content it has no way to verify is current.
	v := make(chan int)
	first := Fingerprint(v)
	second := Fingerprint(v)
	if first == second {
		t.Fatal("an unencodable value fingerprinted stably; stale content would stick")
	}
}

func TestFingerprintStringDiscriminates(t *testing.T) {
	first := FingerprintString("hello")
	second := FingerprintString("hello")
	if first != second {
		t.Fatal("equal strings fingerprinted differently")
	}
	if FingerprintString("hello") == FingerprintString("hello ") {
		t.Fatal("a trailing space did not change the fingerprint")
	}
}
