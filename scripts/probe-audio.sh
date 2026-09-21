#!/usr/bin/env bash
# Shared audio handling for the real-playback probes.
#
# Audibility rules:
#
#   - MusicKit playback (Apple Music) has no per-playback volume: macOS owns the
#     output level for it. A probe that plays Apple Music is audible at the
#     current system level, so it requires explicit opt-in and never changes the
#     system volume behind the user's back.
#   - AVPlayer playback (radio stream, preview) honours LILT_PLAYER_VOLUME, so a
#     radio/preview probe can run quietly without touching anything else.
#
# LILT_PROBE_AUDIO=1 approves an audible MusicKit probe. Without it the probe
# scripts refuse to play rather than surprising the user with sound.

probe_audio_opt_in="${LILT_PROBE_AUDIO:-0}"

# probe_audio_require_consent exits unless audible playback was approved.
probe_audio_require_consent() {
  if [ "$probe_audio_opt_in" = "1" ]; then
    echo "-- audio: audible MusicKit playback approved (LILT_PROBE_AUDIO=1)"
    return 0
  fi
  cat >&2 <<'MSG'
-- audio: refusing to play Apple Music without explicit approval.
   MusicKit has no per-playback volume, so this probe is audible at the current
   system level. Re-run with LILT_PROBE_AUDIO=1 to approve it, or use a
   radio/preview ref, which honours LILT_PLAYER_VOLUME (e.g. 0.1).
MSG
  return 1
}
