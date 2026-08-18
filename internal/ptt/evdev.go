package ptt

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// Linux input event constants
const (
	evKey = 1

	keyEsc        = 1
	keyCapsLock   = 58
	keyLeftCtrl   = 29
	keyLeftShift  = 42
	keyLeftAlt    = 56
	keyRightAlt   = 100
	keySpace      = 57
	keyRightCtrl  = 97
	keyRightShift = 54

	keyRelease = 0
	keyPress   = 1
)

// inputEvent matches the Linux input_event struct (24 bytes on 64-bit)
type inputEvent struct {
	Sec     int64
	Usec    int64
	Type    uint16
	Code    uint16
	Value   int32
}

const inputEventSize = int(unsafe.Sizeof(inputEvent{}))

// Event types sent to the listener
type Event int

const (
	EventChordPress   Event = iota // all chord keys now held
	EventChordRelease              // a chord key was released
	EventEsc                       // Esc pressed
)

// Listener reads raw evdev events and detects chord presses. It can read from
// several devices at once; key state is merged across all of them so a chord
// split across multiple HID interfaces of one keyboard is still detected.
//
// Devices are re-attached automatically: a supervisor goroutine re-runs
// discovery every deviceRescanInterval and opens anything not currently being
// read. This covers a keyboard that is unplugged and replugged (the kernel
// destroys the device behind our fd, so the read loop detaches it and the
// supervisor opens the replacement) as well as a keyboard plugged in after
// startup. Note that evdev paths are recycled — a replugged keyboard usually
// lands back on the same /dev/input/eventN — so attachment is tracked by which
// paths we currently hold an open fd for, not by path alone.
type Listener struct {
	events   chan Event
	stop     chan struct{}
	wg       sync.WaitGroup
	discover func() ([]string, error)

	mu          sync.Mutex // guards held + chordActive across per-device readLoops
	held        map[uint16]bool
	chordActive bool

	devMu       sync.Mutex // guards devices + stopped + lastOpenErr
	devices     map[string]*os.File
	stopped     bool
	lastOpenErr error
}

// deviceRescanInterval is how often the supervisor looks for devices to attach.
// Reading /proc/bus/input/devices is cheap, and this bounds how long the daemon
// stays deaf after a replug.
const deviceRescanInterval = 2 * time.Second

// chordKeys are the keys that make up Ctrl+Shift+Space
var chordKeys = map[uint16]bool{
	keyLeftCtrl:  true,
	keyRightCtrl: true,
	keyLeftShift: true,
	keyRightShift: true,
	keySpace:     true,
}

// cancelKeys are tracked for Ctrl+Shift+Alt cancel combo
var cancelKeys = map[uint16]bool{
	keyLeftCtrl:   true,
	keyRightCtrl:  true,
	keyLeftShift:  true,
	keyRightShift: true,
	keyLeftAlt:    true,
	keyRightAlt:   true,
}

// isTrackedKey returns true for any key we care about
func isTrackedKey(code uint16) bool {
	return chordKeys[code] || cancelKeys[code]
}

// FindKeyboards returns the paths of every real typing keyboard found in
// /proc/bus/input/devices. Real keyboards are identified by the presence of
// "sysrq" in their Handlers line, which cleanly excludes power buttons,
// system-control / consumer-control HID interfaces, and other non-typing devices.
//
// All matching devices are returned because a single physical keyboard often
// exposes several sysrq-capable evdev interfaces, and the actual typing keys
// may land on any one of them (e.g. the Keychron C3 Pro emits Ctrl+Shift+Space
// only on its second interface). The caller listens on all of them at once.
func FindKeyboards() ([]string, error) {
	kbs, err := scanKeyboards()
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(kbs))
	for _, kb := range kbs {
		slog.Info("auto-discovered keyboard", "name", kb.Name, "path", kb.Path)
		paths = append(paths, kb.Path)
	}
	return paths, nil
}

// keyboard is one discovered typing device.
type keyboard struct {
	Name string
	Path string
}

