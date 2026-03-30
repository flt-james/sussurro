APP_NAME := sussurro-stream
BUILD_DIR := bin
CMD_DIR := cmd/sussurro-stream

# Whisper.cpp configuration
WHISPER_DIR := third_party/whisper.cpp
WHISPER_INCLUDE := $(abspath $(WHISPER_DIR)/include)
WHISPER_GGML_INCLUDE := $(abspath $(WHISPER_DIR)/ggml/include)
C_INCLUDE_PATH := $(WHISPER_INCLUDE):$(WHISPER_GGML_INCLUDE)
LIBRARY_PATH := $(abspath $(WHISPER_DIR))

# go-llama.cpp configuration
LLAMA_DIR := third_party/go-llama.cpp

# GTK3 + layer-shell
# pkg-config name is gtk-layer-shell-0 on Ubuntu
HAS_LAYER_SHELL := $(shell pkg-config --exists gtk-layer-shell-0 2>/dev/null && echo yes || echo no)

GTK_CFLAGS  := $(shell pkg-config --cflags gtk+-3.0 2>/dev/null)
GTK_LDFLAGS := $(shell pkg-config --libs   gtk+-3.0 2>/dev/null)

ifeq ($(HAS_LAYER_SHELL),yes)
GTK_CFLAGS  += $(shell pkg-config --cflags gtk-layer-shell-0 2>/dev/null) -DHAVE_GTK_LAYER_SHELL
GTK_LDFLAGS += $(shell pkg-config --libs   gtk-layer-shell-0 2>/dev/null)
endif

# Base CGO link flags (whisper + llama + CUDA)
BASE_LDFLAGS := -Wl,--allow-multiple-definition \
	-L$(WHISPER_DIR)/build/src -L$(WHISPER_DIR)/build/ggml/src \
	-L$(WHISPER_DIR)/build/ggml/src/ggml-cpu \
	-L$(WHISPER_DIR)/build/ggml/src/ggml-cuda \
	-L$(WHISPER_DIR)/build/ggml/src/ggml-blas \
	-lwhisper -lggml-cuda -lcuda -lcudart -lcublas

export C_INCLUDE_PATH
export LIBRARY_PATH

.PHONY: all build run clean

all: build

build:
	@echo "Building $(APP_NAME)..."
	@echo "  Layer shell: $(HAS_LAYER_SHELL)"
	@mkdir -p $(BUILD_DIR)
	CGO_CFLAGS="$(GTK_CFLAGS)" \
	CGO_LDFLAGS="$(BASE_LDFLAGS) $(GTK_LDFLAGS)" \
	go build -o $(BUILD_DIR)/$(APP_NAME) ./$(CMD_DIR)

run: build
	@echo "Running $(APP_NAME)..."
	@./$(BUILD_DIR)/$(APP_NAME)

clean:
	@rm -rf $(BUILD_DIR)
