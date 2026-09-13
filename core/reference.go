package core

import (
	"errors"
	"net/url"
	"strings"
)

// ParseReference turns a user-supplied reference into a PlaybackRequest.
// Accepted forms are an Apple Music URL (https://music.apple.com/...) or a
// kind:id pair such as song:1440845629 or playlist:pl.u-abc.
func ParseReference(reference string) (PlaybackRequest, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return PlaybackRequest{}, errors.New("reference is empty; use an Apple Music URL or kind:id")
	}
	if strings.HasPrefix(reference, "http://") || strings.HasPrefix(reference, "https://") {
		return parseURLReference(reference)
	}
	kind, id, ok := strings.Cut(reference, ":")
	if !ok || kind == "" || id == "" {
		return PlaybackRequest{}, errors.New("reference must be an Apple Music URL or kind:id")
	}
	return PlaybackRequest{Kind: kind, ID: id}, nil
}

func parseURLReference(reference string) (PlaybackRequest, error) {
	parsed, err := url.Parse(reference)
	if err != nil {
		return PlaybackRequest{}, err
	}
	request := PlaybackRequest{URL: reference}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) >= 2 {
		switch segments[1] {
		case "song", "album", "playlist", "station":
			request.Kind = segments[1]
		}
	}
	if request.Kind == "album" {
		if id := parsed.Query().Get("i"); id != "" {
			request.Kind, request.ID = "song", id
			return request, nil
		}
	}
	if len(segments) > 0 {
		request.ID = segments[len(segments)-1]
	}
	return request, nil
}
