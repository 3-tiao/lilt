# lilt

[English](README.md) | 简体中文

在终端（macOS / Linux）里播放 **Apple Music、Audius、Jamendo 与网络电台**。可以用 TUI 手动操作，
也可以用 CLI / JSON 写脚本，或让 AI agent 用一句自然语言控制。

```sh
lilt play apple-music:song:1440845629   # 直接播一首 Apple Music
lilt search "Nujabes" --play            # 打开 TUI，搜索并播放
lilt radio search --tag lofi            # 搜网络电台
lilt status --json                      # 当前播放状态（给脚本 / agent）
```

## 为什么做这个

- **Apple Music 没有官方 CLI / TUI**，也没有 Linux 客户端；终端党想在终端里听 Apple Music。
- **音乐被分散在各个 App**：想要一个可脚本、可 `--json`、能被自动化调用的统一入口，
  而不是再装一个 GUI。
- **想让 AI agent 直接控制播放**：agent 应当是官方入口，而不是靠脚本去凑。

## 怎么用

### TUI（手动操作）

```sh
lilt tui
```

一次典型操作：`s` 选来源 → `/` 搜 “Nujabes” → `Enter` 播放 → `e` 把下一首加入队列 →
`0` 打开 Up Next 编辑（`x` 删除、`J`/`K` 排序）→ `f` 收藏 → `?` 看全部快捷键 → `q` 退出。
**退出 TUI 不会停止播放**——播放由常驻服务持有，重开界面就能接着控制。

### CLI（脚本与自动化）

脚本使用 `--json` 获取结构化输出，只解析 `ok` / `error.code`；`search --play` 会打开 TUI：

```sh
$ lilt search "Nujabes" --type song --json
{"ok":true,"requestId":"...","data":{ ... }}   # 具体字段以 lilt api --json 为准

$ lilt play apple-music:song:1440845629 --json
$ lilt queue add audius:song:123 --next --json
$ lilt pause --json && lilt next --json
```

人类可读用法是 `lilt help`；机器可读的完整命令清单是 `lilt api --json`（不连服务也能用）。

### AI agent（自然语言）

安装随包发布的 skill 后，可以直接说：

> 放点适合写代码的歌 / 用 Audius 播放 XXX / 放个爵士电台 / 下一首 / 停一下

agent 会读 `lilt sources --json` 里的能力，按优先级选来源并播放，再用 `lilt status --json`
汇报播了什么、为什么。接入方式见 [`docs/guides/agent.md`](docs/guides/agent.md)。

### 常见任务速查

| 我想… | TUI | CLI |
|---|---|---|
| 搜歌并播放 | `/` 搜索 → `Enter` | `lilt search "<词>" --play`（打开 TUI） |
| 播一个对象 | `Enter` / `p` | `lilt play <source:kind:id>` |
| 听网络电台 | `s` → Radio → Browse | `lilt radio search --tag lofi` |
| 看 / 编辑队列 | `0` | `lilt queue` · `queue add <ref> --next` |
| 收藏 | `f` | `lilt favorite add <ref>` |
| 暂停 / 下一首 / 停止 | `Space` / `n` / `v` | `lilt pause` / `lilt next` / `lilt stop` |
| 看当前状态 | Now Playing 条 | `lilt status --queue --json` |
| 换主题 | `t` | 主题文件放在 `~/.config/lilt/themes/` |

更细的按键、布局与反馈见 [`docs/guides/tui.md`](docs/guides/tui.md)，命令逐条规格见
[`docs/client-api/commands.md`](docs/client-api/commands.md)。

## 支持的内容来源

lilt 支持 **Apple Music、Audius、Jamendo 与网络电台**（含内置精选台）。每个来源提供的能力、前置
条件与平台差异见 [`docs/product/roadmap.md`](docs/product/roadmap.md) 的「支持的来源（服务）」；
实际可用性以 `lilt sources --json` 为准——每个来源声明自己支持哪些能力，不支持的操作会明确报错，
不会悄悄忽略。

平台差异：macOS 原生支持全部来源；Linux 上 Apple Music 走浏览器引擎，可搜歌、试听、全曲与推荐，
但**没有资料库 / 个人歌单**，Radio、Audius、Jamendo 则由 mpv 播放。

## 与其他终端音乐方案的区别

“聚合多个来源”并不是 lilt 的差异点——不少终端播放器（如 cliamp）聚合得更多。lilt 的取舍是：
**把 Apple Music 接进终端**，并让 AI agent 成为官方控制路径，同时保持零本地曲库、一切可脚本化。

