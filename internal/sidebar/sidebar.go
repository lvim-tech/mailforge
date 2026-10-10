/*
Package sidebar remembers which accounts the NeoMutt sidebar has folded open.

It is a separate package from config for one reason, and it is the same reason the file is separate:
this is STATE, not configuration. It changes when a key is pressed, nobody chose any particular
value of it, and it carries no meaning a person would want to read or edit. config.json is the
opposite on all three counts — decisions, made deliberately, in a file the user is invited to open.

Everything here degrades to "nothing is folded open" rather than failing. The state is a
convenience; a sidebar that comes up folded shut because the state file is unreadable is a small
annoyance, and a NeoMutt that refuses to start because of it would be a large one.
*/
package sidebar

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/lvim-tech/mailforge/internal/config"
)

// State is the whole of what is remembered.
type State struct {
	// Expanded holds the addresses whose folders are showing.
	Expanded []string `json:"expanded"`
	// Current is the account being read, written by the folder-hook on every change of folder.
	//
	// Stored rather than passed on the command line because the key that needs it cannot know
	// it: a NeoMutt macro is fixed text, so the one bound to Enter is the same string in every
	// account. This field is how the keypress and the folder-hook meet.
	Current string `json:"current"`

	// CurrentIsInbox says whether the open folder is that account's INBOX, and it is what keeps
	// the fold key from folding when it should only be opening.
	//
	// The fold key opens the highlighted row first — that is the only way it can act on the row
	// the user is looking at — and the rows are of two kinds. An account's own row IS its INBOX,
	// and Enter there means fold. A folder row under an unfolded account means open that folder,
	// and folding the account then would hide the folder that was just opened.
	//
	// Written by a second folder-hook, for every path ending in /INBOX, defined after the
	// per-account ones so that it is the last word when both match.
	CurrentIsInbox bool `json:"current_is_inbox"`
}

// Load reads the state. A missing or unreadable file is no state, and no error: see the package
// comment — there is nothing here worth refusing to work over.
func Load() State {
	b, err := os.ReadFile(config.StatePath())
	if err != nil {
		return State{}
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}
	}
	return s
}

// Save writes the state atomically, creating its directory.
func (s State) Save() error {
	p := config.StatePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	sort.Strings(s.Expanded)
	// An empty LIST, not null. Go marshals a nil slice as null, which is a correct JSON document
	// and a confusing little file to open: "expanded": null reads as a field that went wrong,
	// while [] reads as nothing folded open — which is what it means.
	if s.Expanded == nil {
		s.Expanded = []string{}
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteAtomic(p, append(b, '\n'), 0o600)
}

// Open reports whether an account is folded open.
func (s State) Open(address string) bool {
	for _, a := range s.Expanded {
		if a == address {
			return true
		}
	}
	return false
}

// OpenSet is the same answer in the shape the renderer wants.
func (s State) OpenSet() map[string]bool {
	out := make(map[string]bool, len(s.Expanded))
	for _, a := range s.Expanded {
		out[a] = true
	}
	return out
}

// Toggle folds one account the other way and reports what it became.
func (s *State) Toggle(address string) bool {
	if address == "" {
		return false
	}
	for i, a := range s.Expanded {
		if a == address {
			s.Expanded = append(s.Expanded[:i], s.Expanded[i+1:]...)
			return false
		}
	}
	s.Expanded = append(s.Expanded, address)
	return true
}

// SetCurrent records the account being read, and writes only if something actually changed.
//
// The check is not an optimisation. This runs from a folder-hook, which fires on every change of
// folder, and rewriting the file each time would mean a write per folder opened for a value that is
// usually the same one.
//
// It clears CurrentIsInbox, always. This hook matches every folder of the account; the one that
// knows an INBOX was opened runs AFTER it and sets the flag back. So the pair reads "assume a plain
// folder, then be corrected", which is the only order in which two hooks that cannot see each other
// can agree.
func SetCurrent(address string) error {
	s := Load()
	if s.Current == address && !s.CurrentIsInbox {
		return nil
	}
	s.Current = address
	s.CurrentIsInbox = false
	return s.Save()
}

// SetCurrentIsInbox records that the open folder is an INBOX, which makes it an account's own row
// in the folded sidebar.
func SetCurrentIsInbox() error {
	s := Load()
	if s.CurrentIsInbox {
		return nil
	}
	s.CurrentIsInbox = true
	return s.Save()
}

// Prune drops addresses that are no longer accounts.
//
// A removed account can sit in Expanded forever otherwise, and the day somebody adds an account at
// the same address it would come up folded open for reasons nobody could trace.
func (s *State) Prune(known map[string]bool) {
	kept := s.Expanded[:0]
	for _, a := range s.Expanded {
		if known[a] {
			kept = append(kept, a)
		}
	}
	s.Expanded = kept
	if s.Current != "" && !known[s.Current] {
		s.Current = ""
		s.CurrentIsInbox = false
	}
}

// ErrNoAccount says a fold was asked for without an account to fold: no folder has been opened
// since NeoMutt started, so the folder-hook has not run and nothing knows what to act on.
var ErrNoAccount = errors.New("no account is open, so there is nothing to fold")
