# 适配器契约与联调测试套件 (`internal/provider/providertest`)

提供各检索/提取适配器共享的离线测试夹具加载器、响应契约不变量断言及手动真实密钥联调用例。

## 目录结构

- `providertest.go`：标准脱敏样本加载（含 `.json` 严格语法校验）、回环测试 HTTP 客户端构造及检索结果归一化不变量断言。

## 开发规范

- **测试专用隔离**：仅允许被 `*_test.go` 导入，严禁进入任何生产构建产物。
- **默认零网络计费**：真实网络测试必须保留 `//go:build live` 标签，确保常规 `go test ./...` 100% 离线运行。

## 验证

```bash
go test -tags live -c -o /dev/null ./internal/provider/providertest
```
