#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ci_workflow="$repo_root/.github/workflows/ci.yml"
release_workflow="$repo_root/.github/workflows/release.yml"
build_workflow="$repo_root/.github/workflows/release-assets.yml"
rolling_workflow="$repo_root/.github/workflows/rolling.yml"

for f in "$ci_workflow" "$release_workflow" "$build_workflow" "$rolling_workflow"; do
  test -s "$f"
done

# 1. 静态契约校验：排除预发布标签、共享互斥锁组、复用 CI/Build 工作流与资产完整性校验
grep -Fq "'!*-*'" "$release_workflow"
grep -Fq "uses: ./.github/workflows/ci.yml" "$release_workflow"
grep -Fq "uses: ./.github/workflows/release-assets.yml" "$release_workflow"
grep -Fq "uses: ./.github/workflows/ci.yml" "$rolling_workflow"
grep -Fq "uses: ./.github/workflows/release-assets.yml" "$rolling_workflow"
grep -Fq "group: rolling-release-mutation" "$rolling_workflow"
grep -Fq "group: rolling-release-mutation" "$release_workflow"
grep -Fq "extract_pending_version.sh" "$rolling_workflow"
grep -Fq 'draft_tag="${rolling_tag}-stage-${SHORT_SHA}"' "$rolling_workflow"
grep -Fq 'trap cleanup_draft EXIT' "$rolling_workflow"
grep -Fq 'if [ "$actual_names" != "$expected_names" ]; then' "$rolling_workflow"
grep -Fq "steps.revalidate.outputs.should_replace == 'true' && steps.download_assets.outcome == 'failure'" "$rolling_workflow"
grep -Fq 'rm -rf release-files' "$rolling_workflow"
grep -Fq 'sha256sum -c SHA256SUMS.txt' "$rolling_workflow"
grep -Fq -- "--draft=false --prerelease" "$rolling_workflow"
grep -Fq -- "--draft=false --latest" "$release_workflow"

if grep -Fq 'workflow_run:' "$rolling_workflow" || grep -Fq 'GITHUB_SHA' "$rolling_workflow"; then
  echo "Rolling workflow must use schedule/workflow_dispatch and fetched origin/main commit rather than workflow_run or GITHUB_SHA" >&2
  exit 1
fi

cleanup_job="$(awk '/^  cleanup-rolling:/,/^  notify-tap:/' "$release_workflow")"
for req_clean in \
  'git merge-base --is-ancestor "$rolling_commit" "$release_commit"' \
  'gh release delete "$tag_name" --yes' \
  'Rolling 不存在，无需清理' \
  'Rolling 指向正式版本之后的提交，保留'; do
  if ! grep -Fq "$req_clean" <<<"$cleanup_job"; then
    echo "cleanup-rolling job missing required contract: $req_clean" >&2
    exit 1
  fi
done

for target in \
  darwin-arm64.tar.gz \
  darwin-amd64.tar.gz \
  linux-amd64.tar.gz \
  linux-arm64.tar.gz \
  windows-amd64.zip \
  windows-arm64.zip; do
  grep -Fq "$target" "$build_workflow"
  grep -Fq "$target" "$rolling_workflow"
done

# 2. 离线状态机行为测试：提取 rolling.yml 的 Validate source commit 脚本并覆盖全部 8 类分支状态
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

prepare_script="$tmp_dir/prepare.sh"
awk '
  /^      - name: Validate source commit$/ { in_step=1; next }
  in_step && /^        run: \|$/ { in_run=1; next }
  in_run {
    if ($0 ~ /^  [a-zA-Z0-9_-]+:/ || $0 ~ /^      - name:/) { exit }
    sub(/^          /, "")
    print
  }
' "$rolling_workflow" > "$prepare_script"
test -s "$prepare_script"
bash -n "$prepare_script"

mock_bin="$tmp_dir/bin"
mkdir -p "$mock_bin"
cat > "$mock_bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" = "api" ]; then
  endpoint="${2:-}"
  case "$endpoint" in
    repos/*/releases\?per_page=100)
      if [ "${MOCK_RELEASES_API_FAIL:-false}" = "true" ]; then
        echo "HTTP 502 Bad Gateway" >&2
        exit 1
      fi
      cat "$MOCK_RELEASES_JSON_FILE"
      exit 0
      ;;
    repos/*/actions/workflows/ci.yml/runs*)
      if [ "${MOCK_CI_API_FAIL:-false}" = "true" ]; then
        exit 1
      fi
      cat "$MOCK_CI_JSON_FILE"
      exit 0
      ;;
  esac
