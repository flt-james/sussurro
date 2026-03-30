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

// Listener reads raw evdev events and detects chord presses.
type Listener struct {
	file     *os.File
	events   chan Event
	stop     chan struct{}
	wg       sync.WaitGroup
	held     map[uint16]bool
	chordActive bool
}

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

// NewListener opens the evdev device and returns a Listener.
func NewListener(devicePath string) (*Listener, error) {
	f, err := os.Open(devicePath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w (are you in the input group?)", devicePath, err)
	}

	l := &Listener{
		file:   f,
		events: make(chan Event, 16),
		stop:   make(chan struct{}),
		held:   make(map[uint16]bool),
	}

	l.wg.Add(1)
	go l.readLoop()

	return l, nil
}

// Events returns the channel of detected events.
func (l *Listener) Events() <-chan Event {
	return l.events
}

// Close stops the listener and releases resources.
func (l *Listener) Close() {
	close(l.stop)
	l.file.Close()
	l.wg.Wait()
	close(l.events)
}

func (l *Listener) readLoop() {
	defer l.wg.Done()

	buf := make([]byte, inputEventSize)
	for {
		select {
		case <-l.stop:
			return
		default:
		}

		// Set a read deadline so we can check stop periodically
		l.file.SetReadDeadline(time.Now().Add(100 * time.Millisecond))

		n, err := l.file.Read(buf)
		if err != nil {
			if os.IsTimeout(err) {
				continue
			}
			select {
			case <-l.stop:
				return
			default:
				slog.Error("evdev read error", "error", err)
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

func (l *Listener) handleKey(code uint16, value int32) {
	// Only track keys we care about
	if !isTrackedKey(code) {
		return
	}

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
