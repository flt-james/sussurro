package audio

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/gen2brain/malgo"
)

var byteBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 0, 4096)
		return &buf
	},
}

// CaptureEngine handles audio recording using malgo (miniaudio).
type CaptureEngine struct {
	ctx          *malgo.AllocatedContext
	device       *malgo.Device
	sampleRate   int
	channels     int
	isRecording  bool
	mutex        sync.Mutex
	dataCallback func([]byte)
}

// NewCaptureEngine creates a new engine. Whisper expects 16kHz mono float32.
func NewCaptureEngine(sampleRate int) (*CaptureEngine, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("init audio context: %w", err)
	}

	return &CaptureEngine{
		ctx:        ctx,
		sampleRate: sampleRate,
		channels:   1,
	}, nil
}

// StartRecording starts capturing audio and sends float32 chunks to the channel.
func (e *CaptureEngine) StartRecording(dataChan chan<- []float32) error {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if e.isRecording {
		return nil
	}

	onData := func(data []byte) {
		numSamples := len(data) / 4
		floats := make([]float32, numSamples)
		if numSamples > 0 {
			src := unsafe.Slice((*float32)(unsafe.Pointer(&data[0])), numSamples)
			copy(floats, src)
		}

		select {
		case dataChan <- floats:
		default:
		}
	}

	return e.startDevice(onData)
}

func (e *CaptureEngine) startDevice(onData func([]byte)) error {
	e.dataCallback = onData

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatF32
	deviceConfig.Capture.Channels = uint32(e.channels)
	deviceConfig.SampleRate = uint32(e.sampleRate)
	deviceConfig.Alsa.NoMMap = 1

	var err error
	e.device, err = malgo.InitDevice(e.ctx.Context, deviceConfig, malgo.DeviceCallbacks{
		Data: func(pOutputSample, pInputSamples []byte, framecount uint32) {
			if e.dataCallback == nil || len(pInputSamples) == 0 {
				return
			}

			rawPtr := byteBufPool.Get().(*[]byte)
			raw := *rawPtr
			if cap(raw) < len(pInputSamples) {
				raw = make([]byte, len(pInputSamples))
			} else {
				raw = raw[:len(pInputSamples)]
			}
			copy(raw, pInputSamples)
			*rawPtr = raw

			e.dataCallback(raw)

			byteBufPool.Put(rawPtr)
		},
	})
	if err != nil {
		return fmt.Errorf("init device: %w", err)
	}

	if err := e.device.Start(); err != nil {
		return fmt.Errorf("start device: %w", err)
	}

	e.isRecording = true
	return nil
}

// Stop halts the audio stream.
func (e *CaptureEngine) Stop() {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if !e.isRecording {
		return
	}

	if e.device != nil {
		e.device.Uninit()
		e.device = nil
	}
	e.isRecording = false
}

// Close releases all resources.
func (e *CaptureEngine) Close() {
	e.Stop()
	if e.ctx != nil {
		e.ctx.Free()
		e.ctx = nil
	}
}
