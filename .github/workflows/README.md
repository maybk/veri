# GitHub Actions 工作流 (`.github/workflows`)

定义仓库持续集成门禁、三端六架构产物构建、每日 Rolling 预发布与正式版本发布流水线。

## 目录结构

- `ci.yml`：`main` 推送、PR 与发布共用的质量门禁（静态/漏洞扫描、契约测试与跨 Job Artifact 校验）；直接触发时并行执行六架构交叉编译与三端 `-race` 测试及冒烟，被发布工作流复用时自动跳过与 `release-assets.yml` 重复的矩阵。
- `release-assets.yml`：正式版与 Rolling 共用的三端六架构原生测试（含 macOS、Linux 与 Windows `amd64` `-race`）、静态归档构建与条目完整性校验流水线。
- `rolling.yml`：每日定时或手动按需构建 `main` 分支最新提交并发布 `v<version>-pre` 预发布版本。
- `release.yml`：`vMAJOR.MINOR.PATCH` 标签触发的正式 GitHub Release 资产与校验和验证、草稿公开及过期 Rolling 清理。

## 维护规范

- **固定 Commit SHA**：所有第三方 GitHub Actions 必须锁定到完整 40 位提交哈希并附带版本注释，由 Dependabot 按周分组更新。
- **最小权限与防抖重试**：工作流默认声明 `permissions: contents: read`，无需 Git 远端凭据的 `actions/checkout` 显式关闭 `persist-credentials: false`；产物上传均设 `overwrite: true`，跨 Job 下载配置失败清理重试。
- **草稿门禁**：发布产物必须先通过 `SHA256SUMS.txt` 校验并上传至 Draft Release 核验数量与文件名后才公开。
