#!/usr/bin/env bash
# sussurro-transcribe installer
# Usage: curl -fsSL https://raw.githubusercontent.com/cesp99/sussurro/master/scripts/install-transcribe.sh | bash
set -euo pipefail

REPO="cesp99/sussurro"
BINARY="sussurro-transcribe"
INSTALL_DIR=""   # resolved below

# ── colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

info()    { printf "${CYAN}  →${RESET} %s\n" "$*"; }
success() { printf "${GREEN}  ✓${RESET} %s\n" "$*"; }
warn()    { printf "${YELLOW}  ⚠${RESET} %s\n" "$*"; }
die()     { printf "${RED}  ✗${RESET} %s\n" "$*" >&2; exit 1; }
header()  { printf "\n${BOLD}%s${RESET}\n" "$*"; }

# ── detect OS & arch ─────────────────────────────────────────────────────────
detect_platform() {
    local os arch

    case "$(uname -s)" in
        Darwin) os="macos" ;;
        Linux)  os="linux" ;;
        *)      die "Unsupported OS: $(uname -s). Only macOS and Linux are supported." ;;
    esac

    case "$(uname -m)" in
        arm64|aarch64) arch="arm64" ;;
        x86_64|amd64)  arch="amd64" ;;
        *)             die "Unsupported architecture: $(uname -m)." ;;
    esac

    echo "${os}-${arch}"
}

# ── check for ffmpeg ──────────────────────────────────────────────────────────
check_ffmpeg() {
    if command -v ffmpeg &>/dev/null; then
        success "ffmpeg found: $(ffmpeg -version 2>&1 | head -1 | cut -d' ' -f1-3)"
    else
        warn "ffmpeg not found — sussurro-transcribe requires it to decode audio files."
        printf "\n  Install ffmpeg:\n"
        case "$(uname -s)" in
            Linux)
                printf "    Arch/Manjaro:   sudo pacman -S ffmpeg\n"
                printf "    Ubuntu/Debian:  sudo apt install ffmpeg\n"
                printf "    Fedora:         sudo dnf install ffmpeg\n"
                ;;
            Darwin)
                printf "    macOS:          brew install ffmpeg\n"
                ;;
        esac
        printf "\n"
        warn "Continuing install — please install ffmpeg before using sussurro-transcribe."
    fi
}

# ── pick install dir ──────────────────────────────────────────────────────────
pick_install_dir() {
    if [ -w "/usr/local/bin" ] || sudo -n true 2>/dev/null; then
        echo "/usr/local/bin"
    else
        local local_bin="$HOME/.local/bin"
        mkdir -p "$local_bin"
        echo "$local_bin"
    fi
}

# ── ensure PATH contains the install dir ─────────────────────────────────────
ensure_in_path() {
    local dir="$1"
    if [[ ":$PATH:" != *":$dir:"* ]]; then
        warn "$dir is not in your PATH."
        local shell_rc=""
        case "$SHELL" in
            */zsh)  shell_rc="$HOME/.zshrc"  ;;
            */bash) shell_rc="$HOME/.bashrc" ;;
            *)      shell_rc="$HOME/.profile" ;;
        esac
        printf '\n# Sussurro companion tools\nexport PATH="%s:$PATH"\n' "$dir" >> "$shell_rc"
        info "Added $dir to PATH in $shell_rc"
        warn "Run: source $shell_rc  (or open a new terminal) before using sussurro-transcribe"
    fi
}

