package sys

import (
	"fmt"
	"os"
	"os/exec"
)

// KeyforgeBin is the generator mailforge hands the question to.
//
// mailforge does not generate passwords. The rules for what one may contain — length, symbols, the
// characters a printed backup sheet has claimed as noise — are decisions with a screen of their
// own in keyforge, and a second implementation here would be a second opinion about the same
// question, wrong in a different way and only discovered when a password made by one cannot be
// read back by the other.
const KeyforgeBin = "keyforge"

// keyforgeReturnFD is the environment variable keyforge reads to learn where to write the value it
// was asked for. Kept in step with internal/sys/handback.go in keyforge.
const keyforgeReturnFD = "KEYFORGE_RETURN_FD"

// handbackFD is the descriptor number the child sees. ExtraFiles[0] is always 3: the child gets
// 0, 1 and 2 as its standard streams and the extras follow immediately.
const handbackFD = 3

// Handback is the open pipe a launched keyforge writes its answer into.
type Handback struct {
	r, w *os.File
}

// Keyforge builds the command that runs keyforge as a generator, with a pipe for the answer.
//
// The value comes back through an inherited pipe and not through a file, the clipboard or stdout —
// see the same reasoning spelled out in keyforge's internal/sys/handback.go. Here the consequence
// worth naming is that nothing on this side ever has a path to unlink or a clipboard to clear: the
// pipe exists between two processes and is gone when either closes it.
//
// The caller is expected to hand the terminal over — tea.ExecProcess — because keyforge is a full
// screen program, and then to call Value on the returned Handback.
func Keyforge() (*exec.Cmd, *Handback, error) {
	if !Have(KeyforgeBin) {
		return nil, nil, fmt.Errorf("%s is not on PATH — mailforge asks it to generate passwords", KeyforgeBin)
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, nil, fmt.Errorf("opening a pipe for the answer: %w", err)
	}
	cmd := Interactive(KeyforgeBin)
	cmd.ExtraFiles = []*os.File{w}
	cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%d", keyforgeReturnFD, handbackFD))
	return cmd, &Handback{r: r, w: w}, nil
}

// Into reads what keyforge sent back straight into a locked buffer.
//
// INTO A SECRET, never through a string. mailforge tells the person at the password field that
// what they type "never becomes an ordinary string in this program", and a generated password is
// the same password — arriving by a different door does not make it less of one. Secret.ReadFrom
// copies the pipe into memory that is mlocked and excluded from core dumps; a []byte on the way
// there would be an ordinary heap allocation, live until the collector happened to reuse it.
//
// It CLOSES THIS PROCESS'S COPY of the write end first, and that is not tidying — it is what makes
// the read terminate. exec duplicated the descriptor into the child, so the pipe has two writers
// until this happens; a reader waits for the last of them to go, and a parent holding its own copy
// open waits for itself forever.
//
// Nothing arriving is not an error. Quitting keyforge without pressing [S] is an ordinary way to
// change your mind, and the caller finds the buffer empty and leaves the field as it was.
func (h *Handback) Into(s *Secret) error {
	if h == nil || s == nil {
		return nil
	}
	h.w.Close()
	defer h.r.Close()
	s.Reset()
	if _, err := s.ReadFrom(h.r); err != nil {
		return fmt.Errorf("reading what %s sent back: %w", KeyforgeBin, err)
	}
	// A generated password never contains a newline, and keyforge writes the value alone. This
	// is for the shell-shaped mistake of an echo somewhere in between, which would otherwise
	// store a password carrying an invisible character that no login accepts and no screen shows.
	s.TruncateAt('\n')
	s.TruncateAt('\r')
	return nil
}

// Close releases the pipe when the command was never run.
func (h *Handback) Close() {
	if h == nil {
		return
	}
	h.w.Close()
	h.r.Close()
}
