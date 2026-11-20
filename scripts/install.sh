#!/bin/sh
# veri official installer for macOS and Linux (POSIX sh).
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/maybk/veri/main/scripts/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/maybk/veri/main/scripts/install.sh | sh -s -- --version v0.1.0
#   curl -fsSL https://raw.githubusercontent.com/maybk/veri/main/scripts/install.sh | sh -s -- --bin-dir "$HOME/bin"
#
# Environment variables:
#   VERI_VERSION         Specific stable release tag/version (e.g. v0.1.0 or 0.1.0)
#   VERI_INSTALL_DIR     Target directory for the binary (default: $HOME/.local/bin)
#   VERI_UPDATE_MIRROR   Optional HTTPS mirror prefix for archive download only
#   VERI_ARCH            Optional architecture override (amd64 | arm64)

set -eu

REPO="maybk/veri"
BINARY="veri"
DEFAULT_GITHUB_BASE="https://github.com/${REPO}"
GITHUB_BASE="${VERI_GITHUB_BASE:-$DEFAULT_GITHUB_BASE}"
GITHUB_BASE="${GITHUB_BASE%/}"

VERSION="${VERI_VERSION:-}"
BIN_DIR="${VERI_INSTALL_DIR:-}"
MIRROR="${VERI_UPDATE_MIRROR:-}"
ARCH_OVERRIDE="${VERI_ARCH:-}"
TMP_DIR=""

say() { printf '%s\n' "$*"; }
info() { printf '%s\n' "$*" >&2; }
die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT INT TERM

usage() {
  cat >&2 <<EOF
veri installer (macOS / Linux)

Usage: install.sh [options]

Options:
  --version <ver>   Install a specific stable release (e.g. v0.1.0; default: latest)
  --bin-dir <dir>   Target installation directory (default: \$HOME/.local/bin)
  --mirror <url>    Optional HTTPS mirror prefix for archive download only
  -h, --help        Show this help message

Environment:
  VERI_VERSION         Same as --version
  VERI_INSTALL_DIR     Same as --bin-dir
  VERI_UPDATE_MIRROR   Same as --mirror
EOF
}

have() { command -v "$1" >/dev/null 2>&1; }

is_official_base() {
  [ "$GITHUB_BASE" = "$DEFAULT_GITHUB_BASE" ]
}

detect_os() {
  os_raw="$(uname -s 2>/dev/null || echo unknown)"
  case "$os_raw" in
    Linux) printf 'linux' ;;
    Darwin) printf 'darwin' ;;
    MINGW* | MSYS* | CYGWIN* | Windows_NT)
      die "Windows detected. Use PowerShell instead:
  irm https://raw.githubusercontent.com/${REPO}/main/scripts/install.ps1 | iex"
      ;;
    *)
      die "unsupported operating system: $os_raw"
      ;;
  esac
}

detect_arch() {
  os="$1"
  if [ -n "$ARCH_OVERRIDE" ]; then
    case "$ARCH_OVERRIDE" in
      amd64 | x86_64) printf 'amd64'; return ;;
      arm64 | aarch64) printf 'arm64'; return ;;
      *) die "unsupported VERI_ARCH override: $ARCH_OVERRIDE (expected amd64 or arm64)" ;;
    esac
  fi

  arch_raw="$(uname -m 2>/dev/null || echo unknown)"
  case "$arch_raw" in
    x86_64 | amd64)
      if [ "$os" = "darwin" ] && have sysctl; then
        translated="$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)"
        if [ "$translated" = "1" ]; then
          printf 'arm64'
          return
        fi
      fi
      printf 'amd64'
      ;;
    arm64 | aarch64)
      printf 'arm64'
      ;;
    *)
      die "unsupported CPU architecture: $arch_raw (supported: amd64, arm64)"
      ;;
  esac
}

validate_mirror() {
  raw="$1"
  if [ -z "$raw" ]; then
    return
  fi
  case "$raw" in
    https://*) ;;
    *) die "invalid mirror prefix: must start with https://" ;;
  esac
  case "$raw" in
    *"@"* | *"?"* | *"#"* | *" "* | *"	"*)
      die "invalid mirror prefix: must not contain credentials, query, fragment, or whitespace"
      ;;
  esac
  MIRROR="${raw%/}/"
}

