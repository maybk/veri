# GitHub 提取器测试样本 (`internal/provider/github/testdata`)

存放 GitHub REST API 结构化读取器契约测试使用的脱敏 JSON 响应样本。

## 目录结构

- `issue_ok.json`：GitHub REST API Issues 详情脱敏样本。
- `comments_empty.json`：GitHub REST API Issue 评论空列表脱敏样本。
- `error.json`：GitHub REST API 匿名配额耗尽（`API rate limit exceeded`）错误响应脱敏样本。

## 维护规范

- **标准 JSON 格式**：所有 `.json` 样本必须为合法的 RFC 8259 JSON，禁止在文件内写入注释；样本来源与用途统一在本 `README.md` 中记录。
- **彻底脱敏**：禁止包含真实 GitHub PAT、私有仓库地址或用户敏感信息。
