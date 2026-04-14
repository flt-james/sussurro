package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jms301/sussurro-stream/internal/asr"
	"github.com/jms301/sussurro-stream/internal/audio"
	"github.com/jms301/sussurro-stream/internal/config"
	"github.com/jms301/sussurro-stream/internal/deliver"
	"github.com/jms301/sussurro-stream/internal/llm"
	"github.com/jms301/sussurro-stream/internal/logger"
	"github.com/jms301/sussurro-stream/internal/ptt"
	"github.com/jms301/sussurro-stream/internal/window"
)

type State int

const (
	StateIdle            State = iota
	StateRecording             // chord held, audio capturing, streaming whisper
	StateCleaning              // chord released, LLM running
	StateReady                 // final text shown, waiting for tap or Esc
	StatePendingDeliver        // first tap done, waiting for double-tap or timeout
	StateDelivering            // wtype running
	StateEditing               // chord held in READY, recording edit instruction
)

func (s State) String() string {
	return [...]string{"IDLE", "RECORDING", "CLEANING", "READY", "PENDING_DELIVER", "DELIVERING", "EDITING"}[s]
}

func main() {
	configPath := flag.String("config", "", "path to config file")
	debug := flag.Bool("debug", false, "enable debug logging")
	device := flag.String("device", "", "evdev device path (overrides config)")
	noUI := flag.Bool("no-ui", false, "terminal-only mode (no GTK window)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	if *debug {
		cfg.Debug = true
	}

	logger.Init(cfg.Debug)

	// Resolve evdev device
	devicePath := cfg.PTT.Device
	if *device != "" {
		devicePath = *device
	}
	if devicePath == "auto" || devicePath == "" {
		devicePath, err = ptt.FindDeviceByName("Gaming KB")
		if err != nil {
			slog.Error("auto-discover keyboard failed", "error", err)
			os.Exit(1)
		}
	}

	// Initialize components
	slog.Info("initializing", "device", devicePath)

	listener, err := ptt.NewListener(devicePath)
	if err != nil {
		slog.Error("open evdev", "error", err)
		os.Exit(1)
	}
	defer listener.Close()

	capture, err := audio.NewCaptureEngine(cfg.Audio.SampleRate)
	if err != nil {
		slog.Error("init audio", "error", err)
		os.Exit(1)
	}
	defer capture.Close()

	slog.Info("loading ASR model", "path", cfg.Models.ASR.Path)
	whisperEngine, err := asr.NewEngine(
		cfg.Models.ASR.Path,
		cfg.Models.ASR.Threads,
		cfg.Models.ASR.Language,
		cfg.Debug,
	)
	if err != nil {
		slog.Error("init whisper", "error", err)
		os.Exit(1)
	}
	defer whisperEngine.Close()

	var llmEngine *llm.Engine
	if cfg.Models.LLM.Enabled {
		slog.Info("loading LLM model", "path", cfg.Models.LLM.Path)
		llmEngine, err = llm.NewEngine(
			cfg.Models.LLM.Path,
			cfg.Models.LLM.Threads,
			2048,
			cfg.Models.LLM.GPULayers,
			cfg.Debug,
		)
		if err != nil {
			slog.Error("init llm", "error", err)
			os.Exit(1)
		}
		defer llmEngine.Close()
	}

	slog.Info("ready — hold Ctrl+Shift+Space to record")

	// Display abstraction: window or terminal
	var display Display
	if *noUI {
		display = &terminalDisplay{}
	} else {
		overlay := window.New()
		display = &windowDisplay{overlay: overlay}

		// Start the state machine before GTK takes over the main thread
		go func() {
			runStateMachine(cfg, listener, capture, whisperEngine, llmEngine, display)
			overlay.Quit()
		}()

		// GTK main loop — blocks on main thread
		overlay.Run()
		return
	}

	// Terminal mode: run state machine on main goroutine
	runStateMachine(cfg, listener, capture, whisperEngine, llmEngine, display)
}

