package logger

import (
	"os"
	"syscall"
)

// SuppressStderr redirects stderr to /dev/null and returns a cleanup function.
// Used to silence C library output from whisper.cpp and llama.cpp.
func SuppressStderr() func() {
	originalStderr, err := syscall.Dup(int(os.Stderr.Fd()))
	if err != nil {
		return func() {}
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0666)
	if err != nil {
		syscall.Close(originalStderr)
		return func() {}
	}

	err = syscall.Dup2(int(devNull.Fd()), int(os.Stderr.Fd()))
	if err != nil {
		syscall.Close(originalStderr)
		devNull.Close()
		return func() {}
	}

	return func() {
		syscall.Dup2(originalStderr, int(os.Stderr.Fd()))
		syscall.Close(originalStderr)
		devNull.Close()
	}
}