validate_version() {
  raw="$1"
  trimmed="${raw#v}"
  trimmed="${trimmed#V}"
  if ! printf '%s' "$trimmed" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
    die "invalid release version '$raw' (expected MAJOR.MINOR.PATCH, e.g. v0.1.0)"
  fi
  VERSION="v${trimmed}"
}

download_file() {
  url="$1"
  dest="$2"
  if have curl; then
    if is_official_base; then
      curl --proto '=https' --tlsv1.2 -fsSL --retry 3 --retry-delay 1 -o "$dest" "$url"
    else
      curl -fsSL --retry 2 --retry-delay 1 -o "$dest" "$url"
    fi
  elif have wget; then
    if is_official_base; then
      wget --https-only --secure-protocol=TLSv1_2 -q -O "$dest" "$url"
    else
      wget -q -O "$dest" "$url"
    fi
  else
    die "neither curl nor wget was found; install one and retry"
  fi
}

resolve_latest_version() {
  latest_url="${GITHUB_BASE}/releases/latest"
  effective=""
  if have curl; then
    if is_official_base; then
      effective="$(curl --proto '=https' --tlsv1.2 -fsSL -o /dev/null -w '%{url_effective}' "$latest_url" 2>/dev/null || true)"
    else
      effective="$(curl -fsSL -o /dev/null -w '%{url_effective}' "$latest_url" 2>/dev/null || true)"
    fi
  elif have wget; then
    if is_official_base; then
      effective="$(wget --https-only --secure-protocol=TLSv1_2 --max-redirect=10 --spider -S "$latest_url" 2>&1 | awk 'tolower($1) == "location:" { print $2 }' | tr -d '\r' | tail -n 1 || true)"
    else
      effective="$(wget --max-redirect=10 --spider -S "$latest_url" 2>&1 | awk 'tolower($1) == "location:" { print $2 }' | tr -d '\r' | tail -n 1 || true)"
    fi
  else
    die "neither curl nor wget was found; install one and retry"
  fi

  case "$effective" in
    */releases/tag/*)
      tag="${effective##*/}"
      tag="${tag%%\?*}"
      tag="${tag%%#*}"
      validate_version "$tag"
      ;;
    *)
      die "failed to resolve latest stable release from ${latest_url}; pass --version <ver> explicitly"
      ;;
  esac
}

sha256_of() {
  file="$1"
  if have sha256sum; then
    sha256sum "$file" | awk '{ print tolower($1) }'
  elif have shasum; then
    shasum -a 256 "$file" | awk '{ print tolower($1) }'
  elif have openssl; then
    openssl dgst -sha256 "$file" | awk '{ print tolower($NF) }'
  else
    die "no SHA-256 tool (sha256sum, shasum, or openssl) found; refusing unverified installation"
  fi
}

parse_expected_sha256() {
  sums_file="$1"
  asset="$2"
  awk -v target="$asset" '
    /^[[:space:]]*#/ || NF < 2 { next }
    {
      name = $2
      sub(/^\*/, "", name)
      sub(/^\.\//, "", name)
      if (name == target) {
        print tolower($1)
        exit
      }
    }
  ' "$sums_file"
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      [ "$#" -ge 2 ] || die "--version requires a value"
      VERSION="$2"
      shift 2
      ;;
    --version=*)
      VERSION="${1#*=}"
      shift
      ;;
    --bin-dir)
      [ "$#" -ge 2 ] || die "--bin-dir requires a value"
      [ -n "$2" ] || die "--bin-dir must not be empty"
      BIN_DIR="$2"
      shift 2
      ;;
    --bin-dir=*)
      BIN_DIR="${1#*=}"
      [ -n "$BIN_DIR" ] || die "--bin-dir must not be empty"
      shift
      ;;
    --mirror)
      [ "$#" -ge 2 ] || die "--mirror requires a value"
      MIRROR="$2"
      shift 2
      ;;
    --mirror=*)
      MIRROR="${1#*=}"
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      usage
      die "unknown option: $1"
      ;;
  esac
done

validate_mirror "$MIRROR"

OS="$(detect_os)"
ARCH="$(detect_arch "$OS")"

if [ -n "$VERSION" ]; then
  validate_version "$VERSION"
else
  resolve_latest_version
fi
NUM_VERSION="${VERSION#v}"

