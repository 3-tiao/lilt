# Spec: Theme（主题）

沿用 [cliamp](https://github.com/bjarneo/cliamp) 的 TOML 主题 schema，便于复用现有主题生态。

## 位置与命名

- 目录：`~/.config/lilt/themes/`（覆盖变量 `LILT_CONFIG`）。
- 文件名去 `.toml` 即主题名（如 `gruvbox.toml` → `gruvbox`）。
- 选择保存在 `state.json` 的 `theme` 字段；`t` 打开选择器即可预览/切换。
- 自定义文件与内置同名时，自定义优先。

## 格式

七个键，均为 `#RRGGBB`：

```toml
bg = "#282828"          # 可选；省略则沿用终端背景
accent = "#7daea3"      # 标题、曲名、进度条、选中项
bright_fg = "#d4be98"   # 主文本、时间
fg = "#a89984"          # 次要文本、帮助栏、非活动元素
green = "#a9b665"       # 播放中、成功、收藏
yellow = "#d8a657"      # 警告
red = "#ea6962"         # 错误、取消收藏
```

- 缺键或非法值 → 忽略该主题。
- 帮助键 pill 文字在黑白间自动取对比色。
- 关键状态同时用稳定文本标记：`> `、`▶`、`★`、`!`、`WARN:`、`ERR:`，保证单色终端也可读。

## 内置主题（v2 目标）

至少内置：`default`（终端配色）、`gruvbox`、`tokyo-night`、`catppuccin`、`nord`、`dracula`。
