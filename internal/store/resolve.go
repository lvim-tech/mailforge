package store

// Existing returns the candidates that actually have an entry, in the order they were given.
//
// The plural is the point. One hit is an answer; two hits are a FINDING — the same password
// encrypted under two names, which drift apart the moment either is rotated and neither of which
// says it is the live one. Callers that only want somewhere to read from use Resolve; callers that
// have to tell the person something use this.
func Existing(candidates []string) []string {
	var found []string
	for _, name := range candidates {
		if name != "" && Exists(name) {
			found = append(found, name)
		}
	}
	return found
}

// Resolve picks the entry to read: the first candidate that exists, or the first candidate when
// none do.
//
// Falling back to candidates[0] rather than to "" is what makes a brand-new account work. Nothing
// exists for it yet, and the honest name for a password that has not been stored is the one it
// WILL have — which is also the name the audit then reports as missing, so the message names a
// path the person can go and look at.
func Resolve(candidates []string) string {
	if found := Existing(candidates); len(found) > 0 {
		return found[0]
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

// Duplicated reports the entries when more than one candidate is occupied, and nil otherwise.
//
// Separate from Existing so the calling code reads as the question it is asking. `if dup :=
// store.Duplicated(...); dup != nil` says what the branch is for; `if len(store.Existing(...)) > 1`
// makes the reader work it out.
func Duplicated(candidates []string) []string {
	if found := Existing(candidates); len(found) > 1 {
		return found
	}
	return nil
}
