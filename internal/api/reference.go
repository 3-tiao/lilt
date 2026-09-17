package api

import (
	"net/url"
	"strings"
)

// Reference is the parsed canonical form of a playable input. URL is set only
// for radio streams; ID is the provider-native id for source-backed refs.
type Reference struct {
	Source SourceID
	Kind   string
	ID     string
	URL    string
	Raw    string
}

// ParseReference parses a canonical ref such as apple-music:song:1646769334,
// a legacy bare ref such as song:1646769334 (interpreted as apple-music), or a
// stream URL. Radio identity refs (radio:<url>) are state identity, not
// playback input, and are rejected here.
func ParseReference(raw string) (Reference, *Error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Reference{}, Errorf(CodeInvalidReference, "reference is empty")
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return parseURLReference(trimmed)
	}
	parsed, err := parseCanonical(trimmed)
	if err != nil {
		return Reference{}, err
	}
	return parsed, nil
}

var knownSources = map[string]SourceID{
	string(SourceAppleMusic): SourceAppleMusic,
	string(SourceRadio):      SourceRadio,
	string(SourceAudius):     SourceAudius,
}

var playbackKinds = map[string]bool{
	KindSong:     true,
	KindPlaylist: true,
	KindStation:  true,
	KindStream:   true,
}

// parseCanonical handles [source:]kind:id forms.
func parseCanonical(raw string) (Reference, *Error) {
	parts := strings.Split(raw, ":")
	if len(parts) < 2 {
		return Reference{}, Errorf(CodeInvalidReference, "reference %q must be a URL or [source:]kind:id", raw)
	}
	reference := Reference{Raw: raw}
	first := strings.ToLower(parts[0])
	if source, ok := knownSources[first]; ok {
		if source == SourceRadio {
			return Reference{}, Errorf(CodeInvalidReference, "radio identity is not a playback input; use the stream URL or an Item ref")
		}
		if len(parts) < 3 {
			return Reference{}, Errorf(CodeInvalidReference, "reference %q is missing its kind:id", raw)
		}
		reference.Source = source
		reference.Kind = strings.ToLower(parts[1])
		reference.ID = strings.Join(parts[2:], ":")
	} else {
		// Bare kind:id is a compatibility form interpreted as apple-music.
		reference.Source = SourceAppleMusic
		reference.Kind = first
		reference.ID = strings.Join(parts[1:], ":")
	}
	if reference.ID == "" {
		return Reference{}, Errorf(CodeInvalidReference, "reference %q has an empty id", raw)
	}
	if !playbackKinds[reference.Kind] {
		return Reference{}, Errorf(CodeInvalidReference, "reference kind %q is not playable", reference.Kind)
	}
	return reference, nil
}

func parseURLReference(raw string) (Reference, *Error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return Reference{}, Errorf(CodeInvalidReference, "invalid URL: %v", err)
	}
	if parsed.Host == "" {
		return Reference{}, Errorf(CodeInvalidReference, "invalid URL: missing host")
	}
	reference := Reference{Raw: raw}
	if isAppleMusicHost(parsed.Host) {
		reference.Source = SourceAppleMusic
		reference.Kind, reference.ID = appleMusicPath(parsed)
		if reference.ID == "" {
			return Reference{}, Errorf(CodeInvalidReference, "Apple Music URL has no resource id")
		}
		if reference.Kind == "" {
			reference.Kind = KindSong
		}
		return reference, nil
	}
	// Any other URL is a radio stream.
	reference.Source = SourceRadio
	reference.Kind = KindStream
	reference.URL = raw
	return reference, nil
}

func isAppleMusicHost(host string) bool {
	host = strings.ToLower(host)
	return host == "music.apple.com" || strings.HasSuffix(host, ".music.apple.com")
}

func appleMusicPath(parsed *url.URL) (kind, id string) {
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) >= 2 {
		switch segments[1] {
		case "song", "album", "playlist", "station":
			kind = segments[1]
		}
	}
	if kind == "album" {
		if trackID := parsed.Query().Get("i"); trackID != "" {
			return KindSong, trackID
		}
	}
	if len(segments) > 0 {
		id = segments[len(segments)-1]
	}
	return kind, id
}

// AppleMusicRef builds the canonical ref for an Apple Music resource.
func AppleMusicRef(kind, providerID string) string {
	return string(SourceAppleMusic) + ":" + kind + ":" + providerID
}

// RadioRef builds the persistent radio identity ref for a normalized URL. It
// is a state identity, not a playback input.
func RadioRef(normalizedURL string) string {
	return string(SourceRadio) + ":" + normalizedURL
}

// Ref builds the canonical playback ref for a reference.
func (r Reference) Ref() string {
	if r.Kind == KindStream {
		return r.URL
	}
	return string(r.Source) + ":" + r.Kind + ":" + r.ID
}

// RadioOrigin values.
const (
	OriginBuiltin   = "builtin"
	OriginDirectory = "directory"
	OriginUser      = "user"
)
