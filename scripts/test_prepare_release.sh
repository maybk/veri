#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
prepare_script="$repo_root/scripts/prepare_release.sh"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

mkdir -p "$tmp_dir/build"
cat > "$tmp_dir/build/config.yml" <<'EOF'
version: '1'
info:
  companyName: "maybk"
  productName: "veri"
  version: "0.1.0"
EOF

cat > "$tmp_dir/CHANGELOG.md" <<'EOF'
# 更新日志

## [0.2.08] - 未发布

### 新增

- 支持本地 AES-256-GCM 密钥库与自动发布编排。

## [0.1.0] - 2026-11-16

### 新增

- 初始发布说明。
EOF

bash "$prepare_script" "0.3.0" --root "$tmp_dir" --date "2026-11-20" >/dev/null

grep -Fq 'version: "0.3.0"' "$tmp_dir/build/config.yml"
grep -Fq '## [0.3.1] - 未发布' "$tmp_dir/CHANGELOG.md"
grep -Fq '## [0.3.0] - 2026-11-20' "$tmp_dir/CHANGELOG.md"
test -r "$tmp_dir/build/config.yml"
test -r "$tmp_dir/CHANGELOG.md"

pending="$(bash "$repo_root/scripts/extract_pending_version.sh" "$tmp_dir/CHANGELOG.md")"
[ "$pending" = "0.3.1" ]
notes="$(bash "$repo_root/scripts/extract_release_notes.sh" "0.3.0" "$tmp_dir/CHANGELOG.md")"
grep -Fq '支持本地 AES-256-GCM 密钥库与自动发布编排' <<<"$notes"
