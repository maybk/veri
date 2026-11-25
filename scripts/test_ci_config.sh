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

# 3. 校验 ci.yml 只读权限、可被发布工作流复用、涵盖静态扫描/漏洞扫描/三端竞态测试/并行六架构编译/Artifact 往返校验
if ! grep -q "contents: read" "$ci_workflow" || grep -q "contents: write" "$ci_workflow"; then
  echo "Ordinary CI must use read-only repository permissions" >&2
  exit 1
fi
if ! grep -q "contents: read" "$release_assets_workflow" || grep -q "contents: write" "$release_assets_workflow"; then
  echo "Release assets build workflow must use read-only repository permissions" >&2
  exit 1
fi

for readonly_wf in "$ci_workflow" "$release_assets_workflow"; do
  checkout_count="$(grep -Fc 'uses: actions/checkout@' "$readonly_wf")"
  persist_false_count="$(grep -Fc 'persist-credentials: false' "$readonly_wf")"
  if [ "$checkout_count" -eq 0 ] || [ "$checkout_count" -ne "$persist_false_count" ]; then
    echo "Every checkout in ${readonly_wf#"$repo_root/"} must set persist-credentials: false (got $persist_false_count/$checkout_count)" >&2
    exit 1
  fi
  upload_count="$(grep -Fc 'uses: actions/upload-artifact@' "$readonly_wf")"
  overwrite_count="$(grep -Fc 'overwrite: true' "$readonly_wf")"
  if [ "$upload_count" -eq 0 ] || [ "$upload_count" -ne "$overwrite_count" ]; then
    echo "Every actions/upload-artifact in ${readonly_wf#"$repo_root/"} must set overwrite: true (got $overwrite_count/$upload_count)" >&2
    exit 1
  fi
done

for required_ci in \
  'workflow_call:' \
  'source_sha:' \
  'golangci/golangci-lint-action@' \
  'golang.org/x/vuln/cmd/govulncheck@' \
  'darwin/arm64' \
  'windows/arm64' \
  'linux/arm64' \
  'go-version-file: go.mod' \
  'cross-build:' \
  'go test -race ./...' \
  'CGO_ENABLED: "1"' \
  'artifact-upload:' \
  'artifact-roundtrip:' \
  'if-no-files-found: error' \
  'overwrite: true' \
  'cmp --silent'; do
  if ! grep -Fq -- "$required_ci" "$ci_workflow"; then
    echo "ci.yml is missing required gate: $required_ci" >&2
    exit 1
  fi
done

ci_cross_build_job="$(awk '/^  cross-build:/,/^  test:/' "$ci_workflow")"
if ! grep -Fq "if: github.workflow == 'CI'" <<<"$ci_cross_build_job" ||
  ! grep -Fq 'darwin/arm64' <<<"$ci_cross_build_job" ||
  ! grep -Fq 'windows/arm64' <<<"$ci_cross_build_job" ||
  ! grep -Fq 'linux/arm64' <<<"$ci_cross_build_job" ||
  ! grep -Fq 'CGO_ENABLED=0' <<<"$ci_cross_build_job"; then
  echo "ci.yml cross-build job must run on direct CI workflows and verify all 6 static targets" >&2
  exit 1
fi

ci_test_job="$(awk '/^  test:/,/^  artifact-upload:/' "$ci_workflow")"
if ! grep -Fq "if: github.workflow == 'CI'" <<<"$ci_test_job" ||
  ! grep -Fq 'CGO_ENABLED: "1"' <<<"$ci_test_job" ||
  ! grep -Fq 'go test -race ./...' <<<"$ci_test_job" ||
  ! grep -Fq 'meta="$(go version -m "$bin_path")"' <<<"$ci_test_job" ||
  ! grep -Fq 'grep -Fq "CGO_ENABLED=0" <<<"$meta"' <<<"$ci_test_job" ||
  grep -Fq 'Windows CGO_ENABLED=0' <<<"$ci_test_job"; then
  echo "ci.yml test matrix must run race tests across all 3 OSes on direct CI and skip duplicate runs in Release/Rolling callers" >&2
  exit 1
