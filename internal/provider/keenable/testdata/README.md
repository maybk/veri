# Keenable 测试样本 (`internal/provider/keenable/testdata`)

存放 Keenable `/v1/search` 与 `/v1/fetch` 离线契约测试使用的脱敏 JSON 响应样本。

## 目录结构

- `search_ok.json`：Keenable Search API 成功响应脱敏样本（含免费额度内 `usage.paid=false`）。
- `fetch_ok.json`：Keenable Fetch API 成功提取脱敏样本（含重定向 `url`、作者与付费 `usage.paid=true`）。
- `err_403_perm.json`：HTTP 403 `permission_error`（密钥吊销）错误响应样本。
- `err_403_target.json`：HTTP 403 `target_error`（目标站点阻断）错误响应样本。

## 维护规范

- **标准 JSON 格式**：所有 `.json` 样本必须为合法的 RFC 8259 JSON，禁止在文件内写入注释；样本来源与用途统一在本 `README.md` 中记录。
- **彻底脱敏**：禁止包含真实 `keen_` 密钥或账户私有信息。
