#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ci_workflow="$repo_root/.github/workflows/ci.yml"
release_workflow="$repo_root/.github/workflows/release.yml"
release_assets_workflow="$repo_root/.github/workflows/release-assets.yml"
rolling_workflow="$repo_root/.github/workflows/rolling.yml"
dependabot_config="$repo_root/.github/dependabot.yml"

# 1. 校验所有工作流存在、声明最小权限，且第三方 GitHub Actions 固定为完整 40 位 Commit SHA
for wf in "$ci_workflow" "$release_workflow" "$release_assets_workflow" "$rolling_workflow"; do
  test -s "$wf"
  grep -Eq '^permissions:' "$wf"
  while IFS= read -r line; do
    ref="${line#*uses: }"
    ref="${ref%%#*}"
    ref="$(printf '%s' "$ref" | tr -d '[:space:]')"
    if [[ "$ref" == ./* ]]; then
      continue
    fi
    if [[ ! "$ref" =~ ^[^@]+@[0-9a-f]{40}$ ]]; then
      echo "Unpinned GitHub Action in $wf: $line" >&2
      exit 1
    fi
  done < <(grep -E '^[[:space:]-]*uses:[[:space:]]+' "$wf")
done

if grep -Eq 'PULL_TOKEN|REPO_URL|@main' "$release_workflow" "$release_assets_workflow" "$rolling_workflow"; then
  echo "Release workflows must not clone another repository or use floating @main actions" >&2
  exit 1
fi

# 2. 校验全仓工作流使用单一一致的官方核心 Actions 版本
for shared_action in checkout setup-go upload-artifact download-artifact; do
  action_pin_count="$(
    grep -hE "uses: actions/$shared_action@[0-9a-f]{40}" \
      "$ci_workflow" "$release_workflow" "$release_assets_workflow" "$rolling_workflow" |
      sed -E "s/.*actions\/$shared_action@([0-9a-f]{40}).*/\1/" |
      sort -u |
      wc -l |
      tr -d '[:space:]'
  )"
  if [ "$action_pin_count" -ne 1 ]; then
    echo "CI and Release must use one consistent actions/$shared_action version" >&2
    exit 1
  fi
done

# 3. 校验 ci.yml 只读权限、可被发布工作流复用、涵盖静态扫描/漏洞扫描/三端测试/交叉编译/Artifact 往返校验
if ! grep -q "contents: read" "$ci_workflow" || grep -q "contents: write" "$ci_workflow"; then
  echo "Ordinary CI must use read-only repository permissions" >&2
  exit 1
fi
if ! grep -q "contents: read" "$release_assets_workflow" || grep -q "contents: write" "$release_assets_workflow"; then
  echo "Release assets build workflow must use read-only repository permissions" >&2
  exit 1
fi

for required_ci in \
  'workflow_call:' \
  'source_sha:' \
  'golangci/golangci-lint-action@' \
  'golang.org/x/vuln/cmd/govulncheck@' \
  'darwin/arm64' \
  'windows/arm64' \
  'linux/arm64' \
  'go-version-file: go.mod' \
  'go test -race ./...' \
  'CGO_ENABLED: "1"' \
  'artifact-roundtrip:' \
  'if-no-files-found: error' \
  'cmp --silent'; do
  if ! grep -Fq "$required_ci" "$ci_workflow"; then
    echo "ci.yml is missing required gate: $required_ci" >&2
    exit 1
  fi
done

# 4. 校验 release-assets.yml 覆盖三端原生架构 Runner 及静态链接/归档完整性验证
for required_runner in \
  'runs-on: macos-latest' \
  'runs-on: ubuntu-24.04' \
  'runs-on: ubuntu-24.04-arm' \
  'runs-on: windows-latest' \
  'runs-on: windows-11-arm' \
  'go-version-file: go.mod' \
  'CGO_ENABLED: "0"' \
  'CGO_ENABLED=1 go test -race ./...' \
  '--uname "" --gname ""' \
  '--owner=0 --group=0 --numeric-owner'; do
  if ! grep -Fq -- "$required_runner" "$release_assets_workflow"; then
    echo "release-assets.yml is missing required native runner or env: $required_runner" >&2
    exit 1
  fi
done

# 5. 校验 release.yml 复用 CI 与构建流水线、实测校验和并先 Draft 验证资产后再公开
release_ci_job="$(awk '/^  ci:/,/^  build:/' "$release_workflow")"
grep -Fq 'uses: ./.github/workflows/ci.yml' <<<"$release_ci_job"

release_build_job="$(awk '/^  build:/,/^  publish:/' "$release_workflow")"
grep -Fq -- '- validate' <<<"$release_build_job"
grep -Fq -- '- ci' <<<"$release_build_job"
grep -Fq 'uses: ./.github/workflows/release-assets.yml' <<<"$release_build_job"
grep -Fq 'retention_days: 7' <<<"$release_build_job"

publish_job="$(awk '/^  publish:/,/^  cleanup-rolling:/' "$release_workflow")"
for expected_asset in \
  '"veri-$APP_VERSION-darwin-arm64.tar.gz"' \
  '"veri-$APP_VERSION-darwin-amd64.tar.gz"' \
  '"veri-$APP_VERSION-linux-amd64.tar.gz"' \
  '"veri-$APP_VERSION-linux-arm64.tar.gz"' \
  '"veri-$APP_VERSION-windows-amd64.zip"' \
  '"veri-$APP_VERSION-windows-arm64.zip"'; do
  if ! grep -Fq "$expected_asset" <<<"$publish_job"; then
    echo "Published release must verify asset: $expected_asset" >&2
    exit 1
  fi
done
grep -Fq 'test "$(wc -l < SHA256SUMS.txt' <<<"$publish_job"
grep -Fq 'sha256sum -c SHA256SUMS.txt' <<<"$publish_job"
grep -Fq 'all(.state == "uploaded")' <<<"$publish_job"
grep -Fq 'chmod 600 "$sig_key_file"' <<<"$publish_job"

# 6. 校验 .golangci.yml 为合法 v2 结构，且 build/config.yml 元信息与根级 main.go 版本注入一致
test -s "$repo_root/.golangci.yml"
grep -Eq '^version:[[:space:]]*"2"' "$repo_root/.golangci.yml"
if grep -Eq '^linters-settings:' "$repo_root/.golangci.yml"; then
  echo ".golangci.yml uses deprecated v1 top-level linters-settings under version 2" >&2
  exit 1
fi

read_info_quoted_field() {
  local field="$1"
  local raw_line re
  raw_line="$(awk -v field="$field" '
    { sub(/\015$/, "") }
    /^[^[:space:]#]/ { in_info = ($1 == "info:") }
    in_info && $0 ~ ("^[[:space:]]+" field ":[[:space:]]*") { print; exit }
  ' "$repo_root/build/config.yml")"
  re="^[[:space:]]+${field}:[[:space:]]+\"(.*)\"[[:space:]]*$"
  if [[ ! "$raw_line" =~ $re ]]; then
    echo "info.${field} in build/config.yml must be a double-quoted scalar: ${raw_line:-<missing>}" >&2
    return 1
  fi
  printf '%s' "${BASH_REMATCH[1]}"
}

config_ver="$(read_info_quoted_field version)"
if [[ ! "$config_ver" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Invalid info.version in build/config.yml: ${config_ver:-<missing>}" >&2
  exit 1
fi
config_product="$(read_info_quoted_field productName)"
if [ "$config_product" != "veri" ]; then
  echo "Invalid info.productName in build/config.yml: ${config_product:-<missing>}" >&2
  exit 1
fi
config_desc="$(read_info_quoted_field description)"
if [ -z "$config_desc" ] || [ "${#config_desc}" -gt 80 ]; then
  echo "Invalid info.description length in build/config.yml (${#config_desc}): ${config_desc:-<missing>}" >&2
  exit 1
fi
case "$config_desc" in
  " "*|*" "|*.|*\"*|*\\*|*": "*|*" #"*)
    echo "info.description in build/config.yml must be a plain scalar without leading/trailing space, trailing period, or unsafe chars: $config_desc" >&2
    exit 1
    ;;
esac
test -s "$repo_root/main.go"
grep -Fq "build/config.yml" "$repo_root/main.go"
if grep -Fq "DefaultVersion" "$repo_root/internal/core/"*.go "$repo_root/internal/spec/"*.go; then
  echo "DefaultVersion must not be hardcoded in internal/core/*.go or internal/spec/*.go" >&2
  exit 1
fi

# 7. 校验资产目录（build/、skills/）不含 .go 文件，且 Dependabot 与各平台 Taskfile.yml 齐全
if [ -n "$(find "$repo_root/build" "$repo_root/skills" -type f -name '*.go' -print -quit)" ]; then
  echo "Asset directories build/ and skills/ must not contain Go source files" >&2
  exit 1
fi
test -s "$dependabot_config"
grep -Fq 'package-ecosystem: "github-actions"' "$dependabot_config"
grep -Fq 'package-ecosystem: "gomod"' "$dependabot_config"
grep -Fq 'interval: "weekly"' "$dependabot_config"
test -s "$repo_root/Taskfile.yml"
grep -Fq "task: lint" "$repo_root/Taskfile.yml"
grep -Fq "golangci-lint run" "$repo_root/Taskfile.yml"
grep -Fq "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@" "$repo_root/Taskfile.yml"
grep -Fq "govulncheck ./..." "$repo_root/Taskfile.yml"
grep -Fq "golang.org/x/vuln/cmd/govulncheck@" "$repo_root/Taskfile.yml"
for test_script in "$repo_root"/scripts/test_*.sh; do
  test -f "$test_script"
  rel_script="${test_script#"$repo_root/"}"
  if ! grep -Fq "bash $rel_script" "$ci_workflow"; then
    echo "ci.yml is missing contract test: $rel_script" >&2
    exit 1
  fi
  if ! grep -Fq "bash $rel_script" "$repo_root/Taskfile.yml"; then
    echo "Taskfile.yml is missing contract test: $rel_script" >&2
    exit 1
  fi
done
test -s "$repo_root/build/Taskfile.yml"
grep -Fq -- '--owner=0 --group=0 --numeric-owner' "$repo_root/build/Taskfile.yml"
grep -Fq -- '--uname "" --gname ""' "$repo_root/build/Taskfile.yml"
test -s "$repo_root/build/darwin/Taskfile.yml"
test -s "$repo_root/build/linux/Taskfile.yml"
test -s "$repo_root/build/windows/Taskfile.yml"
test ! -e "$repo_root/.goreleaser.yaml"
test ! -e "$repo_root/scripts/render_package_manifests.sh"