fi

ci_roundtrip_job="$(awk '/^  artifact-roundtrip:/,0' "$ci_workflow")"
if ! grep -Fq "steps.download.outcome == 'failure'" <<<"$ci_roundtrip_job" ||
  ! grep -Fq 'rm -rf "$RUNNER_TEMP/artifact-download"' <<<"$ci_roundtrip_job" ||
  ! grep -Fq 'cmp --silent' <<<"$ci_roundtrip_job"; then
  echo "ci.yml artifact-roundtrip must clean partial downloads and retry only on failure" >&2
  exit 1
fi

# 4. 校验 release-assets.yml 覆盖三端原生架构 Runner 及静态链接/竞态测试/归档完整性验证
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

win_amd64_job="$(awk '/^  windows-amd64:/,/^  windows-arm64:/' "$release_assets_workflow")"
if [ "$(grep -Fc '$PSNativeCommandUseErrorActionPreference = $true' <<<"$win_amd64_job")" -ne 2 ] ||
  ! grep -Fq '$env:CGO_ENABLED = "1"' <<<"$win_amd64_job" ||
  ! grep -Fq 'go test -race ./...' <<<"$win_amd64_job" ||
  ! grep -Fq 'System.IO.Compression.ZipFile' <<<"$win_amd64_job" ||
  ! grep -Fq 'foreach ($reqEntry in @("veri.exe", "LICENSE", "THIRD_PARTY_NOTICES.md", "README.md", "README.en.md", "skills/veri/SKILL.md"))' <<<"$win_amd64_job"; then
  echo "release-assets.yml windows-amd64 must fail fast on native commands, run -race tests, and verify all 6 ZIP archive entries" >&2
  exit 1
fi

win_arm64_job="$(awk '/^  windows-arm64:/,0' "$release_assets_workflow")"
if [ "$(grep -Fc '$PSNativeCommandUseErrorActionPreference = $true' <<<"$win_arm64_job")" -ne 2 ] ||
  ! grep -Fq 'go test ./...' <<<"$win_arm64_job" ||
  ! grep -Fq 'System.IO.Compression.ZipFile' <<<"$win_arm64_job" ||
  ! grep -Fq 'foreach ($reqEntry in @("veri.exe", "LICENSE", "THIRD_PARTY_NOTICES.md", "README.md", "README.en.md", "skills/veri/SKILL.md"))' <<<"$win_arm64_job"; then
  echo "release-assets.yml windows-arm64 must fail fast on native commands, run native tests, and verify all 6 ZIP archive entries" >&2
  exit 1
fi

# 5. 校验 release.yml 复用 CI 与构建流水线、固定不可变 source_sha、下载防抖重试、实测校验和并先 Draft 验证资产后再公开
release_ci_job="$(awk '/^  ci:/,/^  build:/' "$release_workflow")"
if ! grep -Fq 'needs: validate' <<<"$release_ci_job" ||
  ! grep -Fq 'uses: ./.github/workflows/ci.yml' <<<"$release_ci_job" ||
  ! grep -Fq 'source_sha: ${{ needs.validate.outputs.source_sha }}' <<<"$release_ci_job"; then
  echo "release.yml ci job must depend on validate and reuse ci.yml at validated source_sha" >&2
  exit 1
fi

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
  if ! grep -Fq -- "$expected_asset" <<<"$publish_job"; then
    echo "Published release must verify asset: $expected_asset" >&2
    exit 1
  fi
done
grep -Fq 'ref: ${{ needs.validate.outputs.source_sha }}' <<<"$publish_job"
grep -Fq 'persist-credentials: false' <<<"$publish_job"
grep -Fq "steps.download_assets.outcome == 'failure'" <<<"$publish_job"
grep -Fq 'rm -rf release-files' <<<"$publish_job"
grep -Fq 'test "$(wc -l < SHA256SUMS.txt' <<<"$publish_job"
grep -Fq 'sha256sum -c SHA256SUMS.txt' <<<"$publish_job"
grep -Fq 'all(.state == "uploaded")' <<<"$publish_job"
grep -Fq 'chmod 600 "$sig_key_file"' <<<"$publish_job"

