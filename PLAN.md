# sussurro-stream

A streaming speech-to-text tool for Ubuntu/Wayland with real-time transcription display.

## What it does

1. User holds Ctrl+Shift+Space — a floating window appears, recording starts
2. User speaks. Text appears in the window in real-time as they talk
3. User releases the chord. Recording stops. Final LLM cleanup runs
4. Window shows cleaned-up final text. User clicks where they want it
5. User taps Ctrl+Shift+Space — text is typed into the focused app via wtype. Window disappears
6. Or user presses Esc (CapsLock remapped) — window disappears, nothing delivered

## Interaction model

All input is via evdev (global, works regardless of window focus):

| Input | State | Action |
|-------|-------|--------|
| Ctrl+Shift+Space **hold** | idle | Start recording, show window |
| Ctrl+Shift+Space **release** | recording | Stop recording, run LLM cleanup |
| Ctrl+Shift+Space **tap** (<300ms) | has final text | Deliver text via wtype, dismiss window |
| Esc | has final text | Cancel, dismiss window |
| Esc | recording | Cancel recording, dismiss window |

## UX flow

```
[Hold Ctrl+Shift+Space]
  Window appears: |                                              |

[Speaking: "um so I think we should..."]
  Window updates: | I think we should                            |

[Speaking: "...move the deadline to Friday"]
  Window updates: | I think we should move the deadline to Friday|

[Release Ctrl+Shift+Space]
  Brief spinner/indicator while LLM runs
  Window updates: | I think we should move the deadline to Friday. |

[User clicks into Slack message box]
[Taps Ctrl+Shift+Space]
  wtype types "I think we should move the deadline to Friday."
  Window disappears.

  -- or --

[Presses Esc]
  Window disappears. Nothing delivered.
```

## Architecture

```
┌──────────────┐     ┌──────────────┐     ┌──────────────────┐     ┌──────────┐
│ evdev chord  │────>│ Audio capture │────>│ Streaming whisper │────>│ Window   │
│ listener     │     │ (miniaudio)  │     │ (re-process loop) │     │ (GTK)    │
└──────┬───────┘     └──────────────┘     └──────────────────┘     └──────────┘
       │                                                                 │
       │ [on chord release]                                              │
       │──────────────────────────────────────────────>┌─────────────────┤
       │                                               │ LLM cleanup    │
       │                                               │ (go-llama.cpp) │
       │                                               └────────┬───────┘
       │                                                        │
       │ [on chord tap]                                         │
       │──────────────────────────────────────────────>┌────────▼───────┐
       │                                               │ wtype          │
       │                                               │ (text inject)  │
       │                                               └────────────────┘
       │ [on Esc]
       │──────────────────────────────────────────────> dismiss window
```

## State machine

```
         ┌──────────────────────────────────────────┐
         │                                          │
         ▼                                          │
    ┌─────────┐  chord hold   ┌───────────┐        │
    │  IDLE   │──────────────>│ RECORDING │        │
    └─────────┘               └─────┬─────┘        │
         ▲                     chord│release  Esc   │
         │                     ─────▼─────── ───────┘
         │                   ┌─────────────┐
         │          Esc      │ CLEANING UP │ (LLM running)
         │◄──────────────────└──────┬──────┘
         │                          │ done
         │                   ┌──────▼──────┐
         │          Esc      │   READY     │ (final text shown)
         │◄──────────────────│             │
         │                   └──────┬──────┘
         │                    chord │tap
         │                   ┌──────▼──────┐
         └───────────────────│  DELIVERING │ (wtype running)
                             └─────────────┘
```

## Components

### 1. evdev chord listener (`internal/ptt/`)

Reads raw input events from `/dev/input/eventN`. Tracks modifier key state to detect Ctrl+Shift+Space chord and Esc.

Chord detection logic:
- Maintain a set of currently-held keys
- Chord "press" = all chord keys are now held (the last key of the combo was pressed)
- Chord "release" = any chord key released while chord was active
- Tap detection = chord pressed then released within 300ms with no transition to RECORDING (or RECORDING produced no audio)

Implementation: read 24-byte `input_event` structs from the device fd. ~80 lines of Go, no external library needed.

Requires: user in `input` group (`sudo usermod -aG input $USER`)