# ── resolve latest version from GitHub ───────────────────────────────────────
fetch_latest_version() {
    local tag
    if command -v curl &>/dev/null; then
        tag=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
              | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
    elif command -v wget &>/dev/null; then
        tag=$(wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" \
              | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
    else
        die "Neither curl nor wget found. Please install one and retry."
    fi
    [ -n "$tag" ] || die "Could not determine latest release. Check your internet connection."
    echo "$tag"
}

# ── download helper ───────────────────────────────────────────────────────────
download() {
    local url="$1" dest="$2"
    if command -v curl &>/dev/null; then
        curl -fsSL --progress-bar "$url" -o "$dest"
    else
        wget -q --show-progress "$url" -O "$dest"
    fi
}

# ── check whether Sussurro config exists ─────────────────────────────────────
check_sussurro_config() {
    local cfg="$HOME/.sussurro/config.yaml"
    if [ -f "$cfg" ]; then
        success "Sussurro config found: $cfg"
    else
        warn "~/.sussurro/config.yaml not found."
        warn "sussurro-transcribe shares models with the main Sussurro app."
        warn "Run 'sussurro' at least once to download models and generate config,"
        warn "or point to a custom config with:  sussurro-transcribe -config <path>"
    fi
}

# ── main ──────────────────────────────────────────────────────────────────────
main() {
    header "sussurro-transcribe installer"

    # 1. Platform
    local platform
    platform=$(detect_platform)
    info "Detected platform: ${platform}"

    # 2. Check runtime dependencies
    check_ffmpeg

    # 3. Latest version
    info "Fetching latest release..."
    local version
    version=$(fetch_latest_version)
    info "Latest version: ${version}"

    # 4. Build download URL
    #    The dedicated transcribe archive: sussurro-transcribe-linux-amd64.tar.gz
    local archive_base="sussurro-transcribe-${platform}"
    local archive_name="${archive_base}.tar.gz"
    local download_url="https://github.com/${REPO}/releases/download/${version}/${archive_name}"

    # 5. Download to a temp dir
    local tmpdir
    tmpdir=$(mktemp -d)
    trap 'rm -rf "$tmpdir"' EXIT

    info "Downloading ${archive_name}..."
    download "$download_url" "${tmpdir}/${archive_name}" \
        || die "Download failed. Make sure a release for '${platform}' exists at:\n  ${download_url}"

    # 6. Verify download
    local sz
    sz=$(wc -c < "${tmpdir}/${archive_name}")
    [ "$sz" -gt 1024 ] || die "Downloaded file looks corrupt (only ${sz} bytes)."

    # 7. Extract
    info "Extracting..."
    tar -xzf "${tmpdir}/${archive_name}" -C "$tmpdir"

    # Binary lives inside: sussurro-linux-amd64/sussurro-transcribe
    local extracted_binary="${tmpdir}/${archive_base}/${BINARY}"
    [ -f "$extracted_binary" ] \
        || die "Binary not found in archive. Expected: ${archive_base}/${BINARY}\nThe release may not include the transcribe companion yet."

    # 8. Install
    INSTALL_DIR=$(pick_install_dir)
    local dest="${INSTALL_DIR}/${BINARY}"

    info "Installing to ${dest}..."
    if [ "$INSTALL_DIR" = "/usr/local/bin" ] && [ ! -w "/usr/local/bin" ]; then
        sudo install -m 755 "$extracted_binary" "$dest"
    else
        install -m 755 "$extracted_binary" "$dest"
    fi

    # 9. macOS: strip quarantine
    if [[ "$platform" == macos-* ]]; then
        info "Removing macOS quarantine flag..."
        xattr -d com.apple.quarantine "$dest" 2>/dev/null || true
    fi

    # 10. PATH check
    ensure_in_path "$INSTALL_DIR"

    # 11. Sussurro config check
    check_sussurro_config

    # 12. Done!
    success "sussurro-transcribe ${version} installed successfully!"
    printf "\n${BOLD}Usage${RESET}\n"
    printf "  Basic:      ${CYAN}sussurro-transcribe -i audio.mp3${RESET}\n"
    printf "  With LLM:   ${CYAN}sussurro-transcribe -i audio.wav -clean${RESET}\n"
    printf "  To file:    ${CYAN}sussurro-transcribe -i audio.mp3 -o out.txt${RESET}\n"
    printf "\n  Full docs:  https://github.com/cesp99/sussurro/blob/master/docs/transcribe.md\n\n"
}

main "$@"