cleanup_rolling_job="$(awk '/^  cleanup-rolling:/,/^  notify-tap:/' "$release_workflow")"
if ! grep -Fq -- '- validate' <<<"$cleanup_rolling_job" ||
  ! grep -Fq -- '- publish' <<<"$cleanup_rolling_job" ||
  ! grep -Fq 'ref: ${{ needs.validate.outputs.source_sha }}' <<<"$cleanup_rolling_job"; then
  echo "release.yml cleanup-rolling job must depend on validate and publish and check out validated source_sha" >&2
  exit 1
fi

notify_tap_job="$(awk '/^  notify-tap:/,0' "$release_workflow")"
for required_notify in \
  '- validate' \
  '- publish' \
  'permissions: {}' \
  'TAP_REPO: maybk/homebrew-tap' \
  'TAP_WORKFLOW: sync.yml' \
  'TAP_APP: veri' \
  'actions/create-github-app-token@' \
  'repositories: homebrew-tap' \
  'permission-actions: write' \
  'TAP_DISPATCH_APP_CLIENT_ID' \
  'TAP_DISPATCH_APP_PRIVATE_KEY' \
  'TAP_DISPATCH_TOKEN' \
  '/actions/workflows/${TAP_WORKFLOW}/dispatches' \
  'return_run_details: true'; do
  if ! grep -Fq -- "$required_notify" <<<"$notify_tap_job"; then
    echo "release.yml notify-tap job missing required contract: $required_notify" >&2
    exit 1
  fi
done
if grep -Eq 'permission-contents:|/repos/\$\{TAP_REPO\}/dispatches|repository_dispatch' <<<"$notify_tap_job"; then
  echo "release.yml notify-tap must use workflow_dispatch with Actions: write only (never Contents: write or repository_dispatch)" >&2
  exit 1
fi

if command -v python3 >/dev/null 2>&1 && command -v jq >/dev/null 2>&1 && command -v curl >/dev/null 2>&1; then
  tap_tmp="$(mktemp -d)"
  tap_mock_pid=""
  cleanup_tap_test() {
    if [ -n "$tap_mock_pid" ]; then
      kill "$tap_mock_pid" >/dev/null 2>&1 || true
      wait "$tap_mock_pid" >/dev/null 2>&1 || true
    fi
    rm -rf "$tap_tmp"
  }
  trap cleanup_tap_test EXIT

  extract_step_script() {
    local step_name="$1"
    local out_file="$2"
    awk -v step="      - name: ${step_name}" '
      $0 == step { in_step=1; next }
      in_step && /^        run: \|$/ { in_run=1; next }
      in_run {
        if ($0 ~ /^  [a-zA-Z0-9_-]+:/ || $0 ~ /^      - name:/) { exit }
        sub(/^          /, "")
        print
      }
    ' "$release_workflow" > "$out_file"
    test -s "$out_file"
    bash -n "$out_file"
  }

  auth_mode_script="$tap_tmp/auth_mode.sh"
  dispatch_script="$tap_tmp/dispatch.sh"
  extract_step_script "Detect tap dispatch authentication mode" "$auth_mode_script"
  extract_step_script "Dispatch tap sync workflow" "$dispatch_script"

  # Auth mode detection matrix: App priority, partial-App warning + PAT fallback, CRLF-only PAT, none
  run_auth_mode() {
    local out_file="$tap_tmp/auth_out.txt"
    local log_file="$tap_tmp/auth_log.txt"
    : > "$out_file"
    APP_CLIENT_ID="$1" APP_PRIVATE_KEY="$2" PAT_TOKEN="$3" GITHUB_OUTPUT="$out_file" \
      bash "$auth_mode_script" > "$log_file"
    cat "$out_file"
  }
  [ "$(run_auth_mode "Iv23test" "pem-key" "pat-tok")" = "mode=app" ]
  [ "$(run_auth_mode "Iv23test" "" "pat-tok")" = "mode=pat" ]
  grep -Fq "::warning::Incomplete GitHub App configuration" "$tap_tmp/auth_log.txt"
  [ "$(run_auth_mode "" "pem-key" "")" = "mode=none" ]
  grep -Fq "::warning::Incomplete GitHub App configuration" "$tap_tmp/auth_log.txt"
  [ "$(run_auth_mode "" "" $'\r\n')" = "mode=none" ]
  [ "$(run_auth_mode "" "" "")" = "mode=none" ]

  tap_state="$tap_tmp/state.json"
  tap_port_file="$tap_tmp/port.txt"
  printf '{"mode":"ok_200","calls":0,"last_body":null,"last_auth":""}\n' > "$tap_state"

  python3 - "$tap_state" "$tap_port_file" <<'PY' &
