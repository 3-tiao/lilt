---
name: architecture-audit
description: 对 AI-generated、vibe-coded 或长期快速迭代的软件项目执行独立工程审计。用于重新构建业务目标、检验设计决策本身是否必要，再检查实现一致性、架构边界、重复和意外复杂度、AI 生成代码异味、遗留/僵尸代码、变更放大及测试可信度。只调查和报告，不修改代码；适用于“architecture audit”“engineering audit”“审计项目整体结构”“检查设计或实现是否过度复杂”等请求，不用于普通逐行 code review 或直接重构。
---

# Independent Engineering Audit

> 暂时忘掉这套代码是怎么写出来的，重新理解这个系统，然后判断它现在是否仍然是一个合理的工程。

本 skill 是独立审计角色，不是当前实现的辩护者，也不是重构执行者。审计关系是 **业务目标 → 设计决策 → 实现**：实现与设计一致，只能证明执行一致，不能证明设计合理。

## 不可协商的约束

1. **只读**：不得编辑代码、文档、配置或测试；不得提交、推送、自动修复或创建重构分支。默认在对话中输出报告；只有用户明确要求时才写报告文件，且不写入被审计仓库。
2. **证据先于建议**：没有仓库证据，不得产生 finding；没有完成 Minimum Necessary Model 和 Current State Model，不得提出架构方案。
3. **设计文档不是业务真相**：README、ADR 和架构规范中的机制、状态或模式都是待验证主张；必须追溯到用户结果、外部契约或硬约束。
4. **不信任惯性或模式**：不要因为 abstraction 已存在就保留它，也不得仅凭代码风格或重复片段推荐 Clean Architecture、Repository、DI、事件总线、微服务等模式。
5. **分离事实、推断与证据来源**：明确标注 Observed、Expected、Inference、Unknown；识别证据 lineage，业务意图不清时报告矛盾，不替产品做决定。

## 调用方式与范围

默认执行 `full` 审计。用户可以指定一个重点：

```text
/skill:architecture-audit full
/skill:architecture-audit business
/skill:architecture-audit design
/skill:architecture-audit architecture
/skill:architecture-audit duplication
/skill:architecture-audit ai-smells
/skill:architecture-audit dead-code
/skill:architecture-audit maintainability
/skill:architecture-audit tests
```

也可附带目录、模块或基线，例如“只审计 `internal/server`”或“审计自 `<git-ref>` 以来的架构漂移”。重点审计仍须建立足以支撑结论的局部 Current State Model。

开始时记录：

- 仓库、当前分支/commit、工作树状态和审计范围。
- 用户指定的业务目标、权威文档和排除项。
- 本次读取到的证据类型，以及无法访问的证据。
- 若工作树有未提交改动，明确报告审计对象是当前工作树，而非仅 `HEAD`。

除非缺少范围会让审计无法开始，否则不要先向用户索取完整背景；使用仓库证据继续，并把假设写进报告。

## Phase 1 — Forensics

这一阶段只回答“系统实际上是什么”，不评价好坏，不提出解决方案。

### 1. 建立证据地图与来源谱系

先区分证据家族，而不是简单按文档权威性排序：

1. **Business outcomes**：用户可观察结果、真实业务操作和明确产品目标。
2. **Hard constraints**：外部 API/协议、数据完整性、安全、法律、平台和已测量的性能限制。
3. **Design claims**：README、ADR、架构规范中的状态、模式、抽象和机制。
4. **Implementation**：可执行代码、调用路径、依赖关系、配置和持久化格式。
5. **Derived evidence**：测试、fixture、示例和注释；判断它们是独立验证，还是仅复制同一设计。

文档与实现冲突时，把冲突本身作为证据。文档与实现一致时，也不能自动确认设计正确：若 README 定义方案、代码实现方案、测试复述方案，三者通常属于同一个 evidence lineage，而不是三份独立证据。

