package asr

import (
	"log/slog"
	"sync"
	"time"
)

// Streamer coordinates periodic whisper re-processing of a growing audio buffer.
type Streamer struct {
	engine   *Engine
	interval time.Duration

	mu      sync.Mutex
	buffer  []float32 // accumulated audio samples
	running bool

	stop    chan struct{}
	wg      sync.WaitGroup
	onText  func(string) // called with each transcription update
}

// NewStreamer creates a streaming coordinator that re-processes audio on a timer.
func NewStreamer(engine *Engine, interval time.Duration, onText func(string)) *Streamer {
	return &Streamer{
		engine:   engine,
		interval: interval,
		onText:   onText,
		stop:     make(chan struct{}),
	}
}

// Start begins the periodic transcription loop.
func (s *Streamer) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.buffer = s.buffer[:0]
	s.mu.Unlock()

	s.wg.Add(1)
	go s.loop()
}

// AppendAudio adds audio samples to the buffer.
func (s *Streamer) AppendAudio(samples []float32) {
	s.mu.Lock()
	s.buffer = append(s.buffer, samples...)
	s.mu.Unlock()
}

// Stop halts the streaming loop. Returns the final audio buffer for a last transcription pass.
func (s *Streamer) Stop() []float32 {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.mu.Unlock()

	close(s.stop)
	s.wg.Wait()

	s.mu.Lock()
	buf := make([]float32, len(s.buffer))
	copy(buf, s.buffer)
	s.mu.Unlock()

	return buf
}

// Reset prepares for a new recording session.
func (s *Streamer) Reset() {
	s.mu.Lock()
	s.buffer = s.buffer[:0]
	s.mu.Unlock()
	s.stop = make(chan struct{})
}

func (s *Streamer) loop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.transcribeSnapshot()
		}
	}
}

func (s *Streamer) transcribeSnapshot() {
	s.mu.Lock()
	if len(s.buffer) == 0 {
		s.mu.Unlock()
		return
	}
	snapshot := make([]float32, len(s.buffer))
	copy(snapshot, s.buffer)
	s.mu.Unlock()

	text, err := s.engine.Transcribe(snapshot)
	if err != nil {
		slog.Error("streaming transcription error", "error", err)
		return
	}

	if text != "" && s.onText != nil {
		s.onText(text)
	}
}
