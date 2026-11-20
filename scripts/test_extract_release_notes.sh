#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
extractor="$repo_root/scripts/extract_release_notes.sh"
renderer="$repo_root/scripts/render_release_notes.sh"
ci_workflow="$repo_root/.github/workflows/ci.yml"
release_workflow="$repo_root/.github/workflows/release.yml"

if ! grep -Eq '^## \[[0-9]+\.[0-9]+\.[0-9]+\] - 未发布$' "$repo_root/CHANGELOG.md"; then
  echo "CHANGELOG.md must keep a pending semantic version for incoming changes" >&2
  exit 1
fi

if [ ! -f "$extractor" ] || [ ! -f "$renderer" ]; then
  echo "missing release notes extractor or renderer" >&2
  exit 1
fi
bash -n "$extractor"
bash -n "$renderer"

temp_dir="$(mktemp -d)"
trap 'rm -rf "$temp_dir"' EXIT

fixture="$temp_dir/CHANGELOG.md"
actual="$temp_dir/actual.md"
expected="$temp_dir/expected.md"

cat >"$fixture" <<'EOF'
# 更新日志

## [0.2.0] - 未发布

### 新增

- 尚未发布的变化。

## [0.1.0] - 2026-11-20

### 新增

- 新增正式功能。

### 修复

- 修复正式问题。

## [0.0.9] - 2026-10-01

### 修复

- 不应包含的旧版本内容。
EOF

cat >"$expected" <<'EOF'
### 新增

- 新增正式功能。

### 修复

- 修复正式问题。
EOF

bash "$extractor" "0.1.0" "$fixture" >"$actual"
diff -u "$expected" "$actual"

if bash "$extractor" "3.0.0" "$fixture" >/dev/null 2>&1; then
  echo "missing release version must fail" >&2
  exit 1
fi

cat >"$fixture" <<'EOF'
# 更新日志

## [0.2.0] - 未发布

## [0.1.0] - 2026-11-20

- 旧版本内容。
EOF

set +e
bash "$extractor" "0.2.0" "$fixture" >/dev/null 2>&1
empty_status="$?"
set -e
if [ "$empty_status" -ne 2 ]; then
  echo "empty release notes must return status 2; got $empty_status" >&2
  exit 1
fi

if bash "$extractor" "not-a-version" "$fixture" >/dev/null 2>&1; then
  echo "invalid release version must fail" >&2
  exit 1
fi

cat >"$fixture" <<'EOF'
# 更新日志

## [0.1.0] - 2026-11-20

- 第一段内容。

## [0.1.0] - 2026-11-21

- 重复版本内容。
EOF

if bash "$extractor" "0.1.0" "$fixture" >/dev/null 2>&1; then
  echo "duplicate release versions must fail" >&2
  exit 1
fi

rendered_formal="$temp_dir/rendered-formal.md"
cat >"$fixture" <<'EOF'
# 更新日志

## [0.2.0] - 未发布

### 变更

- 预览版本调整。

## [0.1.0] - 2026-11-20

### 新增

- 新增正式功能。

### 修复

- 修复正式问题。
EOF

bash "$renderer" \
  --version "0.1.0" \
  --repo "maybk/veri" \
  --server-url "https://github.com///" \
  --changelog "$fixture" >"$rendered_formal"

for required_formal in \
  '## 更新内容' \
  '- 新增正式功能。' \
  '- 修复正式问题。' \
  '## 下载安装' \
  '<th align="left">系统</th>' \
  '<th align="left">系统版本</th>' \
  '<th align="left">安装包下载</th>' \
  '<td>Windows 10 / 11</td>' \
  '<td>macOS 13.0+</td>' \
  '<td>主流发行版</td>' \
  'labelColor=334155' \
  'ZIP-x64-0969DA' \
  'ZIP-ARM64-64748B' \
  'tar.gz-Apple%20Silicon-0969DA' \
  'tar.gz-Intel-64748B' \
  'tar.gz-x64-0969DA' \
  'tar.gz-ARM64-64748B' \
  '&nbsp;&nbsp;' \
  'alt="Windows ZIP x64"' \
  'alt="Windows ZIP ARM64"' \
  'alt="macOS tar.gz Apple Silicon"' \
  'alt="macOS tar.gz Intel"' \
  'alt="Linux tar.gz x64"' \
  'alt="Linux tar.gz ARM64"'; do
  if ! grep -Fq -- "$required_formal" "$rendered_formal"; then
    echo "rendered formal release notes missing expected fragment: $required_formal" >&2
    exit 1
  fi
