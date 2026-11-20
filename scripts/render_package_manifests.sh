#!/usr/bin/env bash
set -euo pipefail

repo="maybk/veri"
positional=()

while [ "$#" -gt 0 ]; do
  case "$1" in
    --repo)
      repo="$2"
      shift 2
      ;;
    -h|--help)
      echo "Usage: $0 <version> <sha256sums-file> [output-dir] [release-date] [--repo owner/name]"
      exit 0
      ;;
    *)
      positional+=("$1")
      shift
      ;;
  esac
done

if [ "${#positional[@]}" -lt 2 ] || [ "${#positional[@]}" -gt 4 ]; then
  echo "Usage: $0 <version> <sha256sums-file> [output-dir] [release-date] [--repo owner/name]" >&2
  exit 1
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version="${positional[0]#v}"
sums_file="${positional[1]}"
out_dir="${positional[2]:-$repo_root/dist/manifests}"
release_date="${positional[3]:-}"

if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Version must match MAJOR.MINOR.PATCH: $version" >&2
  exit 1
fi

if [ -n "$release_date" ] && [[ ! "$release_date" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
  echo "Release date must match YYYY-MM-DD: $release_date" >&2
  exit 1
fi

if [ ! -f "$sums_file" ]; then
  echo "SHA256SUMS file does not exist: $sums_file" >&2
  exit 1
fi

extract_info_description() {
  local config_file="$1"
  local in_info=0
  local line trimmed val
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%$'\r'}"
    case "$line" in
      ""|\#*) continue ;;
      info:*) in_info=1; continue ;;
      " "*|$'\t'*) ;;
      *) in_info=0; continue ;;
    esac
    if [ "$in_info" = 1 ]; then
      trimmed="${line#"${line%%[![:space:]]*}"}"
      case "$trimmed" in
        description:*)
          val="${trimmed#description:}"
          val="${val#"${val%%[![:space:]]*}"}"
          val="${val%"${val##*[![:space:]]}"}"
          case "$val" in
            \"*\"*)
              val="${val#\"}"
              val="${val%%\"*}"
              ;;
            \'*\'*)
              val="${val#\'}"
              val="${val%%\'*}"
              ;;
            *)
              val="${val%%#*}"
              val="${val%"${val##*[![:space:]]}"}"
              ;;
          esac
          printf '%s' "$val"
          return 0
          ;;
      esac
    fi
  done < "$config_file"
  return 1
}

config_file="$repo_root/build/config.yml"
if [ ! -f "$config_file" ]; then
  echo "App metadata config does not exist: $config_file" >&2
  exit 1
fi

desc_en="$(extract_info_description "$config_file" || true)"
if [ -z "$desc_en" ]; then
  echo "Missing info.description in $config_file" >&2
  exit 1
fi
if [ "${#desc_en}" -gt 80 ]; then
  echo "info.description exceeds Homebrew 80-char limit (${#desc_en}): $desc_en" >&2
  exit 1
fi
case "$desc_en" in
  *.)
    echo "info.description must not end with a period: $desc_en" >&2
    exit 1
    ;;
  *\"*|*\\*)
    echo "info.description must not contain double quotes or backslashes: $desc_en" >&2
    exit 1
    ;;
  *": "*|*" #"*)
    echo "info.description must be a plain YAML-safe scalar without ': ' or ' #': $desc_en" >&2
    exit 1
    ;;
esac

desc_zh="面向 AI Agent 与开发者的多源联网检索与网页 Markdown 提取工具（CLI、MCP Server 与 Agent Skill）"

extract_sum() {
  local asset="$1"
  local sum
  sum="$(
    awk -v target="$asset" '
      NF >= 2 {
        file = $2
        sub(/^\*/, "", file)
        if (file == target) {
          print tolower($1)
          exit
        }
      }
    ' "$sums_file"
  )"
  if [[ ! "$sum" =~ ^[0-9a-f]{64}$ ]]; then
    echo "Missing or invalid SHA-256 for $asset in $sums_file" >&2
    exit 1
  fi
  printf '%s' "$sum"
}

darwin_arm64_sha="$(extract_sum "veri-${version}-darwin-arm64.tar.gz")"
darwin_amd64_sha="$(extract_sum "veri-${version}-darwin-amd64.tar.gz")"
linux_arm64_sha="$(extract_sum "veri-${version}-linux-arm64.tar.gz")"
linux_amd64_sha="$(extract_sum "veri-${version}-linux-amd64.tar.gz")"
windows_amd64_sha="$(extract_sum "veri-${version}-windows-amd64.zip")"
windows_arm64_sha="$(extract_sum "veri-${version}-windows-arm64.zip")"

mkdir -p "$out_dir/Formula" "$out_dir/bucket"

