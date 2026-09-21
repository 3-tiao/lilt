package api

import (
	"net"
	"net/url"
	"strings"
)

// Identity is the single source of truth for an item's identity. Every layer
// that needs to name an item — playback refs, persisted rows, favorites, TUI
// highlights — derives it here instead of trimming prefixes by hand.
//
// The four fields are distinct and must not be conflated:
//
//   - ProviderID is the provider-native id ("1440845629", "track-1"). It is
//     empty for radio, whose identity is its normalized stream URL.
//   - StableID is the local persistent identity: "am:<providerID>",
//     "audius:<kind>:<providerID>", or "radio:<normalized url>".
//   - Ref is the public, playable reference: "apple-music:<kind>:<id>",
//     "audius:<kind>:<id>", or the raw stream URL for radio.
//   - StreamURL is the stable public stream URL, set for radio only.
type Identity struct {
	Source     SourceID
	Kind       string
	ProviderID string
	StableID   string
	Ref        string
	StreamURL  string
}

// NewIdentity builds the canonical identity for a source-backed item. streamURL
// is used only for radio, where it is also the identity. An unknown source or a
// missing provider id yields a zero Identity, which callers treat as "no
// identity" rather than inventing one.
func NewIdentity(source SourceID, kind, providerID, streamURL string) Identity {
	kind = strings.ToLower(strings.TrimSpace(kind))
	providerID = strings.TrimSpace(providerID)
	streamURL = strings.TrimSpace(streamURL)
	if kind == "" {
		kind = defaultKind(source)
	}
	// Kinds are the public enum: an unknown kind would make the identity
	// unparseable, because the ref spelling is validated against the same set.
	if !playbackKinds[kind] {
		return Identity{}
	}
	switch source {
	case SourceAppleMusic:
		providerID = strings.TrimSpace(providerIDFromStableID(source, providerID))
		if providerID == "" || hasStableIdentityPrefix(providerID) {
			// A provider id that still looks like a stable id ("am:am:1") is
			// ambiguous: stripping prefixes again would make identity depend on
			// how many times it ran.
			return Identity{}
		}
		return Identity{
			Source:     source,
			Kind:       kind,
			ProviderID: providerID,
			StableID:   "am:" + providerID,
			Ref:        AppleMusicRef(kind, providerID),
		}
	case SourceAudius:
		providerID = strings.TrimSpace(providerIDFromStableID(source, providerID))
		if providerID == "" || hasStableIdentityPrefix(providerID) {
			return Identity{}
		}
		return Identity{
			Source:     source,
			Kind:       kind,
			ProviderID: providerID,
			StableID:   AudiusRef(kind, providerID),
			Ref:        AudiusRef(kind, providerID),
		}
	case SourceRadio:
		raw := streamURL
		if raw == "" {
			// Accept the state identity spelling "radio:<url>" as input too.
			raw = strings.TrimPrefix(providerID, string(SourceRadio)+":")
		}
		normalized := streamIdentity(raw)
		if normalized == "" {
			return Identity{}
		}
		return Identity{
			Source:    source,
			Kind:      KindStream,
			StableID:  RadioRef(normalized),
			Ref:       normalized,
			StreamURL: normalized,
		}
	default:
		return Identity{}
	}
}

// IdentityFromComponents rebuilds the identity of an item the server already
// described. It is idempotent: a canonical StableID or Ref always resolves to
// the same Identity, and provider-native ids are accepted too. A ref wins over
// a stable id because only the ref carries the kind for Apple Music, whose
// persisted "am:<id>" identity deliberately does not.
func IdentityFromComponents(source SourceID, kind, providerID, stableID, ref, streamURL string) Identity {
	if identity, ok := parseRef(ref); ok {
		return identity
	}
	if identity, ok := parseStableID(stableID); ok {
		return identity
	}
	// A caller that only knows the provider-native id still gets the canonical
	// form for its source.
	return NewIdentity(source, kind, providerID, streamURL)
}

