# 腾讯云联网搜索测试样本 (`internal/provider/tencentwsa/testdata`)

存放腾讯云联网搜索 SearchPro 接口契约测试使用的脱敏 JSON 响应样本。

## 目录结构

- `search_ok.json`：SearchPro 成功响应脱敏样本（覆盖 `Response.Pages` 字符串数组二次 JSON 反序列化、`score` 与旗舰版 `authority_level`）。
- `search_empty.json`：SearchPro 空结果响应脱敏样本。
- `error.json`：SearchPro 业务错误（HTTP `200` 内嵌 `Response.Error.Code = "ResourceNotFound"`）脱敏样本。

## 维护规范

- **标准 JSON 格式**：所有 `.json` 样本必须为合法的 RFC 8259 JSON，禁止在文件内写入注释；样本来源与用途统一在本 `README.md` 中记录。
- **彻底脱敏**：禁止包含真实云账号凭据或非公开请求标识。
