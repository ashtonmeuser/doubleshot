//go:build darwin && arm64

package power

import "errors"

func (n *Controller) Disable() error { return n.setLidOverride(true) }

// Restore preserves native closed-display mode on AC with an external monitor.
// This policy is shared by the native and fake backends.
func (n *Controller) Restore() error {
	status, readErr := ReadStatus()
	clear, err := shouldClearLidOverride(status)
	if err != nil {
		return errors.Join(readErr, err)
	}
	// A lid-position query failure does not invalidate display/power observations.
	if !clear {
		return nil
	}
	return n.setLidOverride(false)
}

func shouldClearLidOverride(status Status) (bool, error) {
	switch status.ExternalDisplay {
	case No:
		return true, nil
	case Yes:
		switch status.ACPower {
		case No:
			return true, nil
		case Yes:
			return false, nil
		default:
			return false, errors.New("cannot determine AC power for lid restoration")
		}
	default:
		return false, errors.New("cannot determine external display state for lid restoration")
	}
}
