#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install_sh="$repo_root/scripts/install.sh"
install_ps1="$repo_root/scripts/install.ps1"

test -s "$install_sh"
test -s "$install_ps1"

# 1. POSIX sh 语法与静态安全契约检查
sh -n "$install_sh"
grep -Fq "curl --proto '=https' --tlsv1.2" "$install_sh"
grep -Fq "SHA256SUMS.txt" "$install_sh"
grep -Fq "sysctl.proc_translated" "$install_sh"
grep -Fq 'HOME}/.local/bin' "$install_sh"

# 2. PowerShell 安装脚本静态契约检查（若宿主安装 pwsh 则同步验证语法解析）
grep -Fq "SecurityProtocolType]::Tls12" "$install_ps1"
grep -Fq "SHA256SUMS.txt" "$install_ps1"
grep -Fq "Get-FileHash" "$install_ps1"
grep -Fq "DoNotExpandEnvironmentNames" "$install_ps1"
grep -Fq "RegistryValueKind]::ExpandString" "$install_ps1"
grep -Fq '.agents\skills\veri\SKILL.md' "$install_ps1"
grep -Fq 'npx skills add' "$install_ps1"
grep -Fq 'Fail "Invalid mirror prefix: must be an https:// URL without credentials, query, or fragment"' "$install_ps1"
if grep -Fq "http://" "$install_ps1"; then
  echo "install.ps1 must not hardcode insecure http:// URLs" >&2
  exit 1
fi
if command -v pwsh >/dev/null 2>&1; then
  VERI_TEST_INSTALL_PS1="$install_ps1" pwsh -NoProfile -Command '
    $tokens = $null
    $errors = $null
    [void][System.Management.Automation.Language.Parser]::ParseFile($env:VERI_TEST_INSTALL_PS1, [ref]$tokens, [ref]$errors)
    if ($errors.Count -gt 0) {
      $errors | ForEach-Object { Write-Error $_.Message }
      exit 1
    }
  '
fi

# 3. 非法参数拦截校验（无需网络）
if sh "$install_sh" --version "bad-ver" >/dev/null 2>&1; then
  echo "install.sh must reject invalid --version" >&2
  exit 1
fi
if sh "$install_sh" --mirror "http://insecure.example.com/" >/dev/null 2>&1; then
  echo "install.sh must reject non-https --mirror" >&2
  exit 1
fi
if mirror_err="$(sh "$install_sh" --mirror "https://user:SuperSecretMirror99@mirror.example.com/?token=TokLeak88" 2>&1)"; then
  echo "install.sh must reject --mirror containing credentials or query" >&2
  exit 1
fi
if grep -Eq 'SuperSecretMirror99|TokLeak88|user:' <<<"$mirror_err"; then
  echo "install.sh leaked credentials in invalid --mirror error: $mirror_err" >&2
  exit 1
fi
if sh "$install_sh" --bin-dir "" >/dev/null 2>&1; then
  echo "install.sh must reject empty --bin-dir" >&2
  exit 1
fi
sh "$install_sh" --help >/dev/null 2>&1

# 4. 在 Unix 宿主机上通过本地模拟 GitHub Releases 执行端到端离线安装与校验失败拦截测试
uname_s="$(uname -s 2>/dev/null || echo unknown)"
if [ "$uname_s" != "Linux" ] && [ "$uname_s" != "Darwin" ]; then
  exit 0
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required to run offline installer end-to-end test" >&2
  exit 1
fi

tmp_dir="$(mktemp -d)"
server_pid=""
cleanup() {
  if [ -n "$server_pid" ]; then
    kill "$server_pid" >/dev/null 2>&1 || true
    wait "$server_pid" >/dev/null 2>&1 || true
  fi
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

case "$uname_s" in
  Linux) host_os="linux" ;;
  Darwin) host_os="darwin" ;;
esac
uname_m="$(uname -m)"
case "$uname_m" in
  x86_64 | amd64)
    host_arch="amd64"
    if [ "$host_os" = "darwin" ] && command -v sysctl >/dev/null 2>&1; then
      if [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = "1" ]; then
        host_arch="arm64"
      fi
    fi
    ;;
  arm64 | aarch64) host_arch="arm64" ;;
  *) exit 0 ;;