### 2. Audio capture (`internal/audio/`)

Adapted from sussurro. miniaudio via malgo.
- 16kHz, mono, float32
- Pushes chunks to a channel
- Pipeline accumulates into a growing buffer

### 3. Streaming whisper engine (`internal/asr/`)

Two parts:

**engine.go** — Whisper wrapper (adapted from sussurro)
- Load model, create context, transcribe samples

**stream.go** — Streaming coordinator (new)
- On a timer (every 750ms), snapshot the current audio buffer and run whisper
- If a previous pass is still running, skip (don't queue)
- Send each result to the window for display
- On recording stop, run one final pass on the complete audio

### 4. LLM cleanup (`internal/llm/`)

Adapted from sussurro. go-llama.cpp with Qwen 3 Sussurro.
- Runs once on the final full transcription
- Same anti-hallucination validation
- Config toggle to disable (skip cleanup, use raw whisper output)

### 5. Text delivery (`internal/deliver/`)

Uses `wtype` to simulate typing the text into the focused app.
- `wtype "the transcribed text"` — works in any Wayland app
- No clipboard needed, no paste shortcut guessing
- Works in terminals (kitty, tmux), browsers, Electron apps, everything

Requires: `sudo apt install wtype`

### 6. Floating window (`internal/window/`)

A minimal GTK window that:
- Appears on Ctrl+Shift+Space hold
- Shows streaming text as it updates
- Shows a visual indicator during LLM cleanup
- Disappears on deliver or cancel

Properties:
- Dark background, light text, monospace or clean sans-serif
- No titlebar/chrome (GTK CSD disabled)
- Always on top
- Doesn't steal keyboard focus (important: user isn't typing in this window)
- Positioned at top-center or bottom-center of screen
- Text wraps, window grows vertically with content (up to a max)

GTK3 vs GTK4: GTK3 is more pragmatic — better Wayland layer-shell support via gtk-layer-shell, and sussurro already uses GTK3 so we know the CGO bindings work.

Focus behavior: The window should NOT steal focus. On Wayland with gtk-layer-shell, we can create an overlay layer surface that sits above normal windows without taking focus. This is the correct approach.

### 7. Config (`internal/config/`)

Simple YAML config, no viper overhead:

```yaml
ptt:
  device: "/dev/input/event2"
  chord: "ctrl+shift+space"
  cancel: "esc"

audio:
  sample_rate: 16000
  max_duration: "60s"

models:
  asr:
    path: "~/.sussurro/models/ggml-small.bin"
    threads: 4
  llm:
    path: "~/.sussurro/models/qwen3-sussurro-q4_k_m.gguf"
    gpu_layers: 99
    threads: 4
    enabled: true  # set false to skip LLM cleanup

streaming:
  interval: "750ms"
```

## Streaming transcription: the hard problem

### Why it's hard

Whisper is not a streaming model. It processes complete audio segments (up to 30s). No concept of partial results.

When you feed whisper 5s of audio, it produces text. Feed it 10s starting with those same 5s, the first 5s transcription may change — more context lets whisper reconsider.

### v1 approach: re-process everything

Since the small model on a 2080 Ti is fast:
- Every 750ms, process the entire audio buffer accumulated so far
- Replace the window text with the full new result
- Text will occasionally "wobble" as whisper reconsiders with more context
- For clips under 30s (most PTT recordings), this runs in well under 1s

This is simple and good enough. Optimizations for later:
- Confirmed vs tentative text (show tentative in a different style)
- Cache encoder output for already-processed audio
- Only re-run decoder on new portions

### The 30-second boundary

Whisper processes at most 30s at a time. For recordings >30s:
- Finalize the first 30s as confirmed text
- Continue re-processing from the 30s mark onward
- Append confirmed segments
- Most PTT recordings won't hit this

### Processing cadence

- Timer fires every 750ms
- If previous whisper pass still running, skip this tick
- On PTT release, do one final pass on complete audio (this becomes the text that LLM cleans)

## Challenges and risks

### 1. Window that doesn't steal focus

gtk-layer-shell lets us create overlay surfaces on wlr-based compositors. GNOME uses its own layer-shell support (since GNOME 44+). The window should use the OVERLAY layer and set `keyboard_interactivity` to NONE. This means it sits on top and never takes focus — exactly what we want.

