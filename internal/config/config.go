package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	PTT       PTTConfig       `yaml:"ptt"`
	Audio     AudioConfig     `yaml:"audio"`
	Models    ModelsConfig    `yaml:"models"`
	Streaming StreamingConfig `yaml:"streaming"`
	Debug     bool            `yaml:"debug"`
}

type PTTConfig struct {
	Device string `yaml:"device"`
	Chord  string `yaml:"chord"`
	Cancel string `yaml:"cancel"`
}

type AudioConfig struct {
	SampleRate  int           `yaml:"sample_rate"`
	MaxDuration time.Duration `yaml:"-"`
	RawDuration string        `yaml:"max_duration"`
}

type ModelsConfig struct {
	ASR ASRConfig `yaml:"asr"`
	LLM LLMConfig `yaml:"llm"`
}

type ASRConfig struct {
	Path     string `yaml:"path"`
	Threads  int    `yaml:"threads"`
	Language string `yaml:"language"`
}

type LLMConfig struct {
	Path      string `yaml:"path"`
	GPULayers int    `yaml:"gpu_layers"`
	Threads   int    `yaml:"threads"`
	Enabled   bool   `yaml:"enabled"`
}

type StreamingConfig struct {
	Interval    time.Duration `yaml:"-"`
	RawInterval string        `yaml:"interval"`
}

func Default() *Config {
	return &Config{
		PTT: PTTConfig{
			Device: "auto",
			Chord:  "ctrl+shift+space",
			Cancel: "esc",
		},
		Audio: AudioConfig{
			SampleRate:  16000,
			MaxDuration: 60 * time.Second,
			RawDuration: "60s",
		},
		Models: ModelsConfig{
			ASR: ASRConfig{
				Path:     "~/.sussurro/models/ggml-small.bin",
				Threads:  4,
				Language: "en",
			},
			LLM: LLMConfig{
				Path:      "~/.sussurro/models/qwen3-sussurro-q4_k_m.gguf",
				GPULayers: 99,
				Threads:   4,
				Enabled:   true,
			},
		},
		Streaming: StreamingConfig{
			Interval:    750 * time.Millisecond,
			RawInterval: "750ms",
		},
	}
}

func Load(path string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.resolve()
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Audio.RawDuration != "" {
		d, err := time.ParseDuration(cfg.Audio.RawDuration)
		if err != nil {
			return nil, fmt.Errorf("parse audio.max_duration: %w", err)
		}
		cfg.Audio.MaxDuration = d
	}

	if cfg.Streaming.RawInterval != "" {
		d, err := time.ParseDuration(cfg.Streaming.RawInterval)
		if err != nil {
			return nil, fmt.Errorf("parse streaming.interval: %w", err)
		}
		cfg.Streaming.Interval = d
	}

	cfg.resolve()
	return cfg, nil
}

func (c *Config) resolve() {
	c.Models.ASR.Path = expandHome(c.Models.ASR.Path)
	c.Models.LLM.Path = expandHome(c.Models.LLM.Path)
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
