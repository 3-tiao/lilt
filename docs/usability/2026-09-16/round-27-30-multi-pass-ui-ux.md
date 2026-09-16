# Rounds 27–30 · Multi-pass UI/UX assessment

- 日期：2026-09-16
- 规则：本批只测不改；所有候选项留待与用户共同决定
- 范围：新手 Radio 搜索与收藏、Apple Music 队列编辑、80×18 紧凑布局、真实 Apple Music 内容与 Help

## 总结

核心任务均完成，没有阻塞性 UI/UX 问题。当前界面在正常宽度下已有清晰的 Source/View 层级、稳定的播放 dock、明确的选中反馈和可恢复的 Radio 搜索。建议只讨论下面 2 个高价值候选项；其余保持不动。

## 各轮结果

### Round 27 · 新手找 Jazz 电台

**路径**：Apple Home → Tab → Radio Browse → `/` → 输入 `jazz` → Enter → Enter → 播放 → 收藏。

- 成功：搜索确认按钮在输入后成为明确的 `[ Search "jazz" ]`；结果标题 `SHOWING: JAZZ (92)` 明确确认条件已生效。
- 成功：播放、星标与 `Favorited` toast 连续反馈清楚。
- 视觉：搜索结果信息密度较高，但标题、探测符号和次级元数据已可区分。

### Round 28 · Apple Music 日常队列

**路径**：Playlists → detail → Enter 播放 → `0` → `j` → `K` 重排。

- 成功：播放后主列表不跳动；宽屏 dock 左侧播放、右侧 Up Next 的关系易理解。
- 成功：队列焦点出现 `>`，重排后 toast 与队列顺序一致。
- 视觉：播放摘要、队列标题、footer 形成稳定的三层反馈。

### Round 29 · 80×18 紧凑窗口

**路径**：Apple 播放 → `0` 聚焦队列。

- 成功：双栏自动回退为单栏；队列获得焦点后安全地占用主区域；没有溢出或截断边框。
- 发现：队列已聚焦时 dock 仍写 `Up Next · 1 of 2 · 0 focus`，而 `0` 实际是“返回”。文案与状态不一致。

### Round 30 · 真实 Apple 内容与 Help

**路径**：真实 Apple Music Home（只读，不播放）→ Help。

- 成功：中英文、超长英文歌名、中文歌单均不溢出；Home 的分组与类型 glyph 易扫读。
- 成功：Help 的 Navigation / Playback / Up Next / Library 分组清楚，`Esc close` 也没有退出歧义。
- 观察：未播放时固定高度 dock 留白较大；这是稳定布局的代价，不是错误。

## 候选改动（按优先级）

### 1. 建议改 · 紧凑队列的 `0 focus` 动态文案

**证据**：Round 29。聚焦后实际按 `0` 会返回，但 dock 仍提示 `0 focus`。

**方案**：未聚焦时保留 `0 focus`；已聚焦时改为 `0 back`，或隐藏整条 Up Next 摘要。

**收益**：低风险地消除一次明显的状态/动作冲突。

### 2. 需要共同决定 · 空闲 Now Playing dock 的内容

**证据**：Round 30。真实 Home 的固定 dock 只有 `Nothing playing`，下方有约 5 行留白。

**不可变约束**：不能缩短 dock；播放前后布局不能跳动。

**选项**：

1. **保持现状（推荐）**：留白与终端音乐播放器的安静感一致；footer 已提供下一步。
2. **加入一句弱提示**：`Choose a song or station — Enter plays`；更易上手，但会略显重复。
3. **显示最近播放摘要**：信息更丰富，但会让状态 dock 变成第二个内容列表。

### 3. 暂不改 · live stream 的 `—` 格式回退

fake player 的 LIVE dock 显示 `LIVE · playing · —`；真实 helper 通常回报 `live stream`。在真实 Radio stream 再出现空格式前，不应为测试 fixture 改产品文案。

## 保持不动的设计

- Radio 行保留完整格式、国家与标签：它们是比较电台的有效信息；当前已是次级对比度，长文本安全省略。
- 宽屏双栏 / 窄屏单栏队列回退。
- 固定高度的播放 dock。

## 评审后的决定

1. **已实施**：窄屏队列获得焦点时，dock 的 `0 focus` 动态改为 `0 back`；未聚焦时仍为 `0 focus`。
2. **保持现状**：空闲 Now Playing dock 保留留白，不加入第二套行动提示或内容列表。
