package core

import "testing"

func TestParseReference(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		kind string
		id   string
		url  string
	}{
		{"kind id", "song:1440845629", "song", "1440845629", ""},
		{"playlist id", "playlist:pl.u-abc", "playlist", "pl.u-abc", ""},
		{"song url", "https://music.apple.com/us/song/name/1440845629", "song", "1440845629", "https://music.apple.com/us/song/name/1440845629"},
		{"playlist url", "https://music.apple.com/cn/playlist/name/pl.u-abc", "playlist", "pl.u-abc", "https://music.apple.com/cn/playlist/name/pl.u-abc"},
		{"album url song parameter", "https://music.apple.com/us/album/name/1440845629?i=1440845630", "song", "1440845630", "https://music.apple.com/us/album/name/1440845629?i=1440845630"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := ParseReference(test.ref)
			if err != nil {
				t.Fatalf("ParseReference(%q) error = %v", test.ref, err)
			}
			if request.Kind != test.kind || request.ID != test.id || request.URL != test.url {
				t.Fatalf("ParseReference(%q) = %#v, want kind=%q id=%q url=%q", test.ref, request, test.kind, test.id, test.url)
			}
		})
	}
}

func TestParseReferenceRejectsInvalid(t *testing.T) {
	for _, ref := range []string{"", "1440845629", "song:", ":42"} {
		if _, err := ParseReference(ref); err == nil {
			t.Errorf("ParseReference(%q) expected error", ref)
		}
	}
}
