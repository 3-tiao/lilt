# Spec: Theme（主题）

沿用 [cliamp](https://github.com/bjarneo/cliamp) 的 TOML 主题 schema，便于复用现有主题生态。

## 位置与命名

- 目录：`~/.config/lilt/themes/`（覆盖变量 `LILT_CONFIG`）。
- 文件名去 `.toml` 即主题名（如 `gruvbox.toml` → `gruvbox`）。
- 选择保存在 `state.json` 的 `theme` 字段；`t` 打开选择器即可预览/切换。
- 自定义文件与内置同名时，自定义优先。

## 格式

八个键，均为 `#RRGGBB`：

```toml
bg = "#282828"          # 可选；省略则沿用终端背景
selection = "#3c3836"   # 可选；选中行背景（lazygit 风格），省略时用 bg
accent = "#7daea3"      # 标题、曲名、进度条、分组标题
bright_fg = "#ebdbb2"   # 主文本、时间
fg = "#928374"          # 次要文本、帮助栏、非活动元素、已播放
green = "#a9b665"       # 活动边框、正在播放高亮、活动标签、成功
yellow = "#d8a657"      # 警告
red = "#ea6962"         # 错误、取消收藏
```

- 缺键或非法值 → 忽略该主题。
- 帮助键 pill 文字在黑白间自动取对比色。
- 关键状态同时用稳定文本标记：`> `、`▶`、`★`、`!`、`WARN:`、`ERR:`，保证单色终端也可读。

## 视觉规则（lazygit / gruvbox）

- 活动面板边框为 `green`，非活动为 `fg`。
- 当前播放曲目使用 `green` 背景高亮（侧栏和歌单详情一致）。
- 光标行使用 `selection` 背景 + `bright_fg`；两者同时出现时光标优先。
- 已播放曲目使用 `fg` 并变暗。
- 未设置主题时默认使用内置 `gruvbox`。

## 内置主题（v2 目标）

至少内置：`gruvbox`（默认）、`default`（终端配色）、`tokyo-night`、`catppuccin`、`nord`、`dracula`。
