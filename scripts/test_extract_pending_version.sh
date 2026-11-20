#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
extractor="$repo_root/scripts/extract_pending_version.sh"

temp_dir="$(mktemp -d)"
trap 'rm -rf "$temp_dir"' EXIT

fixture="$temp_dir/CHANGELOG.md"

cat >"$fixture" <<'EOF'
# 更新日志

## [0.2.0] - 未发布

- 待发布变化。

## [0.1.0] - 2026-11-16

- 已发布变化。
EOF

version="$(bash "$extractor" "$fixture")"
[ "$version" = "0.2.0" ]

cat >"$fixture" <<'EOF'
# 更新日志

## [0.2.1] - 未发布

## [0.2.0] - 2026-11-16

- 已发布变化。
EOF

version="$(bash "$extractor" "$fixture")"
[ "$version" = "0.2.1" ]

cat >"$fixture" <<'EOF'
# 更新日志

## [0.1.0] - 2026-11-16

## [0.2.0] - 未发布
EOF

if bash "$extractor" "$fixture" >/dev/null 2>&1; then
  echo "pending version must be the first changelog version section" >&2
  exit 1
fi

cat >"$fixture" <<'EOF'
# 更新日志

## [0.1.1] - 未发布

## [0.1.2] - 未发布
EOF

if bash "$extractor" "$fixture" >/dev/null 2>&1; then
  echo "duplicate pending versions must fail" >&2
  exit 1
fi

repository_version="$(bash "$extractor" "$repo_root/CHANGELOG.md")"
[[ "$repository_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]
grep -Fxq "## [$repository_version] - 未发布" "$repo_root/CHANGELOG.md"
