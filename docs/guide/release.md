# 发布指南

`veri` 的每日 Rolling 预发布、正式版本发布及三端包管理器清单校验均通过 GitHub Actions 与 `scripts/` 自动化编排。

## 相关文件

- [`ci.yml`](../../.github/workflows/ci.yml)：`main` 分支推送与 Pull Request 的格式检查、静态扫描、漏洞扫描、竞态测试与契约门禁，同时供发布工作流复用
- [`release-assets.yml`](../../.github/workflows/release-assets.yml)：正式版与 Rolling 共用的三端六架构原生测试与静态归档构建流水线
- [`rolling.yml`](../../.github/workflows/rolling.yml)：每日定时或手动按需构建 `main` 分支未发布提交并更新 `v<version>-pre` 预发布
- [`release.yml`](../../.github/workflows/release.yml)：`vMAJOR.MINOR.PATCH` 版本标签触发的正式 GitHub Release 发布与过期 Rolling 清理
- [`extract_pending_version.sh`](../../scripts/extract_pending_version.sh)：读取并校验 `CHANGELOG.md` 顶部的待发布版本
- [`extract_release_notes.sh`](../../scripts/extract_release_notes.sh)：从 `CHANGELOG.md` 提取指定版本的更新内容
- [`prepare_release.sh`](../../scripts/prepare_release.sh)：一键同步 `build/config.yml` 与 `CHANGELOG.md` 版本段落
- [`render_release_notes.sh`](../../scripts/render_release_notes.sh)：渲染正式版与 Rolling 预发布的 GitHub Release 说明与下载表
- [`render_package_manifests.sh`](../../scripts/render_package_manifests.sh)：根据版本号、`build/config.yml` 元信息与 `SHA256SUMS.txt` 渲染并校验 Homebrew Formula、Scoop 与 WinGet 清单
- [`install.sh`](../../scripts/install.sh) / [`install.ps1`](../../scripts/install.ps1)：macOS / Linux / Windows 一键安装脚本
- [`build/config.yml`](../../build/config.yml)：应用元信息与版本号配置
- [`CHANGELOG.md`](../../CHANGELOG.md)：正式版本与待发布版本更新记录

## 版本号管理

应用版本号统一在 `build/config.yml` 的 `info.version` 中维护，运行期由 `main.go` 直接从内嵌配置读取；日常未发布变更记录在 `CHANGELOG.md` 顶部的 `## [x.y.z] - 未发布` 段落中。

## 自动化发布与产物规格

- **Rolling 预发布**：`rolling.yml` 提取 `CHANGELOG.md` 顶部的待发布版本号，复用已通过的 `main` CI（无记录时先运行 `ci.yml`），构建 `<version>-pre` 预发布资产并更新 `v<version>-pre`。若待发布内容为空、当前提交已打正式标签或完整预发布已存在则自动跳过。
- **正式版本发布**：`release.yml` 仅由 `vMAJOR.MINOR.PATCH` 标签触发，校验标签版本与 `build/config.yml` 及带正式日期的 `CHANGELOG.md` 版本段落一致，且标签提交已包含在 `main` 分支中。
- **发布资产**：
  - `veri-<version>-darwin-arm64.tar.gz`
  - `veri-<version>-darwin-amd64.tar.gz`
  - `veri-<version>-linux-amd64.tar.gz`
  - `veri-<version>-linux-arm64.tar.gz`
  - `veri-<version>-windows-amd64.zip`
  - `veri-<version>-windows-arm64.zip`
  - `SHA256SUMS.txt`（配置 `MINISIGN_SECRET_KEY` 时附带 `SHA256SUMS.txt.minisig`）
- **Release 说明与下载表**：由 `render_release_notes.sh` 生成，展示更新内容与三端安装包下载徽章（`Windows` / `macOS` / `Linux`）；`SHA256SUMS.txt` 校验文件位于底部 `Assets`。
- **草稿校验与清理**：正式版与 Rolling 均先上传资产至 Draft Release，校验全部资产完整后再公开；正式版公开后自动清理已被取代的 Rolling 预发布与标签。

## 发布新版本

1. 同步更新 `build/config.yml` 与 `CHANGELOG.md`（如需指定跳跃版本号可执行 `task release:prepare -- 0.2.0`）：
   ```bash
   task release:prepare
   ```
2. 运行完整本地质量门禁：
   ```bash
   task check
   ```
3. 提交版本变更、创建附注标签并原子推送 `main` 与标签：
   ```bash
   version="$(sed -En 's/^[[:space:]]+version:[[:space:]]*"([^"]+)".*/\1/p' build/config.yml | head -n 1)"
   git commit -m "chore(release): v$version" -- build/config.yml CHANGELOG.md
   git tag -a "v$version" -m "veri v$version"
   git push --atomic origin main "v$version"
   ```
4. Release 工作流发布完成后，[`maybk/homebrew-tap`](https://github.com/maybk/homebrew-tap) 会自动定时同步（或执行 `gh workflow run sync.yml -R maybk/homebrew-tap` 立即触发同步）。
