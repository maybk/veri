#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
extractor="$repo_root/scripts/extract_release_notes.sh"
renderer="$repo_root/scripts/render_release_notes.sh"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

fixture="$tmp_dir/CHANGELOG.md"
cat > "$fixture" <<'EOF'
# 更新日志

## [0.2.0] - 未发布

## [0.1.0] - 2026-11-16

### 新增

- 支持三端 CLI、MCP 与 Agent Skill 统一分发。
EOF

# 空的待发布段落返回状态码 2
set +e
bash "$extractor" "0.2.0" "$fixture" >/dev/null 2>&1
rc="$?"
set -e
[ "$rc" -eq 2 ]

notes="$(bash "$extractor" "0.1.0" "$fixture")"
grep -Fq "支持三端 CLI、MCP 与 Agent Skill 统一分发" <<<"$notes"

formal_md="$(bash "$renderer" --version "0.1.0" --changelog "$fixture" --repo "maybk/veri")"
grep -Fq "## 更新内容" <<<"$formal_md"
grep -Fq "veri-0.1.0-darwin-arm64.tar.gz" <<<"$formal_md"
grep -Fq "veri-0.1.0-windows-amd64.zip" <<<"$formal_md"
grep -Fq "veri-0.1.0-linux-amd64.tar.gz" <<<"$formal_md"
grep -Fq "https://raw.githubusercontent.com/maybk/veri/main/scripts/install.sh" <<<"$formal_md"
grep -Fq "https://raw.githubusercontent.com/maybk/veri/main/scripts/install.ps1" <<<"$formal_md"

sha40="0123456789abcdef0123456789abcdef01234567"
rolling_md="$(bash "$renderer" --rolling --version "0.1.0" --app-version "0.1.0-pre" --source-sha "$sha40" --changelog "$fixture" --repo "maybk/veri")"
grep -Fq "[!WARNING]" <<<"$rolling_md"
grep -Fq "veri-0.1.0-rolling-0123456789ab-darwin-arm64.tar.gz" <<<"$rolling_md"
grep -Fq "## 构建信息" <<<"$rolling_md"
if grep -Fq "install.sh" <<<"$rolling_md"; then
  echo "Rolling release notes should not include formal installer scripts" >&2
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