// ParseIdentity resolves any accepted input spelling into one Identity:
// canonical refs ("apple-music:song:1"), state identity refs
// ("radio:https://…"), Apple Music URLs, and raw stream URLs. Input that
// carries no usable identity is an error, never a zero Identity.
func ParseIdentity(raw string) (Identity, *Error) {
	identity, apiErr := parseIdentity(raw)
	if apiErr != nil {
		return Identity{}, apiErr
	}
	if identity.StableID == "" {
		return Identity{}, Errorf(CodeInvalidReference, "reference %q has no usable identity", raw)
	}
	return identity, nil
}

func parseIdentity(raw string) (Identity, *Error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Identity{}, Errorf(CodeInvalidReference, "reference is empty")
	}
	if identity, ok := parseStableID(trimmed); ok {
		return identity, nil
	}
	if identity, ok := parseRef(trimmed); ok {
		return identity, nil
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		// A bare URL is a radio stream unless it addresses Apple Music.
		reference, apiErr := ParseReference(trimmed)
		if apiErr != nil {
			return Identity{}, apiErr
		}
		if reference.Source == SourceAppleMusic {
			return NewIdentity(SourceAppleMusic, reference.Kind, reference.ID, ""), nil
		}
		return NewIdentity(SourceRadio, KindStream, "", trimmed), nil
	}
	return Identity{}, Errorf(CodeInvalidReference,
		"reference %q must be a source:kind:id ref, an Apple Music URL, or a stream URL", raw)
}

// parseStableID accepts the persistent identities: "am:<id>",
// "audius:<kind>:<id>", and "radio:<url>" (normalized or not).
func parseStableID(raw string) (Identity, bool) {
	var identity Identity
	switch {
	case strings.HasPrefix(raw, "am:"):
		identity = NewIdentity(SourceAppleMusic, "", strings.TrimPrefix(raw, "am:"), "")
	case strings.HasPrefix(raw, string(SourceAudius)+":"):
		rest := strings.TrimPrefix(raw, string(SourceAudius)+":")
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) == 2 {
			identity = NewIdentity(SourceAudius, parts[0], parts[1], "")
		} else {
			// "audius:<id>" is a client spelling from before kind was carried.
			identity = NewIdentity(SourceAudius, KindSong, rest, "")
		}
	case strings.HasPrefix(raw, string(SourceRadio)+":"):
		identity = NewIdentity(SourceRadio, KindStream, "", strings.TrimPrefix(raw, string(SourceRadio)+":"))
	default:
		return Identity{}, false
	}
	// A whitespace-only id must not become an identity with a blank key.
	return identity, identity.StableID != ""
}

// parseRef accepts the canonical playback refs, including the Apple Music
// "apple-music:<id>" spelling clients used before kind was carried.
func parseRef(raw string) (Identity, bool) {
	reference, apiErr := ParseReference(raw)
	if apiErr != nil {
		return Identity{}, false
	}
	var identity Identity
	switch reference.Source {
	case SourceAppleMusic:
		identity = NewIdentity(SourceAppleMusic, reference.Kind, reference.ID, "")
	case SourceAudius:
		identity = NewIdentity(SourceAudius, reference.Kind, reference.ID, "")
	case SourceRadio:
		identity = NewIdentity(SourceRadio, KindStream, "", reference.URL)
	default:
		return Identity{}, false
	}
	return identity, identity.StableID != ""
}