// Display is the output interface for the state machine.
type Display interface {
	Init()
	ShowRecording()
	ShowEditing()
	UpdateText(text string)
	ShowCleaning()
	ShowReady(text string)
	ShowDelivering(text string)
	ShowCancelled()
	ShowIdle()
	Hide()
}

// terminalDisplay prints to stdout.
type terminalDisplay struct{}

func (d *terminalDisplay) Init()                    {}
func (d *terminalDisplay) ShowRecording()            { fmt.Print("\n  recording... ") }
func (d *terminalDisplay) ShowEditing()              { fmt.Print("\n  recording edit... ") }
func (d *terminalDisplay) UpdateText(text string)    { fmt.Printf("\r\033[K  %s", text) }
func (d *terminalDisplay) ShowCleaning()             { fmt.Print("\n  cleaning up...") }
func (d *terminalDisplay) ShowReady(text string)     { fmt.Printf("\r\033[K  final: %s\n  tap to deliver, hold to edit, Ctrl+Shift+Alt to cancel\n", text) }
func (d *terminalDisplay) ShowDelivering(text string) { fmt.Printf("\n  delivering: %s\n", text) }
func (d *terminalDisplay) ShowCancelled()            { fmt.Print("\n  cancelled\n") }
func (d *terminalDisplay) ShowIdle()                 { fmt.Print("  ready\n") }
func (d *terminalDisplay) Hide()                     {}

// windowDisplay uses the GTK overlay.
type windowDisplay struct {
	overlay *window.Overlay
}

func (d *windowDisplay) Init() {
	d.overlay.Wait()
}

func (d *windowDisplay) ShowRecording() {
	d.overlay.SetText("")
	d.overlay.SetStatus("recording...")
	d.overlay.Show()
}

func (d *windowDisplay) ShowEditing() {
	d.overlay.SetStatus("recording edit...")
}

func (d *windowDisplay) UpdateText(text string) {
	d.overlay.SetText(text)
}

func (d *windowDisplay) ShowCleaning() {
	d.overlay.SetStatus("cleaning up...")
}

func (d *windowDisplay) ShowReady(text string) {
	d.overlay.SetText(text)
	d.overlay.SetStatus("tap to deliver, hold to edit, Ctrl+Shift+Alt to cancel")
}

func (d *windowDisplay) ShowDelivering(text string) {
	d.overlay.SetStatus("delivering...")
}

func (d *windowDisplay) ShowCancelled() {
	d.overlay.Hide()
}

func (d *windowDisplay) ShowIdle() {
	d.overlay.Hide()
}

func (d *windowDisplay) Hide() {
	d.overlay.Hide()
}

