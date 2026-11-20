# GitHub Actions 工作流 (`.github/workflows`)

定义仓库持续集成门禁、三端六架构产物构建、每日 Rolling 预发布与正式版本发布流水线。

## 目录结构

- `ci.yml`：`main` 推送、Pull Request 与发布/预发布共用的格式检查、静态扫描、漏洞扫描、发布契约测试、三端测试矩阵及二进制冒烟门禁。
- `release-assets.yml`：正式版与 Rolling 共用的三端六架构原生测试与静态归档构建流水线（macOS / Windows / Linux × `amd64` / `arm64`）。
- `rolling.yml`：每日定时或手动按需构建 `main` 分支最新提交并发布 `v<version>-pre` 预发布版本。
- `release.yml`：`vMAJOR.MINOR.PATCH` 标签触发的正式 GitHub Release 草稿校验、包管理器清单验证、公开与过期 Rolling 清理。

## 维护规范

- **固定 Commit SHA**：所有第三方 GitHub Actions 必须锁定到完整 40 位提交哈希并附带版本注释，由 Dependabot 按周分组更新。
- **最小权限与草稿门禁**：工作流默认声明 `permissions: contents: read`；发布产物必须先上传至 Draft Release 完整校验数量与文件名后才公开。