| 工具 | 音源 | 平台 | 官方 AI-agent 入口 | 许可 |
|---|---|---|---|---|
| **lilt** | Apple Music + Audius + Jamendo + 网络电台（无本地曲库） | macOS、Linux | **有**（Client API + skill） | MIT |
| cliamp | 本地文件 + YouTube(Music)/SoundCloud/Spotify/NetEase/Yandex/Bilibili/Mixcloud + 电台 + 播客 + 自建服务器 | Linux/macOS/Windows | 无（有 Lua 插件系统） | MIT |
| cmus | 本地文件 + 流 | Unix-like | 无 | GPL-2.0 |
| MPD（+ ncmpcpp/mpc） | 本地文件 + 流，socket 协议 | Unix-like | 无 | GPL-2.0 |
| ncspot | Spotify（需 Premium） | Linux/macOS/Windows/*BSD | 无 | BSD-2-Clause |
| spotify-tui | Spotify Web API | macOS/Linux/Windows | 无 | MIT（社区反馈维护停滞） |
| termusic | 本地文件 + yt-dlp 下载 | macOS/Linux | 无 | MIT（部分模块 GPLv3） |
| yewtube | YouTube（mpv/VLC） | Linux/macOS/Windows | 无 | GPL-3.0 |

- **Apple Music**：上表（以及我调研到的常见终端播放器）里没有其他方案支持 Apple Music。
- **AI-agent 一等入口**：随包提供 Client API 与 skill，可以用自然语言控制；其他工具即使有插件
  系统，也不是面向 agent 的控制路径。
- **零本地曲库、可脚本**：lilt 不扫描本地文件，也不做 EQ / 频谱 / 歌词；它专注在线来源，
  并保证每个操作都有 `--json`。

脚注来源：[cliamp](https://github.com/bjarneo/cliamp)、[cmus](https://github.com/cmus/cmus/releases)、
[MPD](https://www.musicpd.org/news/2025/07/mpd-0-24-5-released/)、
[ncspot](https://github.com/hrkfdn/ncspot/releases)、
[spotify-tui](https://github.com/Rigellute/spotify-tui/issues/1043)、
[termusic](https://crates.io/crates/termusic)、[yewtube](https://pypi.org/project/yewtube/)。

## 安装与快速开始

`v1.0.0` 是准备公开安装的首版，但尚未发布签名公证的 Release：当前仍从源码构建。
发布后的 Homebrew 与 `sh` 安装方式、平台要求见
[`安装指南`](docs/getting-started/install.md)；发布流程见 [`release.md`](docs/product/release.md)。

```sh
# 源码构建（macOS 需要 Xcode 与 Apple Developer Team；Linux 只构建 Go）
just build          # Go CLI/TUI + macOS 上的两个签名 helper
./lilt version
```

完整的平台要求、Linux/Nix 用法、外部依赖、数据路径与环境变量见
[`docs/getting-started/install.md`](docs/getting-started/install.md)；第一次授权与播放见
[`docs/getting-started/first-playback.md`](docs/getting-started/first-playback.md)。

## 本地、隐私与信任

- lilt 是**本地播放器 / 客户端**：没有云服务、没有遥测；偏好、收藏与播放历史都存在本机，
  凭据只放操作系统安全存储（macOS Keychain）。网络请求只发生在你实际使用的来源上。
- 随包发布的 **agent skill 会安装进你的 agent 环境**：它只是一份说明（触发、策略、配方），
  通过 `lilt` CLI 工作，不读取本地存储。使用前请自行审查
  [`skills/music-control/SKILL.md`](skills/music-control/SKILL.md)。

## 已知限制（摘要）

- 试听时长由上游决定：macOS 原生约 30 秒，浏览器引擎（Linux，或 macOS 的
  `LILT_APPLE_ENGINE=browser`）实测约 90 秒。完整播放需要有效订阅。
- Apple 的个性化接口（For You、云端最近播放）在本机失败，已接受；`lilt recent` 是 lilt 本地
  历史，不是 Apple 的“最近播放”。
- 收藏是 lilt 本地列表，不写 Apple Music。
- 不做 seek / 音量 / 实时码率 / 频谱；不做本地文件、播客、歌词。
- 不创建或编辑 Apple Music 资料库歌单。
- Linux 的 Apple Music 不提供资料库 / 个人歌单 / 目录电台，也不支持 shuffle / repeat。
- Jamendo 需用户自备 `client_id`，且仅限非商业使用。

完整、带证据的限制见 [`docs/product/limitations.md`](docs/product/limitations.md)。

## 文档导航

| 主题 | 入口 |
|---|---|
| 安装与第一次播放 | [`docs/getting-started/`](docs/getting-started/README.md) |
| TUI 日常操作 | [`docs/guides/tui.md`](docs/guides/tui.md) |
| CLI / JSON 脚本 | [`docs/guides/cli-and-scripting.md`](docs/guides/cli-and-scripting.md) |
| AI agent 接入 | [`docs/guides/agent.md`](docs/guides/agent.md) |
| 网络电台 | [`docs/guides/radio.md`](docs/guides/radio.md) |
| 排障、日志、错误码 | [`docs/guides/troubleshooting.md`](docs/guides/troubleshooting.md) |
| 全部文档地图 | [`docs/README.md`](docs/README.md) |

## 给开发者

lilt 用 Go 实现（server / CLI / TUI / 来源适配），macOS 由两个签名 Swift helper 分别承载
MusicKit 与流播放。
`lilt serve` 是每个 state root 的唯一常驻 server，其余都是通过 Client API（Unix socket）访问它的
client。接口版本固定写作 `v0.1`，仍是快速迭代期，不做向后兼容。

```sh
just build          # 构建（Linux 上只构建 Go）
just test           # 单元测试
just verify         # 提交前门禁
```

实现细节都在 [`docs/`](docs/README.md)：架构见 [`docs/architecture.md`](docs/architecture.md)，
接口契约见 [`docs/client-api/`](docs/client-api/README.md)，实现契约见
[`docs/internals/`](docs/internals/README.md)。贡献与测试约定见 [`AGENTS.md`](AGENTS.md)。

## License

[MIT](LICENSE)。
