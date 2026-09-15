# Bugfix · 广播与 Apple Music 可同时出声（用户报告）

## 现象

用户报告「广播和 Apple Music 好像可以同时播放」，而设计要求二者严格互斥。

## 诊断（helper 内部埋点，观察两个播放器的真实状态）

用可回读的临时探针记录了 `mode`、`AVPlayer.timeControlStatus`、`ApplicationMusicPlayer.playbackStatus`、
以及 **MusicKit 播放位置是否仍在推进**（位置推进 = 真的在出声，比状态字符串可靠）。

结论：

- 方向「电台 → Apple Music」正确：`play()` 会先 `streamPlayer?.pause()` 并释放旧播放器
  （weak 引用验证 `oldStream=released`，即音频确实停止）。
- 方向「Apple Music → 电台」存在问题：
  `ApplicationMusicPlayer.shared.stop()` **异步生效**——调用后 MusicKit 仍报 `playing` 且位置继续推进，
  实测需 **0.05–2.5 秒**才真正静音。这期间电台流已经开始播放 → 两者同时出声。
- 另一个潜在缺口：`playPreview`（未授权或 MusicKit 失败的 preview 回退）此前不停止电台流与 MusicKit。

## 修复

1. 电台播放改为「静音起步」：若启动电台前 MusicKit 正在播，则新 `AVPlayer` 以
   `isMuted=true, volume=0` 开始缓冲，后台轮询（上限 4s）直到 MusicKit 真正非 playing 再解除静音；
   只在 `streamPlayer === player && mode == "stream" && !streamPaused` 时解除，过期实例直接 `pause()`。
   这样既不阻塞命令循环（探测/暂停/退出照常响应），也不产生重叠。
2. preview 回退路径改为**有界等待**（上限 3s）MusicKit 静音后再播。
3. `playPreview` 防御性暂停电台流；`resume()` 恢复流时始终清除 muted 标志
   （修掉「静音窗口内暂停后恢复会一直无声」的边界）。

## 验证

- 埋点实测：`radio.afterMusicStop music=playing musicT=11.5` → 下一次采样
  `radio.unmuted music=paused musicT=11.5`（本次 55ms，慢时最多等 4s 才解除静音），
  即**解除静音永远发生在 MusicKit 静音之后**。
- 反方向仍即时静音（`oldStream=released`）。
- 电台暂停/恢复、切视图、探测、退出均正常；`just verify` 全绿。
- 全部临时埋点已移除（`rg debugPlayers` 无残留）。

## 说明

MusicKit `stop()` 的异步性是平台行为，无法从公开 API 消除；「静音起步 + 静音后解除」是在不牺牲
响应性的前提下保证互斥的最小实现。`docs/spec/playback-state-sync.md` 已记录该行为契约。
