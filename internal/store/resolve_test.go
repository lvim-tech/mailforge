package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// storeWith builds a throwaway pass store holding the named entries, and points the package at it.
//
// Only the .gpg files are made: Exists stats a path and asks nothing about the contents, so an
// empty file is as real to it as an encrypted one. What this must NOT do is touch the developer's
// own store, which is why PASSWORD_STORE_DIR is set for the test rather than assumed.
func storeWith(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		p := filepath.Join(dir, filepath.FromSlash(n)+".gpg")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("making %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte("not really encrypted\n"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", p, err)
		}
	}
	t.Setenv("PASSWORD_STORE_DIR", dir)
}

const (
	flat   = "mail/hello@example.com"
	nested = "mail/example.com/hello@example.com"
)

// TestResolveFindsTheOtherLayout is the bug this whole change exists for: mailforge looked only
// where its own layout said to, found nothing in a store filed by domain, and reported a missing
// password for one that was sitting right there.
func TestResolveFindsTheOtherLayout(t *testing.T) {
	storeWith(t, nested)
	if got := Resolve([]string{flat, nested}); got != nested {
		t.Fatalf("Resolve = %q, want the entry that exists (%q)", got, nested)
	}
}

// TestResolvePrefersTheFirstCandidate: the fallback must not become the rule. When the configured
// layout has an entry, that is the answer, whatever else is lying around.
func TestResolvePrefersTheFirstCandidate(t *testing.T) {
	storeWith(t, flat, nested)
	if got := Resolve([]string{flat, nested}); got != flat {
		t.Fatalf("Resolve = %q, want the leading candidate (%q)", got, flat)
	}
}

// TestResolveOnAnEmptyStoreNamesWhereItWouldGo: a new account has nothing yet, and "" would make
// the audit report a missing password without saying where it was missing FROM.
func TestResolveOnAnEmptyStoreNamesWhereItWouldGo(t *testing.T) {
	storeWith(t)
	if got := Resolve([]string{flat, nested}); got != flat {
		t.Fatalf("Resolve = %q, want the leading candidate (%q)", got, flat)
	}
	if got := Resolve(nil); got != "" {
		t.Fatalf("Resolve(nil) = %q, want empty", got)
	}
}

// TestDuplicatedOnlyFiresOnMoreThanOne. One entry is an answer; two are a finding. A check that
// reported a single hit would put a warning on every correctly configured account.
func TestDuplicatedOnlyFiresOnMoreThanOne(t *testing.T) {
	t.Run("one is not a duplicate", func(t *testing.T) {
		storeWith(t, nested)
		if got := Duplicated([]string{flat, nested}); got != nil {
			t.Fatalf("Duplicated = %v, want nil", got)
		}
	})
	t.Run("none is not a duplicate", func(t *testing.T) {
		storeWith(t)
		if got := Duplicated([]string{flat, nested}); got != nil {
			t.Fatalf("Duplicated = %v, want nil", got)
		}
	})
	t.Run("two are", func(t *testing.T) {
		storeWith(t, flat, nested)
		got := Duplicated([]string{flat, nested})
		if strings.Join(got, ",") != flat+","+nested {
			t.Fatalf("Duplicated = %v, want both in candidate order", got)
		}
	})
}

// TestExistingSkipsEmptyNames: PassCandidates collapses to one entry when both layouts produce the
// same name, and a caller that built its slice by hand may leave a gap. An empty name must not be
// statted — filepath.Join would turn it into the store directory itself, which always exists.
func TestExistingSkipsEmptyNames(t *testing.T) {
	storeWith(t, flat)
	if got := Existing([]string{"", flat, ""}); len(got) != 1 || got[0] != flat {
		t.Fatalf("Existing = %v, want just %q", got, flat)
	}
}
