package deliver

import (
	"fmt"
	"os/exec"
	"time"
)

// Type simulates typing text into the focused app.
// Uses ydotool (kernel-level uinput), falls back to wtype (wlroots only).
func Type(text string) error {
	// Wait for all physical modifier keys to fully release.
	// Our chord release fires on the first key-up, but Ctrl/Shift
	// may still be held for a few ms — typing now would produce shortcuts.
	time.Sleep(100 * time.Millisecond)

	if path, err := exec.LookPath("ydotool"); err == nil {
		cmd := exec.Command(path, "type", "--key-delay", "2", "--", text)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("ydotool: %w: %s", err, out)
		}
		return nil
	}

	if path, err := exec.LookPath("wtype"); err == nil {
		cmd := exec.Command(path, "-d", "2", "--", text)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("wtype: %w: %s", err, out)
		}
		return nil
	}

	return fmt.Errorf("no text input tool found (install ydotool or wtype)")
}
