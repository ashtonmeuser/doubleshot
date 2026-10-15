//go:build darwin && arm64

package power

// State distinguishes an unavailable observation from a definite negative.
type State int

const (
	Unknown State = -1
	No      State = 0
	Yes     State = 1
)

// Status is an observational snapshot of display, power, and lid position.
type Status struct {
	ExternalDisplay State
	ACPower         State
	LidClosed       State
}