If layer-shell isn't available, fall back to a regular always-on-top window. It may briefly steal focus on show, but since the user is just talking, not typing, this is acceptable.

### 2. evdev key event consumption

When we detect Ctrl+Shift+Space via evdev, the keypress also goes to the focused app. Space will type a space somewhere.

Mitigation: use EVIOCGRAB ioctl to grab exclusive access to the keyboard while our chord is active. But grabbing the whole keyboard blocks all input — bad.

Better mitigation: use a uinput virtual device. Read events from the real keyboard, intercept our chord, forward everything else. This is ~100 lines but gives perfect key suppression.

Simplest mitigation: accept that a space might leak. In practice, Ctrl+Shift+Space doesn't produce visible output in most apps (it's not a standard shortcut in terminals, browsers, etc.). Test and see if it's actually a problem before engineering around it.

### 3. wtype timing

wtype types text character by character. For a 100-character sentence, this is essentially instant. For very long text, there might be a brief visible "typing" animation. Acceptable.

### 4. Whisper re-processing overhead

Re-processing the full buffer every 750ms is redundant work. With the small model on GPU, each pass for <30s audio takes ~200-500ms. So we get ~1-2 updates per second. Adequate for real-time feel.

### 5. LLM cleanup latency

go-llama.cpp with Qwen 3 on GPU should clean a sentence in <1s. The user sees a brief indicator ("cleaning up...") in the window. Acceptable since they're switching focus and positioning cursor during this time anyway.

### 6. evdev device discovery

Hardcoding `/dev/input/event2` is fragile — device numbers can change across reboots. Options:
- Config file specifies the device (current approach)
- Auto-discover by device name (e.g. "Gaming KB")
- List devices and let user pick on first run

Recommendation: auto-discover by name, with config override.

## Project structure

```
sussurro-stream/
  cmd/
    sussurro-stream/
      main.go              # Entry point, state machine, wires components
  internal/
    audio/
      capture.go           # miniaudio capture (adapted from sussurro)
    asr/
      engine.go            # Whisper engine wrapper (adapted from sussurro)
      stream.go            # Streaming coordinator (new)
    llm/
      cleanup.go           # LLM text cleanup (adapted from sussurro)
    ptt/
      evdev.go             # evdev reader + chord detection (new)
    window/
      window.go            # GTK3 + layer-shell floating window (new)
    deliver/
      wtype.go             # Text injection via wtype (new, tiny)
    config/
      config.go            # YAML config loader (new, simple)
  config.example.yaml
  Makefile
  go.mod
  go.sum
  PLAN.md
```

## Target system

- Ubuntu 25.10, Wayland (GNOME)
- RTX 2080 Ti (11GB VRAM), CUDA 13.1
- 16 cores
- Gaming KB on /dev/input/event2 (auto-discover by name)
- Go 1.24

No portability needed. Hardcode CUDA, Linux, Wayland, amd64.

## Dependencies

System packages (already installed from sussurro build):
- `libgtk-3-dev`
- `libgtk-layer-shell-dev`
- `nvidia-cuda-toolkit`
- `wtype` (`sudo apt install wtype`)
- User in `input` group for evdev

Go/C libraries (shared with sussurro build):
- whisper.cpp (patched, with CUDA + WSP_GGML_CUDA=ON)
- go-llama.cpp
- malgo (miniaudio)

## Build

Symlink `third_party/` from the sussurro build to avoid rebuilding whisper.cpp and go-llama.cpp:

```bash
ln -s ../sussurro/third_party third_party
```

Makefile links against the pre-built static libs with CUDA. Same CGO flags as sussurro but simpler — no platform conditionals, just Linux+CUDA.

## Build order

1. **evdev chord + audio capture** — Hold chord, capture audio, release, save WAV. Prove basics work.
2. **Single-shot whisper** — On release, transcribe full buffer, print to stdout.
3. **Streaming whisper** — While holding, re-process every 750ms, print to stdout (terminal, no window yet).
4. **GTK3 window** — Replace stdout with floating overlay window.
5. **LLM cleanup** — On release, clean the final text.
6. **wtype delivery** — Tap chord to type text into focused app.
7. **Polish** — Auto-discover evdev device, config file, error handling.