done

for forbidden_formal in \
  '> [!WARNING]' \
  '## 构建信息' \
  '一键脚本安装' \
  'install.sh' \
  'install.ps1' \
  'CGO_ENABLED' \
  'SHA256SUMS.txt'; do
  if grep -Fq -- "$forbidden_formal" "$rendered_formal"; then
    echo "formal release notes must not include: $forbidden_formal" >&2
    exit 1
  fi
done

for curated_suffix in \
  'windows-amd64.zip' \
  'windows-arm64.zip' \
  'darwin-arm64.tar.gz' \
  'darwin-amd64.tar.gz' \
  'linux-amd64.tar.gz' \
  'linux-arm64.tar.gz'; do
  expected_url="https://github.com/maybk/veri/releases/download/v0.1.0/veri-0.1.0-${curated_suffix}"
  if ! grep -Fq "$expected_url" "$rendered_formal"; then
    echo "rendered formal release notes missing curated download URL: $expected_url" >&2
    exit 1
  fi
  if ! grep -Fq "\"veri-\$APP_VERSION-${curated_suffix}\"" "$release_workflow"; then
    echo "curated download suffix does not match verified assets in release.yml: $curated_suffix" >&2
    exit 1
  fi
done

if grep -Eq 'logo=data:image/svg[^"]*(\+)' "$rendered_formal"; then
  echo "inline SVG badge logo query parameter must percent-encode '+' as '%2B'" >&2
  exit 1
fi

win_row_line="$(grep -nF '<td><strong>Windows</strong></td>' "$rendered_formal" | head -n 1 | cut -d: -f1)"
mac_row_line="$(grep -nF '<td><strong>macOS</strong></td>' "$rendered_formal" | head -n 1 | cut -d: -f1)"
linux_row_line="$(grep -nF '<td><strong>Linux</strong></td>' "$rendered_formal" | head -n 1 | cut -d: -f1)"
if [ -z "$win_row_line" ] || [ -z "$mac_row_line" ] || [ -z "$linux_row_line" ] ||
  [ "$win_row_line" -ge "$mac_row_line" ] || [ "$mac_row_line" -ge "$linux_row_line" ]; then
  echo "download table must list OS rows in Windows -> macOS -> Linux order" >&2
  exit 1
fi

rendered_rolling="$temp_dir/rendered-rolling.md"
sample_sha="0123456789abcdef0123456789abcdef01234567"
bash "$renderer" \
  --rolling \
  --version "0.2.0" \
  --app-version "0.2.0-pre" \
  --source-sha "$sample_sha" \
  --built-at "2026-11-20 10:30:00 UTC" \
  --repo "maybk/veri" \
  --server-url "https://github.com" \
  --changelog "$fixture" >"$rendered_rolling"

for required_rolling in \
  '> [!WARNING]' \
  '## 更新内容' \
  '- 预览版本调整。' \
  '## 下载安装' \
  '## 构建信息' \
  '- 预览版本：`0.2.0`' \
  '- 应用版本：`0.2.0-pre`' \
  '- 提交：[`0123456789ab`](https://github.com/maybk/veri/commit/0123456789abcdef0123456789abcdef01234567)' \
  '- 构建时间：`2026-11-20 10:30:00 UTC`'; do
  if ! grep -Fq -- "$required_rolling" "$rendered_rolling"; then
    echo "rendered Rolling release notes missing expected fragment: $required_rolling" >&2
    exit 1
  fi
done

