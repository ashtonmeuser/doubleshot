//go:build darwin && arm64

package power

import "testing"

func TestRestorationPolicy(t *testing.T) {
	for _, test := range []struct {
		name             string
		display, ac      State
		clear, wantError bool
	}{
		{"no monitor on battery", No, No, true, false},
		{"no monitor on AC", No, Yes, true, false},
		{"no monitor with unknown power", No, Unknown, true, false},
		{"monitor on battery", Yes, No, true, false},
		{"preserve native clamshell mode", Yes, Yes, false, false},
		{"monitor with unknown power", Yes, Unknown, false, true},
		{"unknown monitor on battery", Unknown, No, false, true},
		{"unknown monitor on AC", Unknown, Yes, false, true},
		{"both unknown", Unknown, Unknown, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			clear, err := shouldClearLidOverride(Status{ExternalDisplay: test.display, ACPower: test.ac, LidClosed: Unknown})
			if clear != test.clear || (err != nil) != test.wantError {
				t.Fatalf("clear=%t error=%v; want clear=%t error=%t", clear, err, test.clear, test.wantError)
			}
		})
	}
}
