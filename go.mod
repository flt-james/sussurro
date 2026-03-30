module github.com/jms301/sussurro-stream

go 1.24.0

require (
	github.com/AshkanYarmoradi/go-llama.cpp v0.0.0-20240314183750-6a8041ef6b46
	github.com/gen2brain/malgo v0.11.24
	github.com/ggerganov/whisper.cpp/bindings/go v0.0.0-20260209103306-764482c3175d
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/ggerganov/whisper.cpp/bindings/go => ./third_party/whisper.cpp/bindings/go

replace github.com/AshkanYarmoradi/go-llama.cpp => ./third_party/go-llama.cpp
