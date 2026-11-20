#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
renderer="$repo_root/scripts/render_package_manifests.sh"

if [ ! -f "$renderer" ]; then
  echo "missing package manifest renderer: ${renderer#"$repo_root/"}" >&2
  exit 1
fi

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
bash "$renderer" "v0.1.0" "$sums" "$out" "2026-11-20" --repo "maybk/veri"

formula_file="$out/Formula/veri.rb"
scoop_file="$out/bucket/veri.json"
test -s "$formula_file"

for required_formula in \
  'class Veri < Formula' \
  'version "0.1.0"' \
  'license "MIT"' \
  'on_macos do' \
  'on_linux do' \
  'on_arm do' \
  'on_intel do' \
  'v#{version}/veri-#{version}-darwin-arm64.tar.gz' \
  'v#{version}/veri-#{version}-linux-amd64.tar.gz' \
  'bin.install "veri"' \
  'test do' \
  '1111111111111111111111111111111111111111111111111111111111111111'; do
  if ! grep -Fq -- "$required_formula" "$formula_file"; then
    echo "Rendered Formula is missing required stanza: $required_formula" >&2
    exit 1
  fi
done

formula_desc="$(sed -En 's/^[[:space:]]+desc "([^"]+)"$/\1/p' "$formula_file" | head -n 1)"
test -n "$formula_desc"
test "${#formula_desc}" -le 80
case "$formula_desc" in
  *.)
    echo "Homebrew desc must not end with a period: $formula_desc" >&2
    exit 1
    ;;
esac
if command -v ruby >/dev/null 2>&1; then
  ruby -c "$formula_file" >/dev/null
fi

jq -e --arg desc "$formula_desc" '
  .version == "0.1.0" and
  .description == $desc and
  .architecture["64bit"].hash == "5555555555555555555555555555555555555555555555555555555555555555" and
  .architecture["arm64"].hash == "6666666666666666666666666666666666666666666666666666666666666666" and
  .bin == "veri.exe" and
  .checkver == "github" and
  .autoupdate.hash.url == "$baseurl/SHA256SUMS.txt"
' "$scoop_file" >/dev/null
diff -u "$scoop_file" <(jq --indent 4 . "$scoop_file")

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
grep -Fq 'InstallerType: zip' "$winget_dir/maybk.Veri.installer.yaml"
grep -Fq 'NestedInstallerType: portable' "$winget_dir/maybk.Veri.installer.yaml"
grep -Fq 'RelativeFilePath: veri.exe' "$winget_dir/maybk.Veri.installer.yaml"
grep -Fq 'PortableCommandAlias: veri' "$winget_dir/maybk.Veri.installer.yaml"
grep -Fq 'ReleaseDate: 2026-11-20' "$winget_dir/maybk.Veri.installer.yaml"

bash "$renderer" "0.1.0" "$sums" "$tmp_dir/no-date" --repo "maybk/veri"
if grep -Fq 'ReleaseDate:' "$tmp_dir/no-date/winget/manifests/m/maybk/Veri/0.1.0/maybk.Veri.installer.yaml"; then
  echo "Unexpected ReleaseDate when release-date argument is omitted" >&2
  exit 1
fi
