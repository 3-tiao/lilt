package appleweb

import (
	"regexp"
	"strings"
)

// Upstream error text is Apple's, not lilt's: MusicKit playback errors and page
// exceptions can embed signed media URLs, CDN endpoints, query strings, and
// token-shaped blobs. lilt must never republish those — not in a Client API
// response, a watch event, a journal line, or a log — so every upstream
// message passes through sanitizeUpstreamMessage at this package's boundary,
// the single place it enters. The stable, safe remainder is kept; the shapes
// below are replaced with short placeholders.

var (
	// upstreamURLPattern matches an absolute URL up to whitespace or a quote,
	// including its query string. Percent-encoded scheme forms (%3A%2F) match
	// too, because upstream logs occasionally pre-encode the URL.
	upstreamURLPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*(?:://|%3[aA]%2[fF])[^\s"'` + "`" + `<>]+`)
	// upstreamQueryPattern matches bare query strings ("?token=…&sig=…"),
	// which carry signed parameters even after any URL around them is gone.
	upstreamQueryPattern = regexp.MustCompile(`[?&][A-Za-z0-9_.%-]+=[^\s&"'` + "`" + `]*`)
	// upstreamRunPattern finds candidate token runs. A run counts as a token
	// only when it is long and contains a digit (signatures, nonces, and JWT
	// segments are mixed alnum; legible words like NotSupportedError are not),
	// so error text keeps its meaning while blobs do not survive.
	upstreamRunPattern = regexp.MustCompile(`[A-Za-z0-9+/_=-]+`)
	// upstreamSpacePattern collapses the whitespace the removals leave behind.
	upstreamSpacePattern = regexp.MustCompile(`\s+`)
)

// sanitizeUpstreamMessage replaces URLs, query strings, and token-shaped runs
// in an upstream message with placeholders and collapses the leftover
// whitespace. An empty input stays empty: no error is manufactured.
func sanitizeUpstreamMessage(message string) string {
	if strings.TrimSpace(message) == "" {
		return ""
	}
	message = upstreamURLPattern.ReplaceAllString(message, "[url]")
	message = upstreamQueryPattern.ReplaceAllString(message, "[params]")
	message = upstreamRunPattern.ReplaceAllStringFunc(message, func(run string) string {
		if len(run) >= 16 && strings.ContainsAny(run, "0123456789") {
			return "[token]"
		}
		return run
	})
	return strings.TrimSpace(upstreamSpacePattern.ReplaceAllString(message, " "))
}
