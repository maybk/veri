# CLI 输出黄金样本 (`internal/cli/testdata`)

存放命令行端到端测试用于逐字节比对的标准输出基线文件。

## 目录结构

- `search.md`：`veri search` 默认 Markdown 输出基线。
- `search.urls`：`veri --format urls` 纯 URL 列表输出基线。

## 维护规范

- **精确换行与空白**：黄金文件的换行符与行尾字符直接参与逐字节断言，禁止随意裁剪尾部换行。
- **零敏感数据**：禁止包含真实 API Key、个人隐私信息或私有地址；仅在有意调整 CLI 渲染版式时同步更新基线。