fi
echo "unexpected gh command: $*" >&2
exit 1
EOF
chmod +x "$mock_bin/gh"

releases_json_file="$tmp_dir/releases.json"
ci_json_file="$tmp_dir/ci_runs.json"
printf '[]\n' > "$releases_json_file"
printf '{"workflow_runs":[]}\n' > "$ci_json_file"

write_complete_release_json() {
  local ver="$1"
  local ssha="$2"
  local draft="${3:-false}"
  local asset_state="${4:-uploaded}"
  local omit_last="${5:-false}"
  jq -n \
    --arg tag "v${ver}-pre" \
    --arg ver "$ver" \
    --arg ssha "$ssha" \
    --argjson draft "$draft" \
    --arg state "$asset_state" \
    --argjson omit "$omit_last" \
    '
      (
        ["SHA256SUMS.txt"] + (
          [
            "darwin-arm64.tar.gz",
            "darwin-amd64.tar.gz",
            "linux-amd64.tar.gz",
            "linux-arm64.tar.gz",
            "windows-amd64.zip",
            "windows-arm64.zip"
          ] | map("veri-\($ver)-rolling-\($ssha)-" + .)
        )
      ) as $names |
      [
        {
          tag_name: $tag,
          draft: $draft,
          prerelease: true,
          assets: (
            (if $omit then $names[0:6] else $names end)
            | map({ name: ., state: $state })
          )
        }
      ]
    ' > "$releases_json_file"
}

origin_repo="$tmp_dir/origin.git"
work_repo="$tmp_dir/work"
git init --bare --quiet "$origin_repo"
git init --quiet "$work_repo"
git -C "$work_repo" checkout -q -b main
git -C "$work_repo" config user.name "Test Bot"
git -C "$work_repo" config user.email "test@example.com"
git -C "$work_repo" remote add origin "$origin_repo"

mkdir -p "$work_repo/build" "$work_repo/scripts"
cp "$repo_root/scripts/extract_pending_version.sh" "$work_repo/scripts/"
cp "$repo_root/scripts/extract_release_notes.sh" "$work_repo/scripts/"
cat > "$work_repo/build/config.yml" <<'EOF'
info:
  productName: "veri"
  version: "0.1.0"
EOF
cat > "$work_repo/CHANGELOG.md" <<'EOF'
# 更新日志

## [0.1.1] - 未发布

### 修复

- 修复测试问题。

## [0.1.0] - 2026-11-11

### 新增

- 初始正式版。
EOF
git -C "$work_repo" add .
git -C "$work_repo" commit -q -m "feat: pending change"
git -C "$work_repo" push -q origin main

run_prepare() {
  local out_file="$tmp_dir/github_output.txt"
  : > "$out_file"
  (
    cd "$work_repo"
    PATH="$mock_bin:$PATH" \
      RUNNER_TEMP="$tmp_dir" \
      GITHUB_OUTPUT="$out_file" \
      GH_TOKEN="dummy" \
      GH_REPO="maybk/veri" \
      MOCK_RELEASES_JSON_FILE="$releases_json_file" \
      MOCK_CI_JSON_FILE="$ci_json_file" \
      EVENT_NAME="${1:-schedule}" \
      EVENT_REF="${2:-refs/heads/main}" \
      FORCE_PUBLISH="${3:-false}" \
      bash "$prepare_script" >/dev/null
  ) && cat "$out_file"
}

# 2.1 无 -pre 标签且未发布内容非空、无历史 CI -> 构建并触发 CI
out="$(run_prepare schedule refs/heads/main false)"
grep -Fq "should_publish=true" <<<"$out"
grep -Fq "needs_ci=true" <<<"$out"
grep -Fq "rolling_version=0.1.1" <<<"$out"

