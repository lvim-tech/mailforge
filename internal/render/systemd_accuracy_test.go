package render

import (
	"strings"
	"testing"

	"github.com/lvim-tech/mailforge/internal/config"
)

// TestTimerAccuracyIsSmallNextToTheInterval.
//
// AccuracySec is how much systemd may delay a timer to batch it with other wakeups. A minute of
// that on a one-minute timer means the fetch lands somewhere between one and two minutes and never
// at the period that was asked for — the setting accepted, written, displayed, and not honoured.
// A minute of it on a five-minute timer is ordinary slack and worth the power it saves.
func TestTimerAccuracyIsSmallNextToTheInterval(t *testing.T) {
	for _, tc := range []struct {
		every int
		want  string
	}{
		{30, "AccuracySec=1s"},
		{60, "AccuracySec=1s"},
		{61, "AccuracySec=1min"},
		{300, "AccuracySec=1min"},
	} {
		c := config.Default()
		c.SyncEvery = tc.every
		if got := MbsyncTimer(c); !strings.Contains(got, tc.want) {
			t.Errorf("a %ds timer should carry %q:\n%s", tc.every, tc.want, got)
		}
	}
}
