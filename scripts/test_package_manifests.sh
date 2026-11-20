#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
renderer="$repo_root/scripts/render_package_manifests.sh"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

sums="$tmp_dir/SHA256SUMS.txt"
cat > "$sums" <<'EOF'
1111111111111111111111111111111111111111111111111111111111111111  veri-0.1.0-darwin-arm64.tar.gz
2222222222222222222222222222222222222222222222222222222222222222  veri-0.1.0-darwin-amd64.tar.gz
3333333333333333333333333333333333333333333333333333333333333333  veri-0.1.0-linux-arm64.tar.gz
4444444444444444444444444444444444444444444444444444444444444444  veri-0.1.0-linux-amd64.tar.gz
5555555555555555555555555555555555555555555555555555555555555555  veri-0.1.0-windows-amd64.zip
6666666666666666666666666666666666666666666666666666666666666666  veri-0.1.0-windows-arm64.zip
EOF

out="$tmp_dir/manifests"
bash "$renderer" "0.1.0" "$sums" "$out" "2026-11-16" --repo "maybk/veri"

test -s "$out/Formula/veri.rb"
formula_desc="$(sed -En 's/^[[:space:]]+desc "([^"]+)"$/\1/p' "$out/Formula/veri.rb" | head -n 1)"
test -n "$formula_desc"
test "${#formula_desc}" -le 80
case "$formula_desc" in
  *.)
    echo "Homebrew desc must not end with a period: $formula_desc" >&2
    exit 1
    ;;
esac
if command -v ruby >/dev/null 2>&1; then
  ruby -c "$out/Formula/veri.rb" >/dev/null
fi
jq -e --arg desc "$formula_desc" '.bin == "veri.exe" and .version == "0.1.0" and .description == $desc' "$out/bucket/veri.json" >/dev/null

winget_dir="$out/winget/manifests/m/maybk/Veri/0.1.0"
grep -Fq "ShortDescription: $formula_desc" "$winget_dir/maybk.Veri.locale.en-US.yaml"
grep -Eq '^ShortDescription: .+' "$winget_dir/maybk.Veri.locale.zh-CN.yaml"
for wf in \
  "$winget_dir/maybk.Veri.yaml" \
  "$winget_dir/maybk.Veri.installer.yaml" \
  "$winget_dir/maybk.Veri.locale.en-US.yaml" \
  "$winget_dir/maybk.Veri.locale.zh-CN.yaml"; do
  test -s "$wf"
  grep -Fq ".1.28.0.schema.json" "$wf"
  grep -Fq "ManifestVersion: 1.28.0" "$wf"
  if command -v ruby >/dev/null 2>&1; then
    ruby -rpsych -rdate -e 'Psych.safe_load_file(ARGV[0], permitted_classes: [Date])' "$wf"
  fi
done