主动识别 **solution-shaped requirements**：如果“需求”直接指定 Manager、Repository、事件总线、多阶段状态机、兼容层或固定模式，先将其视为设计决策，再追问哪个外部结果或硬约束迫使它存在。

### 2. 重建 Minimum Necessary Model

在接受内部设计词汇前，只根据以下证据建立满足系统目标所需的最小模型：

- 用户必须获得的可观察结果。
- 系统必须遵守的外部契约。
- 必须维护的数据与安全不变量。
- 无法消除的失败、并发和恢复场景。
- 有证据的性能、平台或合规边界。

不要在此阶段继承当前类名、层级、状态枚举或 abstraction。输出最小必要的概念、状态、规则和边界，并标注哪些仍是 `Unknown`。

### 3. 重建 Current State Model

产出以下模型，每项都附关键 `path:line`：

- **System purpose**：用户能做什么，系统对外承诺什么。
- **Domain concepts**：核心实体、状态、身份、生命周期及彼此关系。
- **Business rules**：允许/禁止的行为、状态转换、错误语义和不变量。
- **Execution paths**：选择 3–5 条最关键流程，从入口追踪到 domain、I/O、持久化和返回结果。
- **Ownership map**：每个业务概念、状态和规则由谁拥有，谁只是投影或消费者。

对每条业务规则标注来源：`Declared`、`Enforced`、`Tested`、`Inferred` 或 `Contradicted`。同一规则可有多个标记，但来自同一 lineage 的标记不能冒充独立佐证。

### 4. 建立结构与变化地图

记录：

- 模块边界和实际 dependency direction，而不是目录声称的方向。
- 业务逻辑、networking、persistence、UI/transport glue 的真实位置。
- sources of truth、缓存、投影及同步机制。
- 重复概念、重复流程、旧/新实现并存和 workaround 链。
- 代表性小需求需要触及的文件、层和 switch/registry 数量。

完成后分别形成中性的 **Minimum Necessary Model** 与 **Current State Model**。在内部确认两者足以解释关键流程后，才能进入 Phase 2。

## Phase 2 — Audit

对 Phase 1 的两个模型应用以下七个审计维度。测试可信度是贯穿所有维度的交叉证据，不以“测试绿色”替代业务正确性。

### N — Design Necessity / Premise Audit

逐项审计重要状态、模式、抽象、配置和中间层本身是否必要：

- **Claimed requirement**：文档声称它解决什么问题；区分业务目标、产品策略和设计机制。
- **External constraint**：哪个用户结果、外部契约或硬约束迫使它存在；找不到时写 `Not found`。
- **Counterfactual**：如果删除、合并或简化它，具体会破坏什么可观察行为或 invariant。
- **Complexity cost**：它增加多少状态、分支、配置、同步责任和 change amplification。
- **Current validity**：最初理由现在是否仍成立，还是已经成为历史 workaround。

每个被审计决策给出 `Essential`、`Justified trade-off`、`Accidental complexity`、`Unsupported` 或 `Unknown`。不能因为文档、代码和测试互相一致就判定 `Essential`。

### B — Business ↔ Implementation

检查实际行为是否符合声明或可合理重建的业务规则：

- 同一业务规则是否在 API、UI、后台任务和持久化中语义一致。
- 删除、权限、可见性、身份、排序、幂等和失败语义是否完整闭环。
- 实现是否只满足局部 happy path，却破坏端到端结果。
- 测试是否验证业务结果，而不只是当前实现细节。
- 无法确定产品意图时，是否存在需要产品决策的明确分叉。

### A — Architecture / Boundaries

检查：

- dependency direction 是否与所有权一致，有无反向依赖或环。
- domain logic 是否散落在 UI、handler、network 或 persistence 层。
- 同一业务概念是否有多个模型、管理器或 source of truth。
- god object、隐式全局状态和跨层旁路是否扩大影响面。
- abstraction 是否有清晰职责、调用者和必须存在的理由。

### D — Duplication / Accidental Complexity

先判断重复是否代表**同一个概念**，再讨论抽象：