// scanKeyboards implements the discovery described on FindKeyboards. It is kept
// silent because the device supervisor re-runs it every couple of seconds;
// callers decide what is worth logging.
func scanKeyboards() ([]keyboard, error) {
	data, err := os.ReadFile("/proc/bus/input/devices")
	if err != nil {
		return nil, fmt.Errorf("read /proc/bus/input/devices: %w", err)
	}

	var kbs []keyboard
	for _, block := range strings.Split(string(data), "\n\n") {
		var name, eventName string
		hasSysrq := false
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "N: Name="):
				name = strings.Trim(strings.TrimPrefix(line, "N: Name="), "\"")
			case strings.HasPrefix(line, "H: Handlers="):
				for _, tok := range strings.Fields(strings.TrimPrefix(line, "H: Handlers=")) {
					if tok == "sysrq" {
						hasSysrq = true
					}
					if strings.HasPrefix(tok, "event") {
						eventName = tok
					}
				}
			}
		}
		if hasSysrq && eventName != "" {
			kbs = append(kbs, keyboard{Name: name, Path: "/dev/input/" + eventName})
		}
	}

	if len(kbs) == 0 {
		return nil, fmt.Errorf("no keyboard (sysrq-capable input device) found in /proc/bus/input/devices")
	}
	return kbs, nil
}

// deviceName reads a device's name from sysfs, for logging. Returns "" if the
// device has already gone away.
func deviceName(path string) string {
	data, err := os.ReadFile(filepath.Join("/sys/class/input", filepath.Base(path), "device", "name"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// FindDeviceByName scans /dev/input/ for a device whose name contains the given substring.
func FindDeviceByName(nameSubstr string) (string, error) {
	matches, err := filepath.Glob("/dev/input/event*")
	if err != nil {
		return "", fmt.Errorf("glob /dev/input: %w", err)
	}

	for _, path := range matches {
		// Read the device name from sysfs
		base := filepath.Base(path)
		sysPath := filepath.Join("/sys/class/input", base, "device", "name")
		data, err := os.ReadFile(sysPath)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(data))
		if strings.Contains(strings.ToLower(name), strings.ToLower(nameSubstr)) {
			slog.Info("found input device", "name", name, "path", path)
			return path, nil
		}
	}

	return "", fmt.Errorf("no input device matching %q found", nameSubstr)
}

// NewListener reads from a fixed set of evdev devices. The paths are re-opened
// automatically if those devices disappear and come back.
func NewListener(devicePaths ...string) (*Listener, error) {
	if len(devicePaths) == 0 {
		return nil, fmt.Errorf("no input devices given")
	}
	paths := append([]string(nil), devicePaths...)
	return newListener(func() ([]string, error) { return paths, nil })
}

// NewAutoListener reads from every keyboard found by FindKeyboards, and keeps
// following them across unplug/replug and newly attached keyboards.
func NewAutoListener() (*Listener, error) {
	return newListener(func() ([]string, error) {
		kbs, err := scanKeyboards()
		if err != nil {
			return nil, err
		}
		paths := make([]string, 0, len(kbs))
		for _, kb := range kbs {
			paths = append(paths, kb.Path)
		}
		return paths, nil
	})
}

func newListener(discover func() ([]string, error)) (*Listener, error) {
	l := &Listener{
		events:   make(chan Event, 16),
		stop:     make(chan struct{}),
		discover: discover,
		held:     make(map[uint16]bool),
		devices:  make(map[string]*os.File),
	}

	// Require at least one device up front so a misconfigured path or a missing
	// input-group membership still fails loudly at startup rather than silently
	// waiting forever for a keyboard that will never arrive.
	attached, err := l.attachAvailable()
	if err != nil {
		return nil, err
	}
	if attached == 0 {
		if l.lastOpenErr != nil {
			return nil, fmt.Errorf("open evdev: %w (are you in the input group?)", l.lastOpenErr)
		}
		return nil, fmt.Errorf("no usable input device found")
	}

	l.wg.Add(1)
	go l.supervise()

	return l, nil
}

// attachAvailable opens every discovered device that is not already attached
// and starts a read loop for it. It returns the number now attached.
func (l *Listener) attachAvailable() (int, error) {
	paths, err := l.discover()
	if err != nil {
		return 0, err
	}

	l.devMu.Lock()
	defer l.devMu.Unlock()
	if l.stopped {
		return len(l.devices), nil
	}

	for _, p := range paths {
		if _, ok := l.devices[p]; ok {
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			// Normal right after a replug: udev may not have applied
			// permissions yet. The next scan retries.
			slog.Debug("open evdev device", "path", p, "error", err)
			l.lastOpenErr = err
			continue
		}
		l.devices[p] = f
		slog.Info("keyboard attached", "name", deviceName(p), "path", p)
		l.wg.Add(1)
		go l.readLoop(p, f)
	}
	return len(l.devices), nil
}

// supervise re-attaches devices as they appear.
func (l *Listener) supervise() {
	defer l.wg.Done()

	ticker := time.NewTicker(deviceRescanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			if _, err := l.attachAvailable(); err != nil {
				slog.Debug("keyboard rescan", "error", err)
			}
		}
	}
}

// detach drops a device that has gone away, so the supervisor reopens its path
// when the device comes back.
func (l *Listener) detach(path string) {
	l.devMu.Lock()
	f, ok := l.devices[path]
	delete(l.devices, path)
	l.devMu.Unlock()
	if ok {
		f.Close()
	}
}

// Events returns the channel of detected events.
func (l *Listener) Events() <-chan Event {
	return l.events
}

// Close stops the listener and releases resources.
func (l *Listener) Close() {
	l.devMu.Lock()
	if l.stopped {
		l.devMu.Unlock()
		return
	}
	l.stopped = true
	files := make([]*os.File, 0, len(l.devices))
	for p, f := range l.devices {
		files = append(files, f)
		delete(l.devices, p)
	}
	l.devMu.Unlock()

	close(l.stop)
	for _, f := range files {
		f.Close()
	}
	l.wg.Wait()
	close(l.events)
}

func (l *Listener) readLoop(path string, file *os.File) {
	defer l.wg.Done()

	buf := make([]byte, inputEventSize)
	for {
		select {
		case <-l.stop:
			return
		default:
		}

		// Set a read deadline so we can check stop periodically
		file.SetReadDeadline(time.Now().Add(100 * time.Millisecond))

		n, err := file.Read(buf)
		if err != nil {
			if os.IsTimeout(err) {
				continue
			}
			select {
			case <-l.stop:
				return
			default:
				// The device is gone (typically a replug: ENODEV). Drop it and
				// let the supervisor pick up its replacement.
				slog.Warn("keyboard disconnected", "path", path, "error", err)
				l.detach(path)
				l.forgetKeys()
				return
			}
		}
		if n != inputEventSize {
			continue
		}

		ev := inputEvent{
			Sec:   int64(binary.LittleEndian.Uint64(buf[0:8])),
			Usec:  int64(binary.LittleEndian.Uint64(buf[8:16])),
			Type:  binary.LittleEndian.Uint16(buf[16:18]),
			Code:  binary.LittleEndian.Uint16(buf[18:20]),
			Value: int32(binary.LittleEndian.Uint32(buf[20:24])),
		}

		if ev.Type != evKey {
			continue
		}

		l.handleKey(ev.Code, ev.Value)
	}
}

// forgetKeys clears held-key state after a device disappears. A key that was
// down when the keyboard vanished never produces a release event, so without
// this the stale "held" entry would make the next chord fire early — and if the
// chord was active, recording would run forever. Releasing it here ends that
// recording the same way lifting the keys would.
func (l *Listener) forgetKeys() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.held = make(map[uint16]bool)
	if l.chordActive {
		l.chordActive = false
		select {
		case l.events <- EventChordRelease:
		default:
		}
	}
}

