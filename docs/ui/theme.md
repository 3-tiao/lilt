# Spec: Theme（主题）

沿用 [cliamp](https://github.com/bjarneo/cliamp) 的 TOML 主题 schema，便于复用现有主题生态。

## 位置与命名

- 当前目录：`~/.config/lilt/themes/`；显式 `LILT_CONFIG` 或 `XDG_CONFIG_HOME` 优先。macOS
  native Application Support 路径与一次性迁移尚未实施，见
  [`../internals/persistence/state.md`](../internals/persistence/state.md#路径) 与 roadmap。
- 文件名去 `.toml` 即主题名（如 `gruvbox.toml` → `gruvbox`）。
- 选择保存在 `state.json` 的 `theme` 字段；`t` 打开选择器即可预览/切换。
- 自定义文件与内置同名时，自定义优先。

## 格式

八个键，使用精确 `#RRGGBB`；内置/兼容主题也可使用 `0`–`255` ANSI 色号：

```toml
bg = "#282828"          # 可选；canvas 背景，省略则完全沿用终端背景
selection = "#3c3836"   # 可选；焦点行背景（lazygit 风格），省略时派生
accent = "#7daea3"      # 标题、曲名、进度条、分组标题
bright_fg = "#ebdbb2"   # 主文本、时间
fg = "#928374"          # 次要文本、帮助栏、非活动元素、已播放
green = "#a9b665"       # 正在播放文字、成功
yellow = "#d8a657"      # 警告
red = "#ea6962"         # 错误、取消收藏
```

- 合法的部分主题（包括未提供 `accent`）会对缺键填充安全默认值（取自内置 `gruvbox`）；任一已提供颜色格式非法则拒绝整个自定义文件，不把任意字符串交给终端渲染。
- `bg` 由 lilt 自己填充到每一个 cell（canvas inset、gutter、面板内部），不依赖终端对 OSC 11 的支持；因此主题的底色在任何终端上都成立。未提供 `bg` 的主题（自定义文件可以）不填任何背景，配色完全交给终端。
- `selection` 缺失且 `bg` 存在时，由 `bright_fg` 朝 `bg` 混合 12% 派生，保证焦点行始终可见；`bg` 也缺失时回退 reverse 视频。
- 关键状态同时用稳定文本标记：`› `（可选列表）、`▶`（当前播放项，主列表与 Up Next 一致）、`★`、`!`、`WARN:`、`ERR:`，保证单色终端也可读。

## 语义 token 映射

主题文件提供的是 palette，不是组件配置。renderer MUST 先从 palette 派生语义 token，再由
Panel、row、Now Playing 等组件消费；组件不得直接为自己选择任意 palette 色。palette 色只以两种
身份出现：文字前景，或作为 `surface.selection` / `surface.background` 的底色。完整组件规则见
[design-system.md](design-system.md)。

| Semantic token | palette 来源 | 用途 |
|---|---|---|
| `surface.background` | `bg` | canvas 与 panel 背景 |
| `surface.selection` | `selection`，缺失时由 `bright_fg` 朝 `bg` 派生（无 `bg` 用 reverse） | 仅 active 面板的键盘焦点 row 底色 |
| `text.primary` | `bright_fg` | 曲名、歌单名、可选 row |
| `text.secondary` / `text.muted` | `fg` | 艺人、Header 数字、history、hint、已播放项 |
| `text.panel-title` / `accent` | `accent` | Header title、section、progress fill |
| `border` | `fg` 朝 `bg` 混合派生 | 所有静态 panel border、scrollbar gutter |
| `state.playing` / `state.success` | `green` | 当前播放文字、成功 |
| `state.warning` | `yellow` | loading、warning |
| `state.error` | `red` | error、destructive action |

`progress.track` 从 `text.muted` 派生，`progress.fill` 从 `accent` 派生。

派生规则只有两条，且都是确定性的（cliamp schema 没有 border/muted 键，不新增第九个键）：

- `border = mix(fg, bg, 50%)`：frame 结构比正文安静，且不随主题的 `fg` 亮度漂移。
- `selection` 缺失时 `= mix(bg, bright_fg, 12%)`：焦点行是 canvas 上的一档抬升。

`text.muted` 就是 `fg` 本身：palette 的 `fg` 已经是主题的低对比色，再叠加终端 faint 会掉到
2:1 以下（tokyo-night 实测 1.7:1），艺人名与已播放项不可读。可读性下限优先于"更暗一档"。

单色终端仍 MUST 通过 glyph、稳定文字和 reverse/background 区分状态。

## 视觉规则

- Panel border 使用派生 `border` 色；焦点、selection 与 playing state 由 row marker/state token
  表达，不把整框或整行染成高饱和色。
- 当前播放曲目：`▶` + `green` 文字，不加底色。不用 palette 的 `green` 做整行背景填充——那个色是
  各主题的高饱和信号色，做底色会盖过主题自身的视觉语言（见 [`design-system.md`](design-system.md) §6）。
- 光标行使用 `selection` 背景 + `bright_fg`，且只在 active 面板出现；播放状态与焦点各占一个通道：
  播放由 `green` 文字与 `▶` 表达，光标由 accent 色的 `›` 与底色表达，两者互不覆盖（见
  [`ux.md`](ux.md)）。光标 marker 必须带 accent token：裸文字继承终端前景色，会在被主题填充的
  canvas 上消失。
- 已播放曲目使用 `fg`；需要同时显示光标时改用 `bright_fg` + 底色，已播仍由 `·` 表达。

- 未设置主题时默认使用内置 `gruvbox`。曾经还有一个跟随终端配色的 ANSI 16 色 `default` 主题，已移除：
  它的对比度完全取决于用户终端配色，不可控也不可测；持久化状态里残留的 `"default"` 或空值在
  加载时一次性解析为 `gruvbox`（见 [`../internals/persistence/state.md`](../internals/persistence/state.md#路径)）。lilt 不读取 shell 配置文件。

## 内置主题

内置主题使用各自上游的官方色值，不自行“美化”：

| 内置名 | 来源 |
|---|---|
| `gruvbox` | Gruvbox Dark；也是缺省 palette：`theme` 为空/`default` 时解析到它，部分自定义主题的缺键也从它填充 |
| `gruvbox` | Gruvbox Dark |
| `tokyo-night` | Tokyo Night |
| `catppuccin` | Catppuccin Mocha |
| `nord` | Nord |
| `dracula` | Dracula |
| `ayu-mirage` | Ayu Mirage |
| `rose-pine` | Rosé Pine（main） |
| `everforest` | Everforest Dark Medium |
| `broadcast-night`、`print-room`、`signal-red` | lilt 原创（广播控台风格） |

两个硬约束，二者都有 hermetic 测试：

1. **每个文字色与其自身 `bg` 的对比度 ≥ 4:1**（`TestBuiltinPalettesStayReadable`）。上游用作“注释/次要文本”
   的颜色往往低于这个下限（tokyo-night 的 `#565f89` 只有 2.76:1），此时向该主题自己的前景色混合到过线为止，
   保持主题辨识度而不牺牲可读性。
2. **`green` 必须是“正在播放”能读的颜色，不能是第二个 `red`**。广播系列原本把 `green` 当作红底高亮块用；
   现在它只画文字，所以改为控台“on air”绿。
