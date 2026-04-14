package deliver

import (
	"fmt"
	"os/exec"
	"time"
)

// TypeAndSend simulates typing text followed by Enter.
func TypeAndSend(text string) error {
	if err := Type(text); err != nil {
		return err
	}
	return key("Return")
}

// SendEnter presses Enter without typing any text first.
// Waits briefly so physical modifier keys have time to release.
func SendEnter() error {
	time.Sleep(100 * time.Millisecond)
	return key("Return")
}

func key(name string) error {
	if path, err := exec.LookPath("ydotool"); err == nil {
		// ydotool uses Linux evdev key names (e.g. "enter"), not X11 names ("Return").
		ydoName := name
		if name == "Return" {
			ydoName = "enter"
		}
		cmd := exec.Command(path, "key", ydoName)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("ydotool key: %w: %s", err, out)
		}
		return nil
	}
	if path, err := exec.LookPath("wtype"); err == nil {
		cmd := exec.Command(path, "-k", name)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("wtype key: %w: %s", err, out)
		}
		return nil
	}
	return fmt.Errorf("no text input tool found")
}

// Type simulates typing text into the focused app.
// Uses ydotool (kernel-level uinput), falls back to wtype (wlroots only).
func Type(text string) error {
	// Wait for all physical modifier keys to fully release.
	// Our chord release fires on the first key-up, but Ctrl/Shift
	// may still be held for a few ms — typing now would produce shortcuts.
	time.Sleep(100 * time.Millisecond)

	if path, err := exec.LookPath("ydotool"); err == nil {
		cmd := exec.Command(path, "type", "--delay", "50", "--key-delay", "2", "--", text)
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