# 2.2 同提交已发布完整 7 个资产的预发布 -> 自动跳过重复构建
head_sha="$(git -C "$work_repo" rev-parse HEAD)"
head_short_sha="${head_sha:0:12}"
git -C "$work_repo" tag -a -f "v0.1.1-pre" -m "Rolling" "$head_sha"
git -C "$work_repo" push -q --force origin "refs/tags/v0.1.1-pre"
write_complete_release_json "0.1.1" "$head_short_sha" false uploaded false
printf '{"workflow_runs":[{"event":"push","head_branch":"main","conclusion":"success"}]}\n' > "$ci_json_file"
out="$(run_prepare schedule refs/heads/main false)"
grep -Fq "should_publish=false" <<<"$out"
grep -Fq "needs_ci=false" <<<"$out"

# 2.3 手动 force=true 且最近一次 push CI 成功 -> 复用 CI 直接重新打包
out="$(run_prepare workflow_dispatch refs/heads/main true)"
grep -Fq "should_publish=true" <<<"$out"
grep -Fq "needs_ci=false" <<<"$out"

# 2.4 最近一次 push CI 失败 -> 必须重新运行 CI 门禁
printf '{"workflow_runs":[{"event":"push","head_branch":"main","conclusion":"failure"},{"event":"push","head_branch":"main","conclusion":"success"}]}\n' > "$ci_json_file"
out="$(run_prepare workflow_dispatch refs/heads/main true)"
grep -Fq "should_publish=true" <<<"$out"
grep -Fq "needs_ci=true" <<<"$out"
printf '{"workflow_runs":[{"event":"push","head_branch":"main","conclusion":"success"}]}\n' > "$ci_json_file"

# 2.5 孤儿标签或残缺预发布（草稿态 / 缺资产 / starter 状态 / SHA 不一致）-> 自动自愈重建
printf '[]\n' > "$releases_json_file"
grep -Fq "should_publish=true" <<<"$(run_prepare schedule refs/heads/main false)"
write_complete_release_json "0.1.1" "$head_short_sha" true uploaded false
grep -Fq "should_publish=true" <<<"$(run_prepare schedule refs/heads/main false)"
write_complete_release_json "0.1.1" "$head_short_sha" false uploaded true
grep -Fq "should_publish=true" <<<"$(run_prepare schedule refs/heads/main false)"
write_complete_release_json "0.1.1" "$head_short_sha" false starter false
grep -Fq "should_publish=true" <<<"$(run_prepare schedule refs/heads/main false)"
write_complete_release_json "0.1.1" "deadbeefcafe" false uploaded false
grep -Fq "should_publish=true" <<<"$(run_prepare schedule refs/heads/main false)"

# 2.6 GitHub Releases API 5xx 故障时快速失败而非误触发重建
if MOCK_RELEASES_API_FAIL=true run_prepare schedule refs/heads/main false >/dev/null 2>&1; then
  echo "prepare must fail fast on GitHub Releases API errors" >&2
  exit 1
fi

# 2.7 正式版本标签提交或待发布段落为空 -> 自动跳过 Rolling
git -C "$work_repo" push -q origin ":refs/tags/v0.1.1-pre"
git -C "$work_repo" tag -d "v0.1.1-pre" >/dev/null
git -C "$work_repo" tag -a "v0.1.1" -m "veri v0.1.1" "$head_sha"
git -C "$work_repo" push -q origin "refs/tags/v0.1.1"
printf '[]\n' > "$releases_json_file"
grep -Fq "should_publish=false" <<<"$(run_prepare schedule refs/heads/main false)"

git -C "$work_repo" checkout -q main
cat > "$work_repo/CHANGELOG.md" <<'EOF'
# 更新日志

## [0.1.2] - 未发布

## [0.1.1] - 2026-11-17

### 修复

- 修复测试问题。
EOF
git -C "$work_repo" add CHANGELOG.md
git -C "$work_repo" commit -q -m "chore: empty pending changelog"
git -C "$work_repo" push -q origin main
grep -Fq "should_publish=false" <<<"$(run_prepare schedule refs/heads/main false)"

# 2.8 非 main 分支手动触发 -> 必须拒绝
if run_prepare workflow_dispatch refs/heads/feature false >/dev/null 2>&1; then
  echo "manual Rolling dispatch from non-main branch must be rejected" >&2
  exit 1
fi