import http.server, json, pathlib, sys

state_path = pathlib.Path(sys.argv[1])
port_path = pathlib.Path(sys.argv[2])

class DispatchHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass

    def do_POST(self):
        st = json.loads(state_path.read_text())
        n = int(self.headers.get("Content-Length", "0"))
        body = json.loads(self.rfile.read(n).decode()) if n > 0 else {}
        st["calls"] += 1
        st["last_body"] = body
        st["last_auth"] = self.headers.get("Authorization", "")
        st["last_path"] = self.path
        state_path.write_text(json.dumps(st))

        mode = st["mode"]
        if mode == "ok_200":
            resp = json.dumps({
                "workflow_run_id": 42,
                "html_url": "https://github.com/maybk/homebrew-tap/actions/runs/42",
            }).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(resp)))
            self.end_headers()
            self.wfile.write(resp)
            return
        if mode == "ok_204":
            self.send_response(204)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        if mode == "retry_503_then_200":
            if st["calls"] == 1:
                resp = json.dumps({"message": "Service Unavailable"}).encode()
                self.send_response(503)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(resp)))
                self.end_headers()
                self.wfile.write(resp)
                return
            resp = json.dumps({
                "workflow_run_id": 99,
                "html_url": "https://github.com/maybk/homebrew-tap/actions/runs/99",
            }).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(resp)))
            self.end_headers()
            self.wfile.write(resp)
            return
        resp = json.dumps({"message": "Resource not accessible by personal access token"}).encode()
        self.send_response(403)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(resp)))
        self.end_headers()
        self.wfile.write(resp)

