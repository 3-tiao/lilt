## 概要 / Summary

<!-- 这个 PR 做了什么，为什么。 -->

## 验证 / Verification

- [ ] `just verify` 通过
- [ ] 涉及 provider 时 `just provider-gate` 通过

## Provider 变更准入 / Provider admission

> 仅当本 PR 新增、替换或实质修改一个内容来源（source/provider）时填写。准入规则见
> [`docs/testing/provider-admission.md`](https://github.com/3-tiao/lilt/blob/main/docs/testing/provider-admission.md)。

- [ ] 已阅读并满足 `docs/testing/provider-admission.md` 的结构与语义准入条件
- [ ] `sources.list`、`authorization.list`、capabilities 三者在公开 API 上一致
- [ ] 已从 `internal/server/provider_gate_test.go` 的 `reservedButUnimplemented` 移除本 source
- [ ] fixture 覆盖 search/library 形状、canonical ref、`invalid_reference` / `source_mismatch`
- [ ] fixture 覆盖 401 / 403 / 404 / 429 / 5xx / 超时 / 取消的错误映射
- [ ] 授权：begin / completed / failed / cancel / disconnect 幂等 / token 失效
- [ ] 播放互斥、有限队列不变量、失败不污染 recent / favorites / state
- [ ] 不支持的 capability 返回 `unsupported_command`，不静默降级
- [ ] 无账号、token、authorization code、PKCE verifier、用户数据或真实 API 响应进入仓库 / fixture / 日志
- [ ] hermetic 测试不访问网络
- [ ] provider 标为 experimental，或已附真实账号验收记录

### 真实账号验收（stable 前必填）/ Real-account acceptance

- 验收人：
- 日期：
- 平台 / 系统版本：
- provider API 版本：
- 已验证：授权 / 搜索 / 播放 / pause·resume·next·previous / disconnect
- 脱敏证据（可粘贴命令与结果，不含 token / 用户数据）：
