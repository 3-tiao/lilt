# 测试

本目录放 lilt 的测试分层、隔离边界与 provider 准入标准。

| 文档 | 内容 |
|---|---|
| [`integration.md`](integration.md) | 真实 E2E 与确定性 contract 测试设计、预发布/手动测试隔离、真实会话排障 |
| [`provider-admission.md`](provider-admission.md) | 新增 provider 的准入条件与门禁边界 |

相关命令：`just test`、`just verify`、`just provider-gate`、`just workflow-check`、
`just manual-test`（见 [`../../AGENTS.md`](../../AGENTS.md)）。
