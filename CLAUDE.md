# sussurro-stream — AI Development Context

## What this is

A streaming push-to-talk speech-to-text tool for Linux/Wayland. Hold a chord, speak, release, then tap to type the cleaned text into any app. Written in Go with CGO bindings to whisper.cpp and go-llama.cpp for inference, GTK3 for the overlay window, and raw evdev for keyboard input.

## Tech stack

- **Language**: Go 1.24
- **ASR**: whisper.cpp (C++ via CGO, patched with `WSP_GGML_` prefix for symbol deconfliction)
- **LLM**: go-llama.cpp (C++ via CGO, Qwen 3 model for text cleanup)
- **Audio**: malgo (Go bindings for miniaudio)
- **UI**: GTK3 + gtk-layer-shell (CGO, inline C in `window.go`)
- **Input**: Raw Linux evdev (direct fd reads, no library)
- **Text delivery**: ydotool (preferred) or wtype (fallback) via `exec.Command`
- **Config**: gopkg.in/yaml.v3
- **Platform**: Linux/Wayland/CUDA only — no portability targets

## Build

```bash
# Requires symlinked third_party/ from the sussurro build
ln -s ../sussurro/third_party third_party

make build    # → bin/sussurro-stream
make run      # build + run
make clean
```

The Makefile handles CGO flags for whisper.cpp, go-llama.cpp, GTK3, gtk-layer-shell, and CUDA. It auto-detects gtk-layer-shell availability and sets `-DHAVE_GTK_LAYER_SHELL` accordingly.

## Project structure

```
cmd/sussurro-stream/main.go     State machine + Display interface + CLI flags
internal/
  ptt/evdev.go                  evdev chord detection + keyboard auto-discovery
  audio/capture.go              16kHz mono float32 audio capture
  asr/engine.go                 whisper.cpp wrapper (model load, transcribe)
  asr/stream.go                 Periodic re-transcription coordinator
  llm/cleanup.go                LLM text cleanup + anti-hallucination validation
  deliver/wtype.go              Text injection via ydotool/wtype
  window/window.go              GTK3 overlay (inline C, layer-shell, thread-safe updates)
  config/config.go              YAML config with defaults + home dir expansion
  logger/logger.go              slog init
  logger/suppress.go            Redirect stderr to silence C library output
```

## Key design decisions

1. **Single-binary state machine**: All orchestration lives in `main.go`. The state machine (IDLE → RECORDING → CLEANING → READY → DELIVERING) runs in a goroutine that reads from the evdev event channel.

2. **Display interface**: `main.go` defines a `Display` interface with `ShowRecording()`, `UpdateText()`, `ShowReady()`, etc. Two implementations: `windowDisplay` (GTK overlay) and `terminalDisplay` (stdout). The `-no-ui` flag selects terminal mode.

3. **GTK on main thread**: GTK requires the main thread. In window mode, the state machine runs in a goroutine while `overlay.Run()` blocks the main goroutine. All GTK updates go through `g_idle_add` for thread safety.

4. **Streaming by re-processing**: The `Streamer` snapshots the full audio buffer on a timer and runs whisper on it. If the previous pass is still running (whisper mutex), the tick is effectively skipped. This is simple but good enough for <30s recordings on GPU.

5. **Anti-hallucination validation**: The LLM cleanup in `cleanup.go` validates output against the raw transcription — rejects if >30% of significant words weren't in the original, if the output is >2x the input length, or if it starts with meta-commentary prefixes.

6. **Chord detection**: Tracks held keys via a map. Chord press = all of Ctrl+Shift+Space held. Chord release = any chord key released while chord was active. Cancel = Ctrl+Shift+Alt (without Space). No tap detection in current implementation — delivery happens on chord release in READY state.

7. **Delivery timing**: A 100ms sleep before ydotool/wtype typing ensures physical modifier keys are fully released, preventing accidental shortcut triggers.

8. **Stderr suppression**: whisper.cpp and llama.cpp print verbose output to stderr. `SuppressStderr()` uses `syscall.Dup2` to redirect stderr to `/dev/null` during model loading and inference, restored via cleanup function. Disabled in debug mode.

## Conventions

- All packages are under `internal/` — no exported API
- Structured logging via `log/slog`
- No external config library (just `gopkg.in/yaml.v3` directly)
- Error messages: lowercase, no trailing punctuation, wrapped with `%w`
- Config paths support `~/` expansion
- CGO inline C is used in `window.go` rather than separate C files

## Dependencies on external state

- `third_party/` must be symlinked to a built sussurro checkout (whisper.cpp + go-llama.cpp with CUDA)
- Models at `~/.sussurro/models/` (configurable)
- User must be in `input` group for evdev access
- ydotool or wtype must be installed for text delivery
- GTK3 and optionally gtk-layer-shell dev packages
- NVIDIA GPU with CUDA toolkit
