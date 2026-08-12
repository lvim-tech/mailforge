package verify

import "testing"

// TestParseList covers the four shapes real servers send.
//
// The first version of the parser looked for "the last quoted string", which is correct only when
// the mailbox name happens to be quoted. With an unquoted name the last quotes belong to the
// DELIMITER, so every folder came back named "." — which is exactly what the first live run of the
// wizard showed, on all five rows at once.
func TestParseList(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		attrs []string
		delim string
		want  string
	}{
		{
			name:  "quoted name",
			line:  `* LIST (\HasNoChildren \Sent) "." "Sent"`,
			attrs: []string{`\HasNoChildren`, `\Sent`},
			delim: ".",
			want:  "Sent",
		},
		{
			name:  "unquoted name — the case that was broken",
			line:  `* LIST (\HasNoChildren \Sent) "." Sent`,
			attrs: []string{`\HasNoChildren`, `\Sent`},
			delim: ".",
			want:  "Sent",
		},
		{
			name:  "name with a space",
			line:  `* LIST (\HasNoChildren) "/" "[Gmail]/Sent Mail"`,
			attrs: []string{`\HasNoChildren`},
			delim: "/",
			want:  "[Gmail]/Sent Mail",
		},
		{
			name:  "NIL delimiter",
			line:  `* LIST (\Noselect \HasChildren) NIL "[Gmail]"`,
			attrs: []string{`\Noselect`, `\HasChildren`},
			delim: "NIL",
			want:  "[Gmail]",
		},
		{
			name:  "no attributes",
			line:  `* LIST () "." INBOX`,
			attrs: nil,
			delim: ".",
			want:  "INBOX",
		},
	}
	for _, tc := range cases {
		attrs, delim, got := parseList(tc.line)
		if delim != tc.delim {
			t.Errorf("%s: delimiter %q, want %q", tc.name, delim, tc.delim)
		}
		if got != tc.want {
			t.Errorf("%s: name %q, want %q", tc.name, got, tc.want)
		}
		if len(attrs) != len(tc.attrs) {
			t.Errorf("%s: %d attributes, want %d (%v)", tc.name, len(attrs), len(tc.attrs), attrs)
			continue
		}
		for i := range attrs {
			if attrs[i] != tc.attrs[i] {
				t.Errorf("%s: attribute %d is %q, want %q", tc.name, i, attrs[i], tc.attrs[i])
			}
		}
	}
}

// TestParseListMalformed: a reply this program cannot read must produce no name rather than a
// fragment of one — a folder called "(" would end up in somebody's configuration.
func TestParseListMalformed(t *testing.T) {
	for _, l := range []string{
		"* LIST",
		"* LIST no parens here",
		"",
		`* LIST (\Sent`,
	} {
		if _, _, name := parseList(l); name != "" {
			t.Errorf("%q produced a name: %q", l, name)
		}
	}
}

// TestTokenHandlesEscapes: a mailbox may legitimately contain a quote or a backslash, and a parser
// that stops at the first quote would truncate the name silently.
func TestTokenHandlesEscapes(t *testing.T) {
	got, rest := token(`"a \"quoted\" name" trailing`)
	if got != `a "quoted" name` {
		t.Errorf("got %q", got)
	}
	if rest != " trailing" {
		t.Errorf("rest %q", rest)
	}
	got, _ = token(`bare`)
	if got != "bare" {
		t.Errorf("bare atom: %q", got)
	}
}

// TestHasAttrIsCaseInsensitive: the case of \Sent is not guaranteed by the protocol, and an
// exact comparison silently finds nothing — which looks like a server with no special folders.
func TestHasAttrIsCaseInsensitive(t *testing.T) {
	attrs := []string{`\HasNoChildren`, `\sent`}
	if !hasAttr(attrs, `\Sent`) {
		t.Errorf("case-insensitive match failed")
	}
	if hasAttr(attrs, `\Drafts`) {
		t.Errorf("matched an attribute that is not there")
	}
}
