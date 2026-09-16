# Spec: Theme（主题）

沿用 [cliamp](https://github.com/bjarneo/cliamp) 的 TOML 主题 schema，便于复用现有主题生态。

## 位置与命名

- 目录：`~/.config/lilt/themes/`（目录覆盖变量 `LILT_CONFIG`；若该变量指向已存在普通文件，则按旧版预设文件兼容语义处理，主题仍使用默认目录）。
- 文件名去 `.toml` 即主题名（如 `gruvbox.toml` → `gruvbox`）。
- 选择保存在 `state.json` 的 `theme` 字段；`t` 打开选择器即可预览/切换。
- 自定义文件与内置同名时，自定义优先。

## 格式

八个键，使用精确 `#RRGGBB`；内置/兼容主题也可使用 `0`–`255` ANSI 色号：

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

- 合法的部分主题（包括未提供 `accent`）会对缺键填充安全默认值；任一已提供颜色格式非法则拒绝整个自定义文件，不把任意字符串交给终端渲染。
- 彩色活动标签文字根据背景亮度在黑/白间自动取可读对比色（ANSI 背景使用配置的安全 fallback）。
- 关键状态同时用稳定文本标记：`> `、`▶`、`★`、`!`、`WARN:`、`ERR:`，保证单色终端也可读。

## 视觉规则（lazygit 风格）

- 活动面板边框为 `green`，非活动为 `fg`。
- 当前播放曲目使用 `green` 背景高亮（侧栏和歌单详情一致）。
- 光标行使用 `selection` 背景 + `bright_fg`；两者同时出现时光标优先。
- 已播放曲目使用 `fg` 并变暗。
- 未设置主题时默认使用内置 `default`：只输出 ANSI 16 色，由 Terminal/iTerm/Kitty 的当前主题决定配色；lilt 不读取 shell 配置文件。

## 内置主题（v2 目标）

至少内置：`default`（默认，终端配色）、`gruvbox`、`tokyo-night`、`catppuccin`、`nord`、`dracula`。