// providerIDFromStableID strips a stable-id prefix that a caller passed as the
// provider id, so "am:1", "1", and "audius:song:t1" all resolve the same way.
func providerIDFromStableID(source SourceID, providerID string) string {
	switch source {
	case SourceAppleMusic:
		// Only the explicit "apple-music:<kind>:<id>" client spelling carries a
		// kind segment. Apple provider ids may themselves contain colons
		// ("fake:album"), so the plain "am:" form is never split.
		if rest, ok := strings.CutPrefix(providerID, string(SourceAppleMusic)+":"); ok {
			if idx := strings.Index(rest, ":"); idx >= 0 {
				return rest[idx+1:]
			}
			return rest
		}
		return strings.TrimPrefix(providerID, "am:")
	case SourceAudius:
		// Only the explicit "audius:<kind>:<id>" form carries a kind segment;
		// a bare provider id is kept verbatim.
		rest, ok := strings.CutPrefix(providerID, string(SourceAudius)+":")
		if !ok {
			return providerID
		}
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) == 2 {
			return parts[1]
		}
		return rest
	default:
		return providerID
	}
}

// hasStableIdentityPrefix reports whether id still carries a stable identity
// prefix after the accepted client spellings were stripped.
func hasStableIdentityPrefix(id string) bool {
	for _, prefix := range []string{
		"am:",
		string(SourceAppleMusic) + ":",
		string(SourceAudius) + ":",
		string(SourceRadio) + ":",
	} {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

// defaultKind is the kind a source's items carry when nothing more specific is
// known. Radio is always a stream; the catalog sources default to song.
func defaultKind(source SourceID) string {
	if source == SourceRadio {
		return KindStream
	}
	return KindSong
}

// streamIdentity normalizes a stream URL and rejects anything that is not a
// well-formed absolute http(s) endpoint: radio identity is a URL, and a bare
// token would otherwise become an identity that cannot be played or re-parsed.
func streamIdentity(raw string) string {
	normalized := NormalizeStreamURL(raw)
	if normalized == "" {
		return ""
	}
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Host == "" || !wellFormedHost(parsed.Host) {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	return normalized
}

// opaqueStreamValue is the fallback for input that is not a canonicalizable URL:
// trimmed, stripped of trailing slashes, and stable under another pass so that
// normalization stays idempotent even for junk input.
func opaqueStreamValue(raw string) string {
	value := raw
	for range 4 {
		next := strings.TrimRight(strings.TrimSpace(value), "/")
		if next == value {
			break
		}
		value = next
	}
	return value
}

// NormalizeStreamURL produces the stable radio endpoint identity: lowercase
// scheme and host, no default port, no fragment, no userinfo, and no trailing
// slash on a real path. The query is preserved because it can select a distinct
// stream. This is the only stream-URL normalization in the codebase, and it is
// idempotent: normalizing an already-normalized value changes nothing.
func NormalizeStreamURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || !wellFormedHost(parsed.Host) {
		// Not a URL we can canonicalize: keep it opaque but stable instead of
		// guessing at a host split.
		return opaqueStreamValue(trimmed)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	switch port := parsed.Port(); {
	case port == "" || (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443"):
		parsed.Host = strings.ToLower(parsed.Hostname())
	default:
		parsed.Host = net.JoinHostPort(strings.ToLower(parsed.Hostname()), port)
	}
	// Every trailing slash collapses, and an all-slash path becomes the bare
	// endpoint, so "https://x", "https://x/", and "https://x///" are one
	// identity.
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	// A selector query is preserved, but never with surrounding whitespace:
	// the string would not survive another normalization pass.
	parsed.RawQuery = strings.TrimSpace(parsed.RawQuery)
	parsed.Fragment = ""
	parsed.User = nil
	return parsed.String()
}

// wellFormedHost reports whether Host is one host and at most one port.
// Degenerate values such as "name:443:443" make Hostname/Port strip one layer
// per call, which would make normalization depend on how often it ran.
func wellFormedHost(host string) bool {
	if strings.HasPrefix(host, "[") {
		closing := strings.Index(host, "]")
		if closing < 0 {
			return false
		}
		rest := host[closing+1:]
		return rest == "" || (strings.HasPrefix(rest, ":") && isDigits(rest[1:]))
	}
	if strings.Count(host, ":") > 1 {
		return false
	}
	if idx := strings.Index(host, ":"); idx >= 0 {
		return isDigits(host[idx+1:])
	}
	return true
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