for curated_suffix in \
  'windows-amd64.zip' \
  'windows-arm64.zip' \
  'darwin-arm64.tar.gz' \
  'darwin-amd64.tar.gz' \
  'linux-amd64.tar.gz' \
  'linux-arm64.tar.gz'; do
  expected_rolling_url="https://github.com/maybk/veri/releases/download/v0.2.0-pre/veri-0.2.0-rolling-0123456789ab-${curated_suffix}"
  if ! grep -Fq "$expected_rolling_url" "$rendered_rolling"; then
    echo "rendered Rolling release notes missing curated download URL: $expected_rolling_url" >&2
    exit 1
  fi
done

cat >"$fixture" <<'EOF'
# 更新日志

## [0.2.0] - 未发布

## [0.1.0] - 2026-11-20

- 已有内容。
EOF

set +e
bash "$renderer" --version "0.2.0" --changelog "$fixture" >/dev/null 2>&1
render_empty_status="$?"
set -e
if [ "$render_empty_status" -ne 2 ]; then
  echo "renderer must preserve exit status 2 on empty changelog section; got $render_empty_status" >&2
  exit 1
fi

if bash "$renderer" --rolling --version "0.1.0" --app-version "0.1.0-pre" --source-sha "shortsha" --changelog "$fixture" >/dev/null 2>&1; then
  echo "renderer must reject non-40-hex Rolling source SHA" >&2
  exit 1
fi

if bash "$renderer" --rolling --version "0.1.0" --app-version "0.1.0" --source-sha "$sample_sha" --changelog "$fixture" >/dev/null 2>&1; then
  echo "renderer must reject Rolling app-version without prerelease suffix" >&2
  exit 1
fi

if bash "$renderer" --version "0.1.0" --source-sha "$sample_sha" --changelog "$fixture" >/dev/null 2>&1; then
  echo "renderer must reject Rolling-only flags in formal mode" >&2
  exit 1
fi

if ! grep -Fq 'bash scripts/render_release_notes.sh \' "$release_workflow" ||
  ! grep -Fq -- '--version "$APP_VERSION"' "$release_workflow" ||
  ! grep -Fq -- '--notes-file "$release_notes_file"' "$release_workflow" ||
  grep -Fq -- '--generate-notes' "$release_workflow"; then
  echo "release workflow must render release notes with curated download badges from CHANGELOG.md" >&2
  exit 1
fi

validate_job="$(awk '/^  validate:/,/^  ci:/' "$release_workflow")"
if ! grep -Fq 'name: Validate release notes in CHANGELOG.md' <<<"$validate_job" ||
  ! grep -Fq 'bash scripts/extract_release_notes.sh "$version" CHANGELOG.md' <<<"$validate_job" ||
  ! grep -Fq 'steps.version.outputs.version' <<<"$validate_job"; then
  echo "release workflow must validate release notes before platform builds" >&2
  exit 1
fi

if ! grep -Fq 'bash scripts/test_extract_release_notes.sh' "$ci_workflow"; then
  echo "ordinary CI must run the release notes contract test" >&2
  exit 1
fi

# 校验仓库真实 CHANGELOG.md：待发布版本标题合法（允许刚发版时为空返回 2），且 build/config.yml 当前版本说明非空
pending_ver="$(bash "$repo_root/scripts/extract_pending_version.sh" "$repo_root/CHANGELOG.md")"
set +e
bash "$extractor" "$pending_ver" "$repo_root/CHANGELOG.md" >/dev/null 2>&1
pending_rc="$?"
set -e
if [ "$pending_rc" -ne 0 ] && [ "$pending_rc" -ne 2 ]; then
  echo "Unexpected failure extracting pending changelog section $pending_ver (rc=$pending_rc)" >&2
  exit 1
fi
current_ver="$(awk '/^[^[:space:]#]/{in_info=($1=="info:")} in_info && /^[[:space:]]+version:/{sub(/.*version:[[:space:]]*"/,""); sub(/".*/,""); print; exit}' "$repo_root/build/config.yml")"
current_notes="$(bash "$extractor" "$current_ver" "$repo_root/CHANGELOG.md")"
if ! grep -Eq '^### .+' <<<"$current_notes" || ! grep -Eq '^- .+' <<<"$current_notes"; then
  echo "Repository changelog section for $current_ver must contain a heading and at least one entry" >&2
  exit 1
fi
