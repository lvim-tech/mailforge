package sys

import "testing"

// Append used to drop overflow bytes with no signal at all. That is tolerable for a field the user
// is typing into and it is data loss when the buffer holds a `pass` entry about to be written back:
// the trailer past the capacity was silently re-encrypted away.
func TestSecretRecordsOverflow(t *testing.T) {
	s, err := NewSecret(4)
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	defer s.Close()

	s.Append([]byte("abcd"))
	if s.Overflowed() {
		t.Fatal("a buffer filled exactly to capacity reported an overflow")
	}
	s.Append([]byte("e"))
	if !s.Overflowed() {
		t.Fatal("bytes were dropped and nothing said so")
	}
	s.Reset()
	if s.Overflowed() {
		t.Error("the overflow flag survived a Reset")
	}
}

// Snapshot exists for the goroutine boundary: the check runs in the background while the password
// field is still editable, and handing over the live buffer made the bytes on the wire a mixture of
// what was typed before and after.
func TestSecretSnapshotIsIndependent(t *testing.T) {
	s, err := NewSecret(64)
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	defer s.Close()
	s.Set([]byte("hunter2"))

	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	defer snap.Close()

	if !snap.Equal(s) {
		t.Fatal("the snapshot does not hold the same value")
	}
	s.Set([]byte("something else entirely"))
	if snap.Equal(s) {
		t.Fatal("editing the original changed the snapshot")
	}
	snap.Use(func(v string) {
		if v != "hunter2" {
			t.Errorf("the snapshot holds %q, want %q", v, "hunter2")
		}
	})
}

func TestSnapshotOfNilIsNil(t *testing.T) {
	var s *Secret
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot of nil: %v", err)
	}
	if snap != nil {
		t.Error("a nil secret produced a buffer")
	}
}