cat > "$out_dir/Formula/veri.rb" <<EOF
class Veri < Formula
  desc "${desc_en}"
  homepage "https://github.com/${repo}"
  version "${version}"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/${repo}/releases/download/v#{version}/veri-#{version}-darwin-arm64.tar.gz"
      sha256 "${darwin_arm64_sha}"
    end

    on_intel do
      url "https://github.com/${repo}/releases/download/v#{version}/veri-#{version}-darwin-amd64.tar.gz"
      sha256 "${darwin_amd64_sha}"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/${repo}/releases/download/v#{version}/veri-#{version}-linux-arm64.tar.gz"
      sha256 "${linux_arm64_sha}"
    end

    on_intel do
      url "https://github.com/${repo}/releases/download/v#{version}/veri-#{version}-linux-amd64.tar.gz"
      sha256 "${linux_amd64_sha}"
    end
  end

  def install
    bin.install "veri"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/veri version")
  end
end
EOF

jq --indent 4 -n \
  --arg version "$version" \
  --arg repo "$repo" \
  --arg desc "$desc_en" \
  --arg amd64_sha "$windows_amd64_sha" \
  --arg arm64_sha "$windows_arm64_sha" \
  '{
    version: $version,
    description: $desc,
    homepage: ("https://github.com/" + $repo),
    license: "MIT",
    architecture: {
      "64bit": {
        url: ("https://github.com/" + $repo + "/releases/download/v" + $version + "/veri-" + $version + "-windows-amd64.zip"),
        hash: $amd64_sha
      },
      "arm64": {
        url: ("https://github.com/" + $repo + "/releases/download/v" + $version + "/veri-" + $version + "-windows-arm64.zip"),
        hash: $arm64_sha
      }
    },
    bin: "veri.exe",
    checkver: "github",
    autoupdate: {
      architecture: {
        "64bit": {
          url: ("https://github.com/" + $repo + "/releases/download/v$version/veri-$version-windows-amd64.zip")
        },
        "arm64": {
          url: ("https://github.com/" + $repo + "/releases/download/v$version/veri-$version-windows-arm64.zip")
        }
      },
      hash: {
        url: "$baseurl/SHA256SUMS.txt"
      }
    }
  }' > "$out_dir/bucket/veri.json"

winget_schema_ver="1.28.0"
winget_dir="$out_dir/winget/manifests/m/maybk/Veri/${version}"
mkdir -p "$winget_dir"
amd64_upper="$(printf '%s' "$windows_amd64_sha" | tr '[:lower:]' '[:upper:]')"
arm64_upper="$(printf '%s' "$windows_arm64_sha" | tr '[:lower:]' '[:upper:]')"

cat > "$winget_dir/maybk.Veri.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.version.${winget_schema_ver}.schema.json

PackageIdentifier: maybk.Veri
PackageVersion: ${version}
DefaultLocale: en-US
ManifestType: version
ManifestVersion: ${winget_schema_ver}
EOF

{
  cat <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.installer.${winget_schema_ver}.schema.json

PackageIdentifier: maybk.Veri
PackageVersion: ${version}
InstallerType: zip
NestedInstallerType: portable
NestedInstallerFiles:
- RelativeFilePath: veri.exe
  PortableCommandAlias: veri
EOF
  if [ -n "$release_date" ]; then
    printf 'ReleaseDate: %s\n' "$release_date"
  fi
  cat <<EOF
Installers:
- Architecture: x64
  InstallerUrl: https://github.com/${repo}/releases/download/v${version}/veri-${version}-windows-amd64.zip
  InstallerSha256: ${amd64_upper}
- Architecture: arm64
  InstallerUrl: https://github.com/${repo}/releases/download/v${version}/veri-${version}-windows-arm64.zip
  InstallerSha256: ${arm64_upper}
ManifestType: installer
ManifestVersion: ${winget_schema_ver}
EOF
} > "$winget_dir/maybk.Veri.installer.yaml"

cat > "$winget_dir/maybk.Veri.locale.en-US.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.defaultLocale.${winget_schema_ver}.schema.json

PackageIdentifier: maybk.Veri
PackageVersion: ${version}
PackageLocale: en-US
Publisher: maybk
PublisherUrl: https://github.com/maybk
PublisherSupportUrl: https://github.com/${repo}/issues
PackageName: veri
PackageUrl: https://github.com/${repo}
License: MIT
LicenseUrl: https://github.com/${repo}/blob/HEAD/LICENSE
ShortDescription: ${desc_en}
Tags:
- cli
- mcp
- mcp-server
- web-search
- markdown
- ai-agents
- agent-skills
- golang
ReleaseNotesUrl: https://github.com/${repo}/releases/tag/v${version}
ManifestType: defaultLocale
ManifestVersion: ${winget_schema_ver}
EOF

cat > "$winget_dir/maybk.Veri.locale.zh-CN.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.locale.${winget_schema_ver}.schema.json

PackageIdentifier: maybk.Veri
PackageVersion: ${version}
PackageLocale: zh-CN
PackageName: veri
ShortDescription: ${desc_zh}
Tags:
- cli
- mcp
- 联网检索
- 网页提取
- markdown
- ai-agent
- 搜索
ManifestType: locale
ManifestVersion: ${winget_schema_ver}
EOF
