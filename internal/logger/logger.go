package logger

import (
	"log/slog"
	"os"
	"syscall"
)

// logWriter is a dedicated fd for slog output, duplicated from stderr before
// C libraries suppress fd 2. This ensures Go log output still reaches the
// journal/terminal even when stderr is redirected to /dev/null.
var logWriter *os.File

func Init(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}

	// Dup stderr now so slog keeps a working fd after SuppressStderr dup2's fd 2.
	fd, err := syscall.Dup(int(os.Stderr.Fd()))
	if err == nil {
		logWriter = os.NewFile(uintptr(fd), "slog")
	} else {
		logWriter = os.Stderr
	}

	handler := slog.NewTextHandler(logWriter, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}
