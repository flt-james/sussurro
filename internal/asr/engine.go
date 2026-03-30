package asr

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/jms301/sussurro-stream/internal/logger"
	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
)

// Engine wraps whisper.cpp for speech-to-text.
type Engine struct {
	model   whisper.Model
	context whisper.Context
	mutex   sync.Mutex
	debug   bool
}

// NewEngine loads a whisper model and creates a processing context.
func NewEngine(modelPath string, threads int, language string, debug bool) (*Engine, error) {
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("model file not found: %s", modelPath)
	}

	if !debug {
		cleanup := logger.SuppressStderr()
		defer cleanup()
	}

	model, err := whisper.New(modelPath)
	if err != nil {
		return nil, fmt.Errorf("load whisper model: %w", err)
	}

	ctx, err := model.NewContext()
	if err != nil {
		return nil, fmt.Errorf("create whisper context: %w", err)
	}

	if language != "" {
		if err := ctx.SetLanguage(language); err != nil {
			slog.Warn("whisper: could not set language", "language", language, "error", err)
		}
	}

	return &Engine{
		model:   model,
		context: ctx,
		debug:   debug,
	}, nil
}

// Transcribe processes audio samples and returns the transcribed text.
func (e *Engine) Transcribe(samples []float32) (string, error) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if len(samples) == 0 {
		return "", nil
	}

	if !e.debug {
		cleanup := logger.SuppressStderr()
		defer cleanup()
	}

	if err := e.context.Process(samples, nil, nil, nil); err != nil {
		return "", fmt.Errorf("transcription failed: %w", err)
	}

	var parts []string
	for {
		segment, err := e.context.NextSegment()
		if err != nil {
			break
		}
		if t := strings.TrimSpace(segment.Text); t != "" {
			parts = append(parts, t)
		}
	}

	return strings.TrimSpace(strings.Join(parts, " ")), nil
}

// Close releases model resources.
func (e *Engine) Close() {
	if e.model != nil {
		e.model.Close()
	}
}
