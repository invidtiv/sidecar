package workspacediff

import "testing"

func TestParseSpecAndIdentity(t *testing.T) {
	wt, ok := ParseSpec("wt")
	if !ok || wt.Kind != TargetWorkingTree || wt.Identity() != IdentityWorkingTree {
		t.Fatalf("wt: %+v ok=%v identity=%q", wt, ok, wt.Identity())
	}
	commit, ok := ParseSpec("abc1234")
	if !ok || commit.Kind != TargetCommit || commit.A != "abc1234" || commit.Identity() != "c:abc1234" {
		t.Fatalf("commit: %+v ok=%v identity=%q", commit, ok, commit.Identity())
	}
	if commit.Identity() == IdentityWorkingTree {
		t.Fatal("commit identity must not be wt")
	}
	two, ok := ParseSpec("aaa1111..bbb2222")
	if !ok || two.Kind != TargetRange || two.Dots != ".." || two.Identity() != "r:aaa1111..bbb2222" {
		t.Fatalf("two-dot: %+v ok=%v identity=%q", two, ok, two.Identity())
	}
	three, ok := ParseSpec("aaa1111...bbb2222")
	if !ok || three.Dots != "..." || three.Identity() != "r:aaa1111...bbb2222" {
		t.Fatalf("three-dot: %+v ok=%v identity=%q", three, ok, three.Identity())
	}
	if two.Identity() == three.Identity() {
		t.Fatal(".. and ... must not collapse")
	}
	ident, ok := ParseSpec("c:deadbeef")
	if !ok || ident.Identity() != "c:deadbeef" {
		t.Fatalf("identity form: %+v ok=%v", ident, ok)
	}
	if _, ok := ParseSpec(""); ok {
		t.Fatal("empty spec parsed")
	}
}

func TestWorkingTreeTargetIdentity(t *testing.T) {
	if WorkingTreeTarget().Identity() != IdentityWorkingTree {
		t.Fatal("zero/working-tree target must be wt")
	}
	if (Target{}).Identity() != IdentityWorkingTree {
		t.Fatal("zero Target must be wt, not empty or HEAD")
	}
}
