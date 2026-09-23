package appleweb

import (
	"context"
	"encoding/json"
	"fmt"
)

// WidevineProbe is the EME support answer cached from one browser start. Full
// playback is DRM content, so whether this browser can play it is a
// capability fact, and claiming it without evidence is exactly the lie the
// descriptor used to tell.
type WidevineProbe struct {
	// Answered reports whether a browser start produced a definitive answer.
	// A browser that never started has not answered, and the descriptor keeps
	// its declared precondition instead of paying a cold start to know.
	Answered bool
	// Supported reports whether the page's EME pipeline can play Widevine.
	Supported bool
	// Reason explains a negative answer; it is sanitized upstream text and safe
	// to publish.
	Reason string
}

// widevineProbeJS asks the page's own EME pipeline whether Widevine works,
// inside the same page every catalog and playback call already drives. The
// secure-context guard and the exception path are handled in the page: both a
// refusal from requestMediaKeySystemAccess and an insecure context are negative
// answers, not errors.
const widevineProbeJS = `(async () => {
  try {
    if (!window.isSecureContext) {
      return JSON.stringify({status: 'insecure', reason: 'the page is not a secure context'});
    }
    const config = [{
      initDataTypes: ['cenc', 'keyids', 'webm'],
      audioCapabilities: [{contentType: 'audio/mp4;codecs="mp4a.40.2"'}],
      videoCapabilities: [{contentType: 'video/mp4;codecs="avc1.42E01E"'}],
    }];
    await navigator.requestMediaKeySystemAccess('com.widevine.alpha', config);
    return JSON.stringify({status: 'ok'});
  } catch (e) {
    return JSON.stringify({status: 'denied', reason: String((e && e.name) || e)});
  }
})()`

// probeWidevine asks the page whether its EME pipeline supports Widevine and
// folds every outcome — including its own failure to evaluate — into the
// tri-state answer. A probe that could not run must not be read as support:
// full playback stays undeclared until something positively answers for it.
func (b *Browser) probeWidevine(ctx context.Context) WidevineProbe {
	raw, err := b.Evaluate(ctx, widevineProbeJS)
	if err != nil {
		return WidevineProbe{Answered: true, Reason: sanitizeUpstreamMessage(fmt.Sprintf("the Widevine probe did not answer: %v", err))}
	}
	var payload struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return WidevineProbe{Answered: true, Reason: sanitizeUpstreamMessage(fmt.Sprintf("the Widevine probe returned an unreadable answer: %v", raw))}
	}
	switch payload.Status {
	case "ok":
		return WidevineProbe{Answered: true, Supported: true}
	case "denied", "insecure":
		return WidevineProbe{Answered: true, Reason: sanitizeUpstreamMessage(payload.Reason)}
	default:
		return WidevineProbe{Answered: true, Reason: sanitizeUpstreamMessage(fmt.Sprintf("the Widevine probe returned %q", payload.Status))}
	}
}
