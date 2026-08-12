package keys

import (
	"strconv"
	"strings"
)

// Canonical reduces a key to what the terminal actually sends, so two spellings of one key compare
// equal.
//
// NeoMutt writes the same key several ways: `\Cf` and `\006` are both Ctrl-F, `\e` and `\033` are
// both Escape, `<space>` and a literal space are the same character. Its own tables mix them — the
// octal form in the binding lists, the symbolic form in the manual — so ANY check that compares
// keys as strings is comparing spellings and will report a free key that is taken.
//
// That is not a hypothetical: mailforge's fetch macro was put on `\Cf` after a check found it
// unused, while `\006` sat on forget-passphrase four lines further down the same generated file.
// NeoMutt kept the last binding it read, Ctrl-F forgot the user's passphrases, and nothing in the
// interface could have shown it — both rows looked like they had a key of their own.
func Canonical(k string) string {
	var b strings.Builder
	for i := 0; i < len(k); {
		switch {
		case k[i] == '\\' && i+1 < len(k):
			c, n := escape(k[i+1:])
			if n == 0 {
				b.WriteByte(k[i])
				i++
				continue
			}
			b.WriteByte(c)
			i += 1 + n

		case k[i] == '<':
			if j := strings.IndexByte(k[i:], '>'); j > 0 {
				b.WriteString(named(strings.ToLower(k[i : i+j+1])))
				i += j + 1
				continue
			}
			b.WriteByte(k[i])
			i++

		default:
			b.WriteByte(k[i])
			i++
		}
	}
	return b.String()
}

// escape reads one backslash escape and returns the byte it stands for, with how much it consumed.
func escape(s string) (byte, int) {
	if s == "" {
		return 0, 0
	}
	switch s[0] {
	case 'C', 'c':
		// Ctrl-x is the letter with the top three bits cleared: \Cf is 0x06.
		if len(s) < 2 {
			return 0, 0
		}
		return s[1] & 0x1f, 2
	case 'e', 'E':
		return 0x1b, 1
	case 't':
		return '\t', 1
	case 'r':
		return '\r', 1
	case 'n':
		return '\n', 1
	}
	// Octal, one to three digits: \033 is Escape.
	n := 0
	for n < 3 && n < len(s) && s[n] >= '0' && s[n] <= '7' {
		n++
	}
	if n == 0 {
		return 0, 0
	}
	v, err := strconv.ParseInt(s[:n], 8, 16)
	if err != nil {
		return 0, 0
	}
	return byte(v), n
}

// named maps the symbolic names that stand for a character. Anything else — <up>, <f5>, <home> —
// is left as its own token, lowercased, which is already unambiguous.
func named(name string) string {
	switch name {
	case "<space>":
		return " "
	case "<tab>":
		return "\t"
	case "<enter>", "<return>":
		return "\n"
	case "<esc>", "<escape>":
		return "\x1b"
	case "<backspace>":
		return "\b"
	}
	return name
}