ASSET="${BINARY}-${NUM_VERSION}-${OS}-${ARCH}.tar.gz"
OFFICIAL_RELEASE_BASE="${GITHUB_BASE}/releases/download/${VERSION}"
OFFICIAL_ASSET_URL="${OFFICIAL_RELEASE_BASE}/${ASSET}"
OFFICIAL_SUMS_URL="${OFFICIAL_RELEASE_BASE}/SHA256SUMS.txt"

if [ -n "$BIN_DIR" ]; then
  DEST="$BIN_DIR"
else
  [ -n "${HOME:-}" ] || die "HOME is not set; pass --bin-dir <dir>"
  DEST="${HOME}/.local/bin"
fi
if [ "$DEST" != "/" ]; then
  DEST="${DEST%/}"
fi
[ -n "$DEST" ] || die "installation directory must not be empty"

TMP_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t veri-install)"

info "Installing ${BINARY} ${VERSION} (${OS}/${ARCH})..."

# Always download SHA256SUMS.txt directly from the official release endpoint.
download_file "$OFFICIAL_SUMS_URL" "${TMP_DIR}/SHA256SUMS.txt" ||
  die "failed to download SHA256SUMS.txt from ${OFFICIAL_SUMS_URL}"

EXPECTED="$(parse_expected_sha256 "${TMP_DIR}/SHA256SUMS.txt" "$ASSET")"
if ! printf '%s' "$EXPECTED" | grep -Eq '^[0-9a-f]{64}$'; then
  die "${ASSET} was not found with a valid SHA-256 entry in SHA256SUMS.txt for ${VERSION}"
fi

# Download the archive via optional mirror (if configured), falling back to official URL on transport errors.
if [ -n "$MIRROR" ]; then
  MIRROR_ASSET_URL="${MIRROR}${OFFICIAL_ASSET_URL}"
  if ! download_file "$MIRROR_ASSET_URL" "${TMP_DIR}/${ASSET}"; then
    info "Mirror download failed; falling back to official release URL..."
    download_file "$OFFICIAL_ASSET_URL" "${TMP_DIR}/${ASSET}" ||
      die "failed to download ${ASSET} from ${OFFICIAL_ASSET_URL}"
  fi
else
  download_file "$OFFICIAL_ASSET_URL" "${TMP_DIR}/${ASSET}" ||
    die "failed to download ${ASSET} from ${OFFICIAL_ASSET_URL}"
fi

ACTUAL="$(sha256_of "${TMP_DIR}/${ASSET}")"
if [ "$ACTUAL" != "$EXPECTED" ]; then
  die "SHA-256 checksum mismatch for ${ASSET}
  expected: ${EXPECTED}
  actual:   ${ACTUAL}"
fi

mkdir -p "${TMP_DIR}/extract"
tar -xzf "${TMP_DIR}/${ASSET}" -C "${TMP_DIR}/extract" || die "failed to extract ${ASSET}"

BIN_SRC="${TMP_DIR}/extract/${BINARY}"
if [ ! -f "$BIN_SRC" ] || [ -L "$BIN_SRC" ]; then
  die "regular root binary '${BINARY}' not found inside ${ASSET}"
fi

mkdir -p "$DEST" || die "failed to create installation directory: ${DEST}"
[ -w "$DEST" ] || die "installation directory is not writable: ${DEST} (pass --bin-dir <dir>)"

STAGED="${DEST}/.${BINARY}.new.$$"
cp "$BIN_SRC" "$STAGED" || {
  rm -f "$STAGED"
  die "failed to stage binary in ${DEST}"
}
chmod 755 "$STAGED"
mv -f "$STAGED" "${DEST}/${BINARY}" || {
  rm -f "$STAGED"
  die "failed to install binary to ${DEST}/${BINARY}"
}

say "Installed ${BINARY} ${VERSION} to ${DEST}/${BINARY}"

case ":${PATH}:" in
  *":${DEST}:"*) ;;
  *)
    say ""
    say "Add ${DEST} to your PATH:"
    say "  export PATH=\"${DEST}:\$PATH\""
    ;;
esac

say ""
say "Next steps:"
say "  ${BINARY} version"
say "  ${BINARY} update"

if [ -z "${HOME:-}" ] || [ ! -f "${HOME}/.agents/skills/${BINARY}/SKILL.md" ]; then
  say ""
  say "Optional Agent Skill:"
  say "  npx skills add ${REPO}   (offline: ${BINARY} skill install)"
fi