- 同一规则或流程被多次实现，且会一起变化：通常是 accidental duplication。
- 名字不同但输入、状态转换和副作用高度重合：追踪到共同业务含义。
- 不同信任边界或不同失败责任中的相似校验：可能是 intentional duplication。
- 只有语法相似、生命周期不同的代码：不得仅为 DRY 合并。
- 每个抽象建议必须说明会减少哪一种变化成本，以及不会错误耦合什么。

### V — AI-generated Code Smells

主动寻找 vibe coding 常见的结构性信号：

- **Patch-on-patch**：guard、special case、fallback 按时间堆叠，但状态模型未被重新审视。
- **Defensive inflation**：错误被 `try?`、空 catch、默认值或早退吞掉，invariant 因而不可见。
- **Workaround persistence**：临时旁路、兼容分支或 feature flag 已变成永久路径。
- **Abstraction drift**：接口、manager、helper 或 wrapper 存在，但职责与名称已经偏移。
- **Local-success bias**：每个 feature 局部可用，跨 feature 的身份、状态或错误语义却不一致。

代码较长、分支较多或使用某种模式，本身不是 finding；必须说明它如何掩盖状态、增加歧义或放大变化。

### L — Dead / Legacy / Zombie Code

候选项必须分级，不可因“看起来没用”就建议删除：

- **Confirmed dead**：无可达入口、无反射/注册/生成引用，且构建与配置中均无使用证据。
- **Likely dead**：静态路径不可达，但仍存在动态加载、外部调用或部署配置的不确定性。
- **Potentially dead**：旧 API、旧 abstraction、永久开关、迁移遗留或重复实现，需要进一步验证。
- **Unknown**：证据不足；列出缺失证据，不提出删除建议。

检查代码引用之外，也检查配置、脚本、生成器、序列化兼容、插件注册和外部入口。删除方向必须说明验证方法。

### M — Maintainability / Change Amplification

选择 2–4 个现实的小变化场景，追踪其修改面，例如新增类型、改变一条业务规则、替换 provider 或增加状态：

- 需要改多少文件、层、枚举、switch、registry、fixture 和文档。
- 修改点是同一责任的合理展开，还是多个并行 source of truth。
- 是否必须依赖隐式顺序、人工同步或遗漏即静默失败的注册。
- 局部变化是否迫使无关模块重新理解或重新测试。
- 测试是否能在变化遗漏时失败，而不是继续绿色。

不要使用固定文件数阈值判定好坏；解释变化为何被放大，以及放大来自哪条边界或模型。

## 测试可信度检查

对每个高影响 finding 检查测试证据：

- 测试覆盖的是业务结果、契约还是实现细节。
- 关键 invariant 是否有失败路径和边界条件。
- mock 是否绕过了最可能出错的真实协作边界。
- fixture 是否已经固化错误语义或过时 schema。
- 测试缺口是否会让已识别的偏差继续保持绿色。

默认只读取测试。仅当运行测试能验证关键事实且命令安全、范围明确时才运行；不得以修改测试来证明 finding。

## Finding 证据标准

每条 finding 必须包含至少一个直接仓库证据。证据强度：

- **Direct**：代码、schema、配置或可复现行为直接证明。
- **Corroborated**：两个以上独立 evidence family 互相支持。
- **Inferred**：由调用关系或缺失路径推断，仍有未验证假设。
- **Unknown**：证据不足，只能列为待调查问题，不能列为确定 finding。

每条 finding 同时记录 `Evidence count` 与 `Independent evidence families`。README → 实现 → 基于同一 README 编写的测试通常只算一个 lineage；不能用数量制造虚假的高置信度。

置信度使用 `High / Medium / Low`。严重度与置信度分开：高严重度、低置信度是需要优先验证，不是已经证实的灾难。

严重度：