srv = http.server.HTTPServer(("127.0.0.1", 0), DispatchHandler)
port_path.write_text(str(srv.server_address[1]))
srv.serve_forever()
PY
  tap_mock_pid="$!"
  for _ in $(seq 1 30); do
    [ -s "$tap_port_file" ] && break
    sleep 0.05
  done
  if [ ! -s "$tap_port_file" ]; then
    echo "Mock tap dispatch server failed to start" >&2
    exit 1
  fi
  tap_api="http://127.0.0.1:$(cat "$tap_port_file")"
  tap_summary="$tap_tmp/summary.md"

  # 1. Unconfigured credentials skip cleanly with manual fallback notice and step summary
  : > "$tap_summary"
  TAP_REPO="maybk/homebrew-tap" TAP_WORKFLOW="sync.yml" TAP_REF="main" TAP_APP="veri" APP_VERSION="0.2.7" \
    AUTH_MODE="none" APP_TOKEN="" PAT_TOKEN="" GITHUB_API_URL="$tap_api" GITHUB_STEP_SUMMARY="$tap_summary" \
    bash "$dispatch_script" | grep -Fq "Skipping automatic maybk/homebrew-tap sync"
  grep -Fq "Status: skipped" "$tap_summary"

  # 2. App token dispatches workflow_dispatch (HTTP 200 with run URL and step summary)
  : > "$tap_summary"
  TAP_REPO="maybk/homebrew-tap" TAP_WORKFLOW="sync.yml" TAP_REF="main" TAP_APP="veri" APP_VERSION="0.2.7" \
    AUTH_MODE="app" APP_TOKEN="app-tok" PAT_TOKEN="pat-tok" GITHUB_API_URL="$tap_api" \
    GITHUB_STEP_SUMMARY="$tap_summary" TAP_DISPATCH_RETRY_SLEEP_SEC="0.05" \
    bash "$dispatch_script" | grep -Fq "https://github.com/maybk/homebrew-tap/actions/runs/42"
  grep -Fq "https://github.com/maybk/homebrew-tap/actions/runs/42" "$tap_summary"
  jq -e '
    .last_path == "/repos/maybk/homebrew-tap/actions/workflows/sync.yml/dispatches" and
    .last_auth == "Bearer app-tok" and
    .last_body.ref == "main" and
    .last_body.inputs.app == "veri" and
    .last_body.inputs.expected_version == "0.2.7" and
    .last_body.return_run_details == true
  ' "$tap_state" >/dev/null

  # 3. HTTP 204 backward-compatible response and trailing CRLF in PAT token succeed cleanly
  printf '{"mode":"ok_204","calls":0,"last_body":null,"last_auth":""}\n' > "$tap_state"
  TAP_REPO="maybk/homebrew-tap" TAP_WORKFLOW="sync.yml" TAP_REF="main" TAP_APP="veri" APP_VERSION="0.2.7" \
    AUTH_MODE="pat" APP_TOKEN="" PAT_TOKEN=$'pat-crlf\r\n' GITHUB_API_URL="$tap_api" TAP_DISPATCH_RETRY_SLEEP_SEC="0.05" \
    bash "$dispatch_script" | grep -Fq "HTTP 204"
  jq -e '.calls == 1 and .last_auth == "Bearer pat-crlf"' "$tap_state" >/dev/null

  # 4. Quote or control character in dispatch token is rejected before invoking curl
  if TAP_REPO="maybk/homebrew-tap" TAP_WORKFLOW="sync.yml" TAP_REF="main" TAP_APP="veri" APP_VERSION="0.2.7" \
      AUTH_MODE="pat" APP_TOKEN="" PAT_TOKEN='bad"token' GITHUB_API_URL="$tap_api" \
      bash "$dispatch_script" >/dev/null 2>&1; then
    echo "Expected notify-tap dispatch script to reject quote characters in token" >&2
    exit 1
  fi
  jq -e '.calls == 1' "$tap_state" >/dev/null

  # 5. Transient 503 retries and succeeds
  printf '{"mode":"retry_503_then_200","calls":0,"last_body":null,"last_auth":""}\n' > "$tap_state"
  TAP_REPO="maybk/homebrew-tap" TAP_WORKFLOW="sync.yml" TAP_REF="main" TAP_APP="veri" APP_VERSION="0.2.7" \
    AUTH_MODE="pat" APP_TOKEN="" PAT_TOKEN="pat-tok" GITHUB_API_URL="$tap_api" TAP_DISPATCH_RETRY_SLEEP_SEC="0.05" \
    bash "$dispatch_script" | grep -Fq "https://github.com/maybk/homebrew-tap/actions/runs/99"
  jq -e '.calls == 2 and .last_auth == "Bearer pat-tok"' "$tap_state" >/dev/null

  # 6. 403 permission error fails fast without silent swallow
  printf '{"mode":"err_403","calls":0,"last_body":null,"last_auth":""}\n' > "$tap_state"
  if TAP_REPO="maybk/homebrew-tap" TAP_WORKFLOW="sync.yml" TAP_REF="main" TAP_APP="veri" APP_VERSION="0.2.7" \
      AUTH_MODE="pat" APP_TOKEN="" PAT_TOKEN="pat-tok" GITHUB_API_URL="$tap_api" TAP_DISPATCH_RETRY_SLEEP_SEC="0.05" \
      bash "$dispatch_script" >/dev/null 2>&1; then
    echo "Expected notify-tap dispatch script to fail visibly on HTTP 403" >&2
    exit 1
  fi
  jq -e '.calls == 1' "$tap_state" >/dev/null

  # 7. Non-HTTP curl transport failure reports HTTP 000 cleanly (never empty status)
  kill "$tap_mock_pid" >/dev/null 2>&1 || true
  wait "$tap_mock_pid" >/dev/null 2>&1 || true
  tap_mock_pid=""
  set +e
  conn_err_out="$(
    TAP_REPO="maybk/homebrew-tap" TAP_WORKFLOW="sync.yml" TAP_REF="main" TAP_APP="veri" APP_VERSION="0.2.7" \
      AUTH_MODE="pat" APP_TOKEN="" PAT_TOKEN="pat-tok" GITHUB_API_URL="$tap_api" TAP_DISPATCH_RETRY_SLEEP_SEC="0.01" \
      bash "$dispatch_script" 2>&1
  )"
  conn_err_rc="$?"
  set -e
  [ "$conn_err_rc" -ne 0 ]
  grep -Fq "(HTTP 000)" <<<"$conn_err_out"
