# sussurro-stream

Streaming speech-to-text with push-to-talk for Linux/Wayland. Hold a keyboard chord to record, see live transcription in a floating overlay, release to clean up with an LLM, then tap to type the result into any focused app.

## How it works

1. **Hold** Ctrl+Shift+Space — a floating overlay appears, recording starts
2. **Speak** — text appears in real-time as whisper re-processes the growing audio buffer every 750ms
3. **Release** the chord — recording stops, LLM cleans up filler words and grammar
4. **Click** into the target app, **tap** Ctrl+Shift+Space — cleaned text is typed via ydotool/wtype
5. **Esc** (Ctrl+Shift+Alt) at any point to cancel

## Architecture

```
evdev listener ──> audio capture (malgo) ──> streaming whisper ──> GTK3 overlay
      │                                           │
      │ [chord release]                           │
      │                                    LLM cleanup (llama.cpp)
      │                                           │
      │ [chord tap]                               │
      └──────────────────────────────────> ydotool/wtype (text inject)
```

### State machine

```
IDLE ──[chord hold]──> RECORDING ──[chord release]──> CLEANING ──[done]──> READY ──[chord tap]──> DELIVERING ──> IDLE
  ^                       |                             |                    |
  └───────[Esc]───────────┘─────────────[Esc]───────────┘────────[Esc]──────┘
```

### Components

| Package | Purpose |
|---------|---------|
| `cmd/sussurro-stream/main.go` | Entry point, state machine, Display interface |
| `internal/ptt/evdev.go` | Raw evdev reader, chord detection, keyboard auto-discovery |
| `internal/audio/capture.go` | 16kHz mono float32 capture via malgo (miniaudio) |
| `internal/asr/engine.go` | whisper.cpp Go bindings wrapper |
| `internal/asr/stream.go` | Periodic re-transcription of growing audio buffer |
| `internal/llm/cleanup.go` | go-llama.cpp text cleanup with anti-hallucination validation |
| `internal/deliver/wtype.go` | Text injection via ydotool (preferred) or wtype (fallback) |
| `internal/window/window.go` | GTK3 + gtk-layer-shell floating overlay (no focus steal) |
| `internal/config/config.go` | YAML config with sensible defaults |
| `internal/logger/` | slog init + stderr suppression for C library output |

## Requirements

- **OS**: Linux with Wayland (tested on Ubuntu 25.10 / GNOME)
- **GPU**: NVIDIA with CUDA (hardcoded, no CPU fallback)
- **Go**: 1.24+

### System packages

```bash
sudo apt install libgtk-3-dev libgtk-layer-shell-dev nvidia-cuda-toolkit wtype
# ydotool is preferred over wtype — install if available
sudo apt install ydotool
```

### User permissions

```bash
# evdev access for push-to-talk
sudo usermod -aG input $USER
# then log out and back in
```

### Models

Download to `~/.sussurro/models/`:

- **ASR**: `ggml-small.bin` (whisper small model)
- **LLM**: `qwen3-sussurro-q4_k_m.gguf` (Qwen 3 quantized for text cleanup)

### Shared third-party libraries

sussurro-stream shares pre-built whisper.cpp (with CUDA + `WSP_GGML_` prefix patch) and go-llama.cpp from the [sussurro](https://github.com/cesp99/sussurro) build:

```bash
ln -s ../sussurro/third_party third_party
```

## Build

```bash
make build    # outputs bin/sussurro-stream
make run      # build + run
make clean    # remove bin/
```

## Usage

```bash
# With config file
./bin/sussurro-stream -config config.yaml

# With defaults (auto-discovers "Gaming KB" keyboard)
./bin/sussurro-stream

# Terminal-only mode (no GTK window, prints to stdout)
./bin/sussurro-stream -no-ui

# Debug logging
./bin/sussurro-stream -debug

# Override evdev device
./bin/sussurro-stream -device /dev/input/event2
```

## Configuration

Copy `config.example.yaml` and edit:

```yaml
ptt:
  device: "auto"              # auto-discover by name, or /dev/input/eventN
  chord: "ctrl+shift+space"
  cancel: "esc"

audio:
  sample_rate: 16000
  max_duration: "60s"

models:
  asr:
    path: "~/.sussurro/models/ggml-small.bin"
    threads: 4
    language: "en"
  llm:
    path: "~/.sussurro/models/qwen3-sussurro-q4_k_m.gguf"
    gpu_layers: 99
    threads: 4
    enabled: true             # false to skip LLM cleanup

streaming:
  interval: "750ms"           # how often to re-run whisper on the audio buffer
```

## How streaming transcription works

Whisper is not a streaming model — it processes complete audio segments. sussurro-stream works around this by re-processing the entire accumulated audio buffer every 750ms. Each pass replaces the previous result, so text may "wobble" as whisper reconsiders with more context. For recordings under 30 seconds (the common case), each pass runs in ~200-500ms on GPU.

For recordings exceeding 30 seconds, the first 30s are finalized as confirmed text and only the remainder is re-processed.

## Credits

Inspired by and adapted from [sussurro](https://github.com/cesp99/sussurro).
