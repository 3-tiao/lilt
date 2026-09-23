package appleweb

import (
	"strings"
	"testing"
)

// rawLeakPatterns are generalized leak signatures, not fixture echoes: any
// absolute URL, any percent-encoded scheme, any query-string assignment, any
// long token-shaped run, and the fixture's own host and asset names. A
// sanitized message must contain none of them.
func rawLeakPatterns(t *testing.T, message string) {
	t.Helper()
	for _, pattern := range []struct {
		name   string
		needle string
	}{
		{"scheme", "://"},
		{"percent-encoded scheme", "%3A"},
		{"query assignment", "token="},
		{"query separator", "sig="},
		{"fixture host", "audio-ssl"},
		{"fixture asset", ".m4a"},
		{"fixture token", "SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"},
		{"fixture hex", "deadbeefdeadbeefdeadbeefdeadbeef"},
		{"fixture base64", "MzU2NmVjZGRkNzg5YUc5emRHVHZkR1Z4TG1SdmJnPT0"},
	} {
		if strings.Contains(message, pattern.needle) {
			t.Fatalf("sanitized message still leaks %s: %q", pattern.name, message)
		}
	}
	// Any remaining unbroken run is checked by the same shape the sanitizer uses:
	// long and carrying a digit — legible words survive by design.
	for _, field := range strings.FieldsFunc(message, func(r rune) bool {
		return r == ' ' || r == '.' || r == ',' || r == ')' || r == ']' || r == '}' || r == '(' || r == '[' || r == '{'
	}) {
		if len(field) >= 16 && isTokenShaped(field) {
			t.Fatalf("sanitized message still has a token-shaped run %q: %q", field, message)
		}
	}
}

func isTokenShaped(field string) bool {
	digits := 0
	for _, r := range field {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '+', r == '/', r == '_', r == '-', r == '=':
			if r >= '0' && r <= '9' {
				digits++
			}
		default:
			return false
		}
	}
	return digits > 0
}

func TestSanitizeUpstreamMessageStripsLeaksAndKeepsTheMessage(t *testing.T) {
	nasty := "playback failed fetching https://audio-ssl.itunes.apple.com/AssetStore/v4/12/34/56/abc.m4a?token=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c&sig=deadbeefdeadbeefdeadbeefdeadbeef (403)"
	sanitized := sanitizeUpstreamMessage(nasty)
	rawLeakPatterns(t, sanitized)
	for _, want := range []string{"playback failed fetching", "[url]", "403"} {
		if !strings.Contains(sanitized, want) {
			t.Fatalf("sanitized = %q, want it to keep %q", sanitized, want)
		}
	}
}

func TestSanitizeUpstreamMessageHandlesBareQueriesAndTokenBlobs(t *testing.T) {
	variants := []string{
		"manifest request ?token=ZmFrZS10b2tlbg&sig=deadbeefdeadbeefdeadbeefdeadbeef failed",
		"AuthenticationError MzU2NmVjZGRkNzg5YUc5emRHVHZkR1Z4TG1SdmJnPT0= while contacting the CDN",
		"GET https%3A%2F%2Faudio-ssl.example%2Fasset.m4a%3Ftoken%3DZW5jb2RlZC10b2tlbg timed out",
		"the page said only ordinary words and kept them",
	}
	for _, variant := range variants {
		sanitized := sanitizeUpstreamMessage(variant)
		rawLeakPatterns(t, sanitized)
	}
	// Ordinary text survives untouched.
	if got := sanitizeUpstreamMessage("the page said only ordinary words and kept them"); got != "the page said only ordinary words and kept them" {
		t.Fatalf("ordinary text was rewritten: %q", got)
	}
}

func TestSanitizeUpstreamMessageKeepsEmptyEmpty(t *testing.T) {
	if got := sanitizeUpstreamMessage("   "); got != "" {
		t.Fatalf("blank input became %q", got)
	}
	if got := sanitizeUpstreamMessage(""); got != "" {
		t.Fatalf("empty input became %q", got)
	}
}

// The state probe is the leak vector: a fake page reporting a signed-URL
// playbackError must yield a sanitized State.Error, because everything
// downstream publishes that field verbatim.
func TestStateSanitizesThePagePlaybackError(t *testing.T) {
	nasty := "media failed: https://audio-ssl.itunes.apple.com/asset.m4a?token=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c&sig=deadbeefdeadbeefdeadbeefdeadbeef"
	t.Setenv("LILT_TEST_FAKE_CDP_STATE", `{"ready":true,"authorized":false,"state":1,"error":"`+nasty+`"}`)
	browser, _ := startFakeBrowser(t, true)
	state, err := browser.State(t.Context())
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	rawLeakPatterns(t, state.Error)
	if !strings.Contains(state.Error, "[url]") {
		t.Fatalf("state error = %q, want the URL replaced by a placeholder", state.Error)
	}

	// A not-ready page carries the failure text the same way.
	t.Setenv("LILT_TEST_FAKE_CDP_STATE", `{"ready":false,"failure":"fetch failed: https://audio-ssl.example.com/x.m4a?sig=deadbeefdeadbeefdeadbeef"}`)
	browser, _ = startFakeBrowser(t, true)
	state, err = browser.State(t.Context())
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	rawLeakPatterns(t, state.Error)
}