esac

stage="$tmp_dir/stage"
www="$tmp_dir/www"
mkdir -p "$stage" "$www/releases/download/v0.2.0" "$www/releases/download/v0.2.1"

cat > "$stage/veri" <<'EOF'
#!/bin/sh
echo "veri 0.2.0 (mock)"
EOF
chmod 755 "$stage/veri"

asset_ok="veri-0.2.0-${host_os}-${host_arch}.tar.gz"
(cd "$stage" && tar -czf "$www/releases/download/v0.2.0/$asset_ok" veri)

if command -v sha256sum >/dev/null 2>&1; then
  sha_ok="$(sha256sum "$www/releases/download/v0.2.0/$asset_ok" | awk '{print $1}')"
else
  sha_ok="$(shasum -a 256 "$www/releases/download/v0.2.0/$asset_ok" | awk '{print $1}')"
fi
printf '%s  %s\n' "$sha_ok" "$asset_ok" > "$www/releases/download/v0.2.0/SHA256SUMS.txt"

# 构造一份校验和不匹配的 v0.2.1 归档，验证 Fail-Closed 拦截
asset_bad="veri-0.2.1-${host_os}-${host_arch}.tar.gz"
cp "$www/releases/download/v0.2.0/$asset_ok" "$www/releases/download/v0.2.1/$asset_bad"
printf '%s  %s\n' "0000000000000000000000000000000000000000000000000000000000000000" "$asset_bad" > "$www/releases/download/v0.2.1/SHA256SUMS.txt"

port_file="$tmp_dir/port.txt"
python3 - "$www" "$port_file" <<'PY' &
import http.server
import os
import sys

root = sys.argv[1]
port_file = sys.argv[2]

class Handler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=root, **kwargs)
    def do_GET(self):
        if self.path == "/releases/latest":
            self.send_response(302)
            self.send_header("Location", "/releases/tag/v0.2.0")
            self.end_headers()
            return
        if self.path == "/releases/tag/v0.2.0":
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")
            return
        super().do_GET()
    def log_message(self, format, *args):
        pass

httpd = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
with open(port_file, "w", encoding="utf-8") as fh:
    fh.write(str(httpd.server_address[1]))
httpd.serve_forever()
PY
server_pid="$!"

for _ in $(seq 1 50); do
  [ -s "$port_file" ] && break
  sleep 0.05
done
port="$(cat "$port_file")"
test -n "$port"
base_url="http://127.0.0.1:${port}"

# 4a. 通过 302 自动发现最新版并成功安装（验证尾部斜杠归一化与缺失 Skill 时的可选提示）
install_dir="$tmp_dir/bin"
fake_home="$tmp_dir/home"
mkdir -p "$fake_home"
install_out="$(HOME="$fake_home" VERI_GITHUB_BASE="$base_url" sh "$install_sh" --bin-dir "$install_dir/")"
test -x "$install_dir/veri"
out_ver="$("$install_dir/veri")"
[ "$out_ver" = "veri 0.2.0 (mock)" ]
grep -Fq "npx skills add maybk/veri" <<<"$install_out"
grep -Fq "veri skill install" <<<"$install_out"

# 4b. 已安装标准路径 SKILL.md 时静默跳过 Skill 提示
mkdir -p "$fake_home/.agents/skills/veri"
printf '# skill\n' > "$fake_home/.agents/skills/veri/SKILL.md"
reinstall_out="$(HOME="$fake_home" VERI_GITHUB_BASE="$base_url" sh "$install_sh" --bin-dir "$install_dir")"
if grep -Fq "Optional Agent Skill:" <<<"$reinstall_out"; then
  echo "install.sh should skip optional skill hint when standard SKILL.md already exists" >&2
  exit 1
fi

# 4c. SHA-256 不匹配时必须中止且不能破坏已安装的二进制
if HOME="$fake_home" VERI_GITHUB_BASE="$base_url" sh "$install_sh" --version "v0.2.1" --bin-dir "$install_dir" >/dev/null 2>&1; then
  echo "install.sh must fail closed on SHA-256 checksum mismatch" >&2
  exit 1
fi
[ "$("$install_dir/veri")" = "veri 0.2.0 (mock)" ]