func (l *Listener) handleKey(code uint16, value int32) {
	// Only track keys we care about
	if !isTrackedKey(code) {
		return
	}

	// held + chordActive are shared across per-device readLoops.
	l.mu.Lock()
	defer l.mu.Unlock()

	switch value {
	case keyPress:
		l.held[code] = true
	case keyRelease:
		delete(l.held, code)
	default:
		return // autorepeat, ignore
	}

	hasCtrl := l.held[keyLeftCtrl] || l.held[keyRightCtrl]
	hasShift := l.held[keyLeftShift] || l.held[keyRightShift]
	hasAlt := l.held[keyLeftAlt] || l.held[keyRightAlt]
	hasSpace := l.held[keySpace]

	// Ctrl+Shift+Alt (without Space) = cancel
	if hasCtrl && hasShift && hasAlt && !hasSpace && value == keyPress {
		select {
		case l.events <- EventEsc:
		default:
		}
		return
	}

	// Ctrl+Shift+Space = chord
	chordComplete := hasCtrl && hasShift && hasSpace

	if chordComplete && !l.chordActive {
		l.chordActive = true
		select {
		case l.events <- EventChordPress:
		default:
		}
	} else if !chordComplete && l.chordActive {
		l.chordActive = false
		select {
		case l.events <- EventChordRelease:
		default:
		}
	}
}