fi

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
grep -Fq "golangci-lint --version" "$repo_root/Taskfile.yml"
grep -Fq "golangci-lint run" "$repo_root/Taskfile.yml"
grep -Fq "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@" "$repo_root/Taskfile.yml"
grep -Fq "govulncheck ./..." "$repo_root/Taskfile.yml"
grep -Fq "golang.org/x/vuln/cmd/govulncheck@" "$repo_root/Taskfile.yml"
if [ "$(grep -Fc 'if [ "{{ARCH}}" = "amd64" ] && command -v gcc >/dev/null 2>&1; then' "$repo_root/Taskfile.yml")" -ne 2 ]; then
  echo "Taskfile.yml must enable conditional Windows amd64+gcc -race detection in both test and test:cover" >&2
  exit 1
fi

task_lint_ver="$(awk '/^[[:space:]]+GOLANGCI_LINT_VERSION:[[:space:]]*/ { print $2; exit }' "$repo_root/Taskfile.yml")"
task_vuln_ver="$(awk '/^[[:space:]]+GOVULNCHECK_VERSION:[[:space:]]*/ { print $2; exit }' "$repo_root/Taskfile.yml")"
if [[ ! "$task_lint_ver" =~ ^v2\.[0-9]+\.[0-9]+$ ]] || [[ ! "$task_vuln_ver" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Invalid Taskfile.yml lint tool versions: golangci-lint=${task_lint_ver:-<missing>}, govulncheck=${task_vuln_ver:-<missing>}" >&2
  exit 1
fi
if ! grep -Fq "version: $task_lint_ver" "$ci_workflow" ||
  ! grep -Fq "golang.org/x/vuln/cmd/govulncheck@$task_vuln_ver" "$ci_workflow"; then
  echo "ci.yml and Taskfile.yml must pin matching golangci-lint ($task_lint_ver) and govulncheck ($task_vuln_ver) versions" >&2
  exit 1
fi
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
if [ "$(grep -Fc 'if ($LASTEXITCODE -ne 0)' "$repo_root/build/Taskfile.yml")" -lt 3 ] ||
  ! grep -Fq 'System.Text.UTF8Encoding($false)' "$repo_root/build/Taskfile.yml" ||
  ! grep -Fq '{{.ARGS | default .CLI_ARGS}}' "$repo_root/Taskfile.yml"; then
  echo "Taskfile.yml and build/Taskfile.yml must guard Windows native exit codes, write LF UTF-8 checksums, and forward release:prepare args" >&2
  exit 1
fi
test -s "$repo_root/build/darwin/Taskfile.yml"
test -s "$repo_root/build/linux/Taskfile.yml"
test -s "$repo_root/build/windows/Taskfile.yml"
test ! -e "$repo_root/.goreleaser.yaml"
test ! -e "$repo_root/scripts/render_package_manifests.sh"
