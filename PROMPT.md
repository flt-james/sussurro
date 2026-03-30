# sussurro-stream — Build Prompt

Read PLAN.md first. It has the full architecture, state machine, UX flow, and build order.

## Context

This project is a streaming speech-to-text tool for Ubuntu 25.10 / Wayland / GNOME. It's inspired by https://github.com/cesp99/sussurro which is already built and installed at `../sussurro/`. That codebase has working audio capture, whisper.cpp integration, LLM cleanup, and build system — adapt from it, don't rewrite from scratch.

## Target hardware

- Ubuntu 25.10, Wayland, GNOME
- RTX 2080 Ti (11GB VRAM), CUDA 13.1, driver 590.48
- 16 CPU cores
- Gaming KB keyboard (auto-discover by name via evdev, typically /dev/input/event2)
- Go 1.24

## Key design decisions already made

1. **One chord**: Ctrl+Shift+Space. Hold = record. Release = stop + LLM cleanup. Tap (when text is ready) = deliver via wtype. Esc = cancel.
2. **Streaming**: While recording, re-process entire audio buffer through whisper every ~750ms. Display updated text in floating window.
3. **Text delivery**: `wtype` types text directly into focused app. No clipboard, no paste shortcut guessing.
4. **Window**: GTK3 + gtk-layer-shell overlay. No focus steal, no titlebar. Dark, minimal.
5. **evdev for input**: Raw evdev reader for chord detection. User must be in `input` group.
6. **CUDA**: Hardcoded. No CPU fallback needed.
7. **Shared third_party/**: Symlink to `../sussurro/third_party/` for pre-built whisper.cpp (with CUDA + WSP_GGML_ prefix patch) and go-llama.cpp.

## Build order

Follow this sequence. Each step should be testable on its own:

1. **evdev chord + audio capture** — Hold chord, capture audio to buffer, release. Save as WAV or print duration. Proves PTT and audio work.
2. **Single-shot whisper** — On release, transcribe full buffer, print to stdout.
3. **Streaming whisper** — While holding, re-process audio every 750ms, print updated text to stdout.
4. **GTK3 overlay window** — Replace stdout with floating layer-shell window showing streaming text.
5. **LLM cleanup** — On release, clean final text, show in window.
6. **wtype delivery** — Tap chord to deliver, Esc to cancel.
7. **Polish** — Auto-discover keyboard by name, config file, error handling.

## Reference files in sussurro

Adapt these (don't copy blindly, simplify for our single-platform target):

- `internal/audio/capture.go` — miniaudio capture via malgo
- `internal/asr/whisper.go` — whisper.cpp Go bindings wrapper
- `internal/llm/llama.go` — go-llama.cpp wrapper + anti-hallucination validation
- `internal/config/config.go` — config structure (simplify heavily)
- `Makefile` — CGO link flags for whisper + llama + CUDA (our version already has the fixes: `WSP_GGML_CUDA=ON`, `-Wl,--allow-multiple-definition`, `-lggml-cuda -lcuda -lcudart -lcublas`, and `.cuh` in the patch script)

## Sussurro build patches we already applied

The sussurro build needed these fixes (already applied in `../sussurro/`):

- `scripts/patch-whisper.sh`: Added `*.cuh` to the file pattern so CUDA headers get the WSP_ prefix rename
- `Makefile`: `GGML_NATIVE=OFF` → `WSP_GGML_NATIVE=OFF`, added `WSP_GGML_CUDA=ON`
- `Makefile`: Added `-Wl,--allow-multiple-definition` to BASE_LDFLAGS (ggml symbol collision between whisper.cpp and go-llama.cpp)
- `Makefile`: Added `-L$(WHISPER_DIR)/build/ggml/src/ggml-cuda` to library search path
- `third_party/whisper.cpp/bindings/go/whisper.go`: Added `-lggml-cuda -lcuda -lcudart -lcublas` to cgo LDFLAGS

## Models (already downloaded)

- ASR: `~/.sussurro/models/ggml-small.bin` (user prefers speed over accuracy)
- LLM: `~/.sussurro/models/qwen3-sussurro-q4_k_m.gguf`

## Config defaults

```yaml
ptt:
  device: "auto"  # auto-discover "Gaming KB" by name
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
    enabled: true
streaming:
  interval: "750ms"
```
