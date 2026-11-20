# 构建与发布脚本 (`scripts`)

管理版本提取、发版准备、Release 说明渲染、包管理器清单生成及对应契约测试。

## 目录结构

- `extract_pending_version.sh`：读取并校验 `CHANGELOG.md` 顶部的待发布版本号。
- `extract_release_notes.sh`：从 `CHANGELOG.md` 提取指定版本的更新说明。
- `prepare_release.sh`：同步更新 `build/config.yml` 与 `CHANGELOG.md` 版本标题及日期。
- `render_release_notes.sh`：渲染正式版与 Rolling 预发布版的 GitHub Release 说明与下载表。
- `render_package_manifests.sh`：根据版本号、`build/config.yml` 元信息与 `SHA256SUMS.txt` 渲染 Homebrew、Scoop 与 WinGet 清单。
- `install.sh`：macOS / Linux 一键安装脚本（纯 POSIX `sh`，校验 `SHA256SUMS.txt`）。
- `install.ps1`：Windows PowerShell 一键安装脚本（校验 `SHA256SUMS.txt` 并配置用户 `PATH`）。

## 开发规范

- **脚本健壮性**：Bash 发布与测试脚本开启 `set -euo pipefail`；`install.sh` 使用纯 POSIX `sh`（`set -eu`），`install.ps1` 开启严格模式并启用 TLS 1.2；测试脚本在临时目录隔离运行并在退出时自动清理。

## 验证

```bash
bash scripts/test_extract_pending_version.sh
bash scripts/test_extract_release_notes.sh
bash scripts/test_prepare_release.sh
bash scripts/test_package_manifests.sh
bash scripts/test_install_scripts.sh
bash scripts/test_ci_config.sh
bash scripts/test_rolling_release_config.sh
```