- **Critical**：核心业务语义、数据完整性、安全边界或不可逆状态存在已证实破坏。
- **High**：常见变化或主路径很可能造成错误、分叉或系统性维护风险。
- **Medium**：局部设计导致重复、理解成本或可预见的修改风险，但有明确边界。
- **Low**：影响较小的清理、命名或局部一致性问题，不阻塞系统演进。

## Finding 格式

```markdown
### A-01 — <具体问题，不写抽象口号>

Severity: High
Confidence: High
Evidence strength: Corroborated

Observed:
<系统现在实际做什么>

Expected:
<声明的业务规则、架构责任或需要确认的目标>

Evidence:
- path/to/file.ext:123 — <该证据证明什么>
- path/to/other.ext:45 — <交叉证据>

Impact:
<当前业务后果或具体的 change amplification>

Recommended direction:
<方向与约束，不给未经验证的大型重构蓝图>

Do NOT:
<最诱人但会继续掩盖问题的补丁或错误抽象>

Verification:
<如何证实问题已解决；优先写业务断言>
```

设计必要性 finding 还必须增加：

```markdown
Claimed requirement:
<文档声称为什么需要该设计>

External constraint:
<独立于该设计的业务结果或硬约束；没有则写 Not found>

Counterfactual:
<删除、合并或简化后会失去什么>

Complexity cost:
<新增的状态、分支、配置、同步责任和修改面>

Verdict:
Essential / Justified trade-off / Accidental complexity / Unsupported / Unknown
```

ID 前缀使用 `N`、`B`、`A`、`D`、`V`、`L`、`M`；测试缺口附着于对应 finding。纯测试架构问题可用 `T`。

## 最终报告结构

```markdown
# Engineering Audit

## Scope and Snapshot
<仓库、commit/worktree、范围、证据限制>

## Executive Summary
Health:
- Business correctness: ✅ / ⚠️ / ❌ / Unknown
- Design necessity: ✅ / ⚠️ / ❌ / Unknown
- Architecture: ✅ / ⚠️ / ❌ / Unknown
- Maintainability: ✅ / ⚠️ / ❌ / Unknown
- Duplication: ✅ / ⚠️ / ❌ / Unknown
- Legacy code: ✅ / ⚠️ / ❌ / Unknown
- Test confidence: ✅ / ⚠️ / ❌ / Unknown

Critical: N | High: N | Medium: N | Low: N
Top priorities: <最多 5 项>

## Minimum Necessary Model
<仅由业务结果、外部契约与硬约束推导出的最小模型>

## Current State Model
<当前设计和实现的中性系统模型、关键流程、ownership 与 sources of truth>

## Critical and High Findings
<按严重度，再按业务影响排序>

## Findings by Category
### Design Necessity / Business / Architecture / Duplication / AI Smells / Legacy / Maintainability
<完整 findings；同组超过 5 项时再按子系统分组>

## Dead-Code Candidates
<Confirmed / Likely / Potential / Unknown，附验证方式>

## Positive Findings
<具体说明哪些边界、模型、测试或删除策略工作良好，以及证据>

## Unknowns and Decision Points
<缺失证据、相互矛盾的业务意图、需要人决定的分叉>

## Recommended Order
<只给调查或重构顺序，不执行修改；最多 5 个阶段>
```

Positive Findings 必须保留，但只能写有证据的工程优势，不能用泛泛表扬平衡负面数量。

## 审计纪律

- 不把 lint、命名偏好、文件长度或“不是我喜欢的架构”升级为架构 finding。
- 不把 solution-shaped requirement 当成不可质疑的业务目标；先追溯其外部必要性。
- 不因文档、实现和测试一致就判定设计正确，也不因测试少就假定实现错误。
- 不把所有重复都合并，不把所有旧代码都删除，不把所有 manager 都重写。
- 不在报告后顺手修复，也不声称超出审计边界的“全仓库无问题”。实施必须成为新的明确任务。

完成标准：读者能从报告中区分**系统事实、业务偏差、工程风险、证据强度、未知项和建议方向**，且无需相信审计者的架构偏好。
