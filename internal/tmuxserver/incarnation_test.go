package tmuxserver

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestIncarnationStatesAreDistinct(t *testing.T) {
	u := Unknown()
	a := Absent()
	p := Present(1, 2, 3)

	if !u.IsUnknown() || u.IsAbsent() || u.IsPresent() {
		t.Fatalf("Unknown() = %v", u)
	}
	if !a.IsAbsent() || a.IsUnknown() || a.IsPresent() {
		t.Fatalf("Absent() = %v", a)
	}
	if !p.IsPresent() || p.IsUnknown() || p.IsAbsent() {
		t.Fatalf("Present() = %v", p)
	}
	if u == a || u == p || a == p {
		t.Fatal("Unknown, Absent, and Present must not compare equal")
	}
	if got := (Incarnation{}); got != u {
		t.Fatalf("zero value = %v, want Unknown", got)
	}
}

func TestPresentDropsInodeWithoutCtime(t *testing.T) {
	if got := Present(42, 0, 0); !got.IsUnknown() {
		t.Fatalf("inode without ctime or pid = %v, want Unknown (inode reuse is not identity)", got)
	}
	if got := Present(42, 0, 9); got != Present(0, 0, 9) {
		t.Fatalf("inode without ctime, with pid = %v, want pid-only Present", got)
	}
	if got := Present(0, 0, 0); !got.IsUnknown() {
		t.Fatalf("empty Present = %v, want Unknown", got)
	}
	if got := Present(7, 8, 0); !got.IsPresent() {
		t.Fatalf("inode+ctime = %v, want Present", got)
	}
	if Present(7, 8, 0) == Present(7, 9, 0) {
		t.Fatal("different ctimes compared equal; a rebound socket would look like the same server")
	}
	if Present(7, 8, 0) == Present(8, 8, 0) {
		t.Fatal("different inodes compared equal")
	}
}

func TestPresentIsComparable(t *testing.T) {
	a := Present(1, 2, 3)
	b := Present(1, 2, 3)
	c := Present(1, 2, 4)
	if a != b {
		t.Fatal("identical Present values must compare equal")
	}
	if a == c {
		t.Fatal("Present values that differ in pid must not compare equal")
	}
	absentA, absentB := Absent(), Absent()
	if absentA != absentB {
		t.Fatal("Absent must compare equal to itself")
	}
}

func TestIncarnationEqualTreatsUnspecifiedPIDAsSameServer(t *testing.T) {
	sock := Present(1, 2, 0)
	withPID := Present(1, 2, 99)
	otherPID := Present(1, 2, 100)

	if !sock.Equal(withPID) {
		t.Fatal("Present(1,2,0).Equal(Present(1,2,99)) = false, want true (pid 0 is unspecified)")
	}
	if !withPID.Equal(sock) {
		t.Fatal("Equal must be symmetric for unspecified pid")
	}
	if withPID.Equal(otherPID) {
		t.Fatal("Present(1,2,99).Equal(Present(1,2,100)) = true, want false (both pids known, different servers)")
	}
	if sock == withPID {
		t.Fatal("== is field-wise and must stay so; Equal is the same-server predicate")
	}

	if sock.Equal(Absent()) {
		t.Fatal("Present.Equal(Absent) = true, want false")
	}
	if Unknown().Equal(Absent()) {
		t.Fatal("Unknown.Equal(Absent) = true, want false")
	}
	if Absent().Equal(withPID) {
		t.Fatal("Absent.Equal(Present) = true, want false")
	}
	if !Unknown().Equal(Unknown()) {
		t.Fatal("Unknown must Equal itself")
	}
	if !Absent().Equal(Absent()) {
		t.Fatal("Absent must Equal itself")
	}

	if !Combine(sock, 99).Equal(sock) {
		t.Fatal("Combine(socket, pid) must Equal the socket-stat observation of the same server")
	}
	if !Combine(sock, 99).Equal(withPID) {
		t.Fatal("Combine(socket, 99) must Equal Present(inode, ctime, 99)")
	}

	pidOnly := Present(0, 0, 99)
	if !pidOnly.Equal(Present(0, 0, 99)) {
		t.Fatal("pid-only Present must Equal itself")
	}
	if pidOnly.Equal(Present(0, 0, 100)) {
		t.Fatal("different pid-only Presents must not Equal")
	}
	if !pidOnly.Equal(withPID) {
		t.Fatal("pid-only 99 must Equal socket+pid 99 (same server, one source missing the socket)")
	}
	if pidOnly.Equal(sock) {
		t.Fatal("pid-only 99 must not Equal socket-stat with unspecified pid (no shared specified field)")
	}
}

func TestFromFileInfoUsesInodeAndCtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "socket")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	got := FromFileInfo(info)
	if !got.IsPresent() {
		t.Fatalf("FromFileInfo = %v, want Present", got)
	}
	again, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if FromFileInfo(again) != got {
		t.Fatalf("same file produced %v then %v", got, FromFileInfo(again))
	}

	other := filepath.Join(dir, "other")
	if err := os.WriteFile(other, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	otherInfo, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}
	if FromFileInfo(otherInfo) == got {
		t.Fatal("two files produced the same incarnation")
	}
}

func TestFromPathMissingIsUnknown(t *testing.T) {
	if got := FromPath(filepath.Join(t.TempDir(), "nope")); !got.IsUnknown() {
		t.Fatalf("missing path = %v, want Unknown (not Absent)", got)
	}
}

func TestSocketStatsTmuxenvPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMUX_TMPDIR", dir)
	sockDir := filepath.Join(dir, "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(sockDir, "default")
	if err := os.WriteFile(sock, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := Socket()
	info, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if got != FromFileInfo(info) {
		t.Fatalf("Socket() = %v, want %v from the tmuxenv socket path", got, FromFileInfo(info))
	}
}

func TestCombine(t *testing.T) {
	sock := Present(1, 2, 0)
	got := Combine(sock, 99)
	if got != Present(1, 2, 99) {
		t.Fatalf("Combine(present, pid) = %v", got)
	}
	if !got.Equal(sock) {
		t.Fatalf("Combine(socket, pid).Equal(socket) = false, want true")
	}
	if got := Combine(Unknown(), 99); got != Present(0, 0, 99) {
		t.Fatalf("Combine(unknown, pid) = %v", got)
	}
	if got := Combine(Absent(), 99); !got.IsAbsent() {
		t.Fatalf("Combine(absent, pid) = %v, want Absent", got)
	}
	if got := Combine(sock, 0); got != sock {
		t.Fatalf("Combine(present, 0) = %v, want unchanged socket", got)
	}
	if got := Combine(Unknown(), 0); !got.IsUnknown() {
		t.Fatalf("Combine(unknown, 0) = %v, want Unknown", got)
	}
}

func TestParsePID(t *testing.T) {
	tests := []struct {
		in  string
		pid int
		ok  bool
	}{
		{"90028", 90028, true},
		{" 12 ", 12, true},
		{"#{pid}", 0, false},
		{"", 0, false},
		{"0", 0, false},
		{"-3", 0, false},
		{"nope", 0, false},
	}
	for _, tt := range tests {
		pid, ok := ParsePID(tt.in)
		if pid != tt.pid || ok != tt.ok {
			t.Errorf("ParsePID(%q) = %d, %v; want %d, %v", tt.in, pid, ok, tt.pid, tt.ok)
		}
	}
}

func TestFromFileInfoNilIsUnknown(t *testing.T) {
	if got := FromFileInfo(nil); !got.IsUnknown() {
		t.Fatalf("FromFileInfo(nil) = %v, want Unknown", got)
	}
}

func TestPresentDoesNotUseWallClockEqualityAsIdentity(t *testing.T) {
	// Two observations taken a moment apart of different sockets must not
	// collapse just because ctime resolution is coarse; inode is in the id.
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	if err := os.WriteFile(a, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(0) // keep the test honest if the FS timestamps collide
	if err := os.WriteFile(b, []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	ia, _ := os.Stat(a)
	ib, _ := os.Stat(b)
	if FromFileInfo(ia) == FromFileInfo(ib) {
		t.Fatal("distinct files shared an incarnation")
	}
}
