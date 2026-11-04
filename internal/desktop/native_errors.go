package desktop

import (
	"errors"
	"fmt"
)

// Native bridge statuses distinguish unsupported systems from failures on a
// supported Mac. In particular, a failed screenshot must not suggest granting
// screen recording again when permission was already accepted.
func permissionError(code int) error {
	switch code {
	case 0:
		return nil
	case 1:
		return errors.New("Allow Miya in System Settings → Privacy & Security → Screen Recording on your Mac, then retry. macOS may require reopening the app.")
	case 2:
		return errors.New("Allow Miya in System Settings → Privacy & Security → Accessibility on your Mac, then retry.")
	case 3:
		return errors.New("Remote desktop requires macOS 14 or later")
	case 4:
		return ErrWindowUnavailable
	case 5:
		return errors.New("Unable to list shareable windows. Your Mac may be waking up; try again shortly. If this continues, check that your Mac is signed in.")
	case 6:
		return errors.New("The target window is not shareable. Complete this step on your Mac.")
	case 7:
		return errors.New("Unable to capture the target screen. Retry, or complete this step on your Mac if it continues.")
	case 8:
		return errors.New("Screen capture timed out. Your Mac may be waking up; try again shortly. If this continues, check that your Mac is online and signed in.")
	case 9:
		return errors.New("Failed to encode the screen image. Try again.")
	case 10:
		return errors.New("The screen image exceeds the size limit. Try again.")
	case 11:
		return errors.New("Unable to allocate screen image memory. Try again later.")
	case 13:
		return errors.New("The Mac screen is unavailable, possibly asleep. Waking it up; try again shortly.")
	case 12:
		return errors.New("Unable to wake the Mac display. Wake it on your Mac and retry.")
	default:
		return fmt.Errorf("Remote action failed (error %d). Try again.", code)
	}
}

func captureError(code int, diagnostic string) error {
	err := permissionError(code)
	if err == nil || diagnostic == "" {
		return err
	}
	return fmt.Errorf("%w (%s)", err, diagnostic)
}