func runStateMachine(
	cfg *config.Config,
	listener *ptt.Listener,
	capture *audio.CaptureEngine,
	whisperEngine *asr.Engine,
	llmEngine *llm.Engine,
	display Display,
) {
	display.Init()

	var (
		state           = StateIdle
		stateMu         sync.Mutex
		finalText        string
		audioChan        = make(chan []float32, 256)
		editStart        time.Time
		editCapStarted   bool
		autoDeliver      bool
		doubleTapTimer   *time.Timer
		recordingStart   time.Time
	)

	// Quick taps from idle are too short for a useful recording —
	// treat them as a "deliver Enter" gesture instead.
	const idleTapMax = 250 * time.Millisecond

	streamer := asr.NewStreamer(whisperEngine, cfg.Streaming.Interval, func(text string) {
		display.UpdateText(text)
	})

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-sigCh:
			slog.Info("shutting down")
			return

		case ev, ok := <-listener.Events():
			if !ok {
				return
			}

			stateMu.Lock()
			switch ev {
			case ptt.EventChordPress:
				switch state {
				case StatePendingDeliver:
					// Double-tap: deliver with Enter (or just Enter if no text).
					if doubleTapTimer != nil {
						doubleTapTimer.Stop()
						doubleTapTimer = nil
					}
					state = StateDelivering
					slog.Debug("state", "new", state, "reason", "double-tap-deliver-with-enter")
					display.ShowDelivering(finalText)
					text := finalText
					go func() {
						var err error
						if text == "" {
							err = deliver.SendEnter()
						} else {
							err = deliver.TypeAndSend(text)
						}
						if err != nil {
							slog.Error("deliver", "error", err)
						}
						stateMu.Lock()
						state = StateIdle
						finalText = ""
						stateMu.Unlock()
						display.ShowIdle()
					}()

				case StateIdle:
					state = StateRecording
					recordingStart = time.Now()
					slog.Debug("state", "new", state)
					display.ShowRecording()

					streamer.Reset()
					streamer.Start()

					for len(audioChan) > 0 {
						<-audioChan
					}

					if err := capture.StartRecording(audioChan); err != nil {
						slog.Error("start recording", "error", err)
						state = StateIdle
						stateMu.Unlock()
						continue
					}

					go func() {
						for samples := range audioChan {
							stateMu.Lock()
							s := state
							stateMu.Unlock()
							if s != StateRecording {
								return
							}
							streamer.AppendAudio(samples)
						}
					}()

				case StateCleaning:
					// Tap during cleanup — auto-deliver when done.
					autoDeliver = true
					slog.Debug("state", "note", "auto-deliver queued")

				case StateReady:
					// Record timestamp. Audio capture starts after 400ms
					// hold threshold — quick tap just delivers.
					state = StateEditing
					editStart = time.Now()
					editCapStarted = false
					slog.Debug("state", "new", state)

					go func() {
						time.Sleep(400 * time.Millisecond)
						stateMu.Lock()
						if state != StateEditing {
							stateMu.Unlock()
							return
						}
						editCapStarted = true
						stateMu.Unlock()

						// Still holding — start recording edit instruction.
						display.ShowEditing()
						streamer.Reset()
						streamer.Start()

						for len(audioChan) > 0 {
							<-audioChan
						}

						stateMu.Lock()
						if state != StateEditing {
							stateMu.Unlock()
							return
						}
						stateMu.Unlock()

						if err := capture.StartRecording(audioChan); err != nil {
							slog.Error("start edit recording", "error", err)
							stateMu.Lock()
							state = StateReady
							editCapStarted = false
							stateMu.Unlock()
							display.ShowReady(finalText)
							return
						}

						for samples := range audioChan {
							stateMu.Lock()
							s := state
							stateMu.Unlock()
							if s != StateEditing {
								return
							}
							streamer.AppendAudio(samples)
						}
					}()
				}

			case ptt.EventChordRelease:
				if state == StateEditing {
					elapsed := time.Since(editStart)

					if !editCapStarted {
						// Short tap — wait for possible double-tap.
						state = StatePendingDeliver
						slog.Debug("state", "new", state, "reason", "tap-pending", "elapsed", elapsed)
						doubleTapTimer = time.AfterFunc(350*time.Millisecond, func() {
							stateMu.Lock()
							if state != StatePendingDeliver {
								stateMu.Unlock()
								return
							}
							text := finalText
							if text == "" {
								// Single tap from idle — nothing to deliver.
								state = StateIdle
								stateMu.Unlock()
								slog.Debug("state", "new", StateIdle, "reason", "idle-single-tap-noop")
								display.ShowIdle()
								return
							}
							state = StateDelivering
							stateMu.Unlock()
							slog.Debug("state", "new", StateDelivering, "reason", "single-tap-deliver-no-enter")
							display.ShowDelivering(text)
							if err := deliver.Type(text + " "); err != nil {
								slog.Error("deliver", "error", err)
							}
							stateMu.Lock()
							state = StateIdle
							finalText = ""
							stateMu.Unlock()
							display.ShowIdle()
						})
					} else {
						capture.Stop()
						for len(audioChan) > 0 {
							streamer.AppendAudio(<-audioChan)
						}
						editBuf := streamer.Stop()
						// Long hold — process edit instruction.
						state = StateCleaning
						slog.Debug("state", "new", state, "reason", "edit-instruction")
						display.ShowCleaning()
						origText := finalText

						go func() {
							instruction, err := whisperEngine.Transcribe(editBuf)
							if err != nil {
								slog.Error("edit transcription", "error", err)
								stateMu.Lock()
								state = StateReady
								stateMu.Unlock()
								display.ShowReady(origText)
								return
							}

							slog.Info("edit instruction", "text", instruction)

							if llmEngine != nil && instruction != "" {
								edited, err := llmEngine.EditText(origText, instruction)
								if err != nil {
									slog.Error("llm edit", "error", err)
								} else {
									slog.Info("edit applied", "before", origText, "after", edited)
									origText = edited
								}
							} else {
								slog.Info("edit skipped", "llm_nil", llmEngine == nil, "instruction_empty", instruction == "")
							}

							stateMu.Lock()
							finalText = origText
							state = StateReady
							stateMu.Unlock()
							display.ShowReady(origText)
						}()
					}
				} else if state == StateRecording {
					if time.Since(recordingStart) < idleTapMax {
						// Tap too short to be a real recording — treat as a
						// "deliver Enter on double-tap" gesture.
						capture.Stop()
						streamer.Stop()
						finalText = ""
						state = StatePendingDeliver
						slog.Debug("state", "new", state, "reason", "idle-tap-pending-enter")
						display.ShowIdle()
						doubleTapTimer = time.AfterFunc(350*time.Millisecond, func() {
							stateMu.Lock()
							if state != StatePendingDeliver {
								stateMu.Unlock()
								return
							}
							state = StateIdle
							stateMu.Unlock()
							slog.Debug("state", "new", StateIdle, "reason", "idle-single-tap-noop")
							display.ShowIdle()
						})
						stateMu.Unlock()
						continue
					}
					state = StateCleaning
					slog.Debug("state", "new", state)
					capture.Stop()
					finalBuf := streamer.Stop()
					display.ShowCleaning()

					go func() {
						start := time.Now()

						text, err := whisperEngine.Transcribe(finalBuf)
						if err != nil {
							slog.Error("final transcription", "error", err)
							stateMu.Lock()
							state = StateIdle
							stateMu.Unlock()
							display.ShowIdle()
							return
						}

						slog.Debug("final transcription", "text", text,
							"samples", len(finalBuf),
							"duration", time.Since(start))

						// LLM cleanup disabled — use raw whisper text directly.
						// LLM is still loaded for EditText support.

						stateMu.Lock()
						if autoDeliver {
							autoDeliver = false
							finalText = text
							state = StateDelivering
							stateMu.Unlock()
							slog.Debug("state", "new", state, "reason", "auto-deliver")
							display.ShowDelivering(text)
							go func() {
								if err := deliver.TypeAndSend(text); err != nil {
									slog.Error("deliver", "error", err)
								}
								stateMu.Lock()
								state = StateIdle
								finalText = ""
								stateMu.Unlock()
								display.ShowIdle()
							}()
						} else {
							finalText = text
							state = StateReady
							stateMu.Unlock()
							display.ShowReady(text)
						}
					}()
				}

			case ptt.EventEsc:
				switch state {
				case StateRecording:
					capture.Stop()
					streamer.Stop()
					state = StateIdle
					finalText = ""
					display.ShowCancelled()
				case StateEditing:
					if editCapStarted {
						capture.Stop()
						streamer.Stop()
					}
					state = StateReady
					display.ShowReady(finalText)
				case StatePendingDeliver:
					if doubleTapTimer != nil {
						doubleTapTimer.Stop()
						doubleTapTimer = nil
					}
					state = StateIdle
					finalText = ""
					display.ShowCancelled()
				case StateCleaning, StateReady:
					state = StateIdle
					finalText = ""
					autoDeliver = false
					display.ShowCancelled()
				}
			}
			stateMu.Unlock()
		}
	}
}
