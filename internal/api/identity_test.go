package api

import "testing"

// Every accepted input spelling must land on the same identity: this is the
// property that keeps favorites, history, and the TUI highlight in sync.
func TestIdentitySpellingsAgree(t *testing.T) {
	for _, test := range []struct {
		name     string
		got      Identity
		wantID   string
		wantRef  string
		wantProv string
		wantKind string
	}{
		{
			name:     "apple canonical ref",
			got:      NewIdentity(SourceAppleMusic, KindSong, "1440845629", ""),
			wantID:   "am:1440845629",
			wantRef:  "apple-music:song:1440845629",
			wantProv: "1440845629",
			wantKind: KindSong,
		},
		{
			name:     "apple stable id input",
			got:      NewIdentity(SourceAppleMusic, KindSong, "am:1440845629", ""),
			wantID:   "am:1440845629",
			wantRef:  "apple-music:song:1440845629",
			wantProv: "1440845629",
			wantKind: KindSong,
		},
		{
			name:     "apple client spelling with kind segment",
			got:      NewIdentity(SourceAppleMusic, KindSong, "apple-music:song:1440845629", ""),
			wantID:   "am:1440845629",
			wantRef:  "apple-music:song:1440845629",
			wantProv: "1440845629",
			wantKind: KindSong,
		},
		{
			name:     "audius canonical",
			got:      NewIdentity(SourceAudius, KindSong, "track-1", ""),
			wantID:   "audius:song:track-1",
			wantRef:  "audius:song:track-1",
			wantProv: "track-1",
			wantKind: KindSong,
		},
		{
			name:     "audius stable id input",
			got:      NewIdentity(SourceAudius, KindSong, "audius:song:track-1", ""),
			wantID:   "audius:song:track-1",
			wantRef:  "audius:song:track-1",
			wantProv: "track-1",
			wantKind: KindSong,
		},
		{
			name:     "radio normalizes at construction",
			got:      NewIdentity(SourceRadio, KindStream, "", "HTTPS://Radio.Example:443/Live/?token=one#frag"),
			wantID:   "radio:https://radio.example/Live?token=one",
			wantRef:  "https://radio.example/Live?token=one",
			wantProv: "",
			wantKind: KindStream,
		},
	} {
		if test.got.StableID != test.wantID {
			t.Errorf("%s: StableID = %q, want %q", test.name, test.got.StableID, test.wantID)
		}
		if test.got.Ref != test.wantRef {
			t.Errorf("%s: Ref = %q, want %q", test.name, test.got.Ref, test.wantRef)
		}
		if test.got.ProviderID != test.wantProv {
			t.Errorf("%s: ProviderID = %q, want %q", test.name, test.got.ProviderID, test.wantProv)
		}
		if test.got.Kind != test.wantKind {
			t.Errorf("%s: Kind = %q, want %q", test.name, test.got.Kind, test.wantKind)
		}
	}
}

// Unknown or incomplete inputs must yield no identity rather than a guessed one.
func TestIdentityRejectsUnusableInput(t *testing.T) {
	for _, test := range []struct {
		name     string
		identity Identity
	}{
		{"apple without provider id", NewIdentity(SourceAppleMusic, KindSong, "", "")},
		{"audius without provider id", NewIdentity(SourceAudius, KindSong, "", "")},
		{"radio without url", NewIdentity(SourceRadio, KindStream, "", "")},
		{"unknown source", NewIdentity(SourceID("spotify"), KindSong, "1", "")},
	} {
		if test.identity.StableID != "" || test.identity.Ref != "" {
			t.Errorf("%s: %+v, want zero identity", test.name, test.identity)
		}
	}
}

// Rebuilding from a described item is idempotent, whatever spelling the caller
// passes, and never invents a second identity for the same resource.
func TestIdentityFromComponentsIsIdempotent(t *testing.T) {
	canonical := NewIdentity(SourceAppleMusic, KindSong, "1", "")
	for _, test := range []struct {
		name       string
		kind       string
		providerID string
		stableID   string
		ref        string
	}{
		{name: "stable id only", stableID: "am:1"},
		{name: "ref only", ref: "apple-music:song:1"},
		{name: "provider id only", kind: KindSong, providerID: "1"},
		{name: "everything at once", kind: KindSong, providerID: "1", stableID: "am:1", ref: "apple-music:song:1"},
	} {
		got := IdentityFromComponents(SourceAppleMusic, test.kind, test.providerID, test.stableID, test.ref, "")
		if got != canonical {
			t.Errorf("%s: %+v, want %+v", test.name, got, canonical)
		}
	}
}

func TestParseIdentityAcceptsEveryInputSpelling(t *testing.T) {
	want := NewIdentity(SourceAppleMusic, KindSong, "1", "")
	for _, raw := range []string{
		"apple-music:song:1",
		"am:1",
		"https://music.apple.com/us/album/x/1?i=1",
	} {
		got, apiErr := ParseIdentity(raw)
		if apiErr != nil {
			t.Errorf("ParseIdentity(%q): %v", raw, apiErr)
			continue
		}
		if got != want {
			t.Errorf("ParseIdentity(%q) = %+v, want %+v", raw, got, want)
		}
	}

	radio := NewIdentity(SourceRadio, KindStream, "", "https://radio.example/live")
	for _, raw := range []string{
		"https://radio.example/live",
		"radio:https://radio.example/live",
		"radio:HTTPS://Radio.Example:443/live/",
	} {
		got, apiErr := ParseIdentity(raw)
		if apiErr != nil {
			t.Errorf("ParseIdentity(%q): %v", raw, apiErr)
			continue
		}
		if got != radio {
			t.Errorf("ParseIdentity(%q) = %+v, want %+v", raw, got, radio)
		}
	}

	if _, apiErr := ParseIdentity("not-a-reference"); apiErr == nil {
		t.Error("ParseIdentity must reject an unparseable reference")
	}
}

func TestNormalizeStreamURL(t *testing.T) {
	for _, test := range []struct{ raw, want string }{
		{"HTTPS://Radio.Example/Live/?token=one#fragment", "https://radio.example/Live?token=one"},
		{"https://radio.example:443/live", "https://radio.example/live"},
		{"http://radio.example:80/live", "http://radio.example/live"},
		{"https://user:pass@radio.example/live", "https://radio.example/live"},
		// Root, bare, and slash-heavy spellings of one endpoint collapse.
		{"https://radio.example/", "https://radio.example"},
		{"https://radio.example", "https://radio.example"},
		{"https://radio.example///", "https://radio.example"},
		{"  https://radio.example/live  ", "https://radio.example/live"},
		{"https://radio.example/live///", "https://radio.example/live"},
		{"not a url", "not a url"},
		{"not a url//", "not a url"},
		{"", ""},
	} {
		if got := NormalizeStreamURL(test.raw); got != test.want {
			t.Errorf("NormalizeStreamURL(%q) = %q, want %q", test.raw, got, test.want)
		}
	}
}

// A canonical ref must survive parse → build → parse unchanged.
func FuzzIdentityRoundTrip(f *testing.F) {
	for _, seed := range []string{
		"apple-music:song:1",
		"apple-music:playlist:pl.abc",
		"audius:song:track-1",
		"audius:playlist:pl-1",
		"https://radio.example/live",
		"radio:https://radio.example:443/live/?a=1#f",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		identity, apiErr := ParseIdentity(raw)
		if apiErr != nil {
			return
		}
		rebuilt := IdentityFromComponents(identity.Source, identity.Kind, identity.ProviderID, identity.StableID, identity.Ref, identity.StreamURL)
		if rebuilt != identity {
			t.Fatalf("identity %q is not stable: %+v vs %+v", raw, identity, rebuilt)
		}
		// Re-parsing the ref must reproduce the identity exactly. The stable id is
		// the persistence key and must at least be stable itself; for Apple Music
		// it deliberately carries no kind.
		again, err := ParseIdentity(identity.Ref)
		if err != nil {
			t.Fatalf("re-parsing ref %q (from %q): %v", identity.Ref, raw, err)
		}
		if again != identity {
			t.Fatalf("ref %q of %q resolved to %+v, want %+v", identity.Ref, raw, again, identity)
		}
		stable, err := ParseIdentity(identity.StableID)
		if err != nil {
			t.Fatalf("re-parsing stable id %q (from %q): %v", identity.StableID, raw, err)
		}
		if stable.StableID != identity.StableID {
			t.Fatalf("stable id %q of %q resolved to %q", identity.StableID, raw, stable.StableID)
		}
	})
}

// Apple provider ids may contain colons ("fake:album", "fake:track:1"); only
// the explicit "apple-music:<kind>:<id>" client spelling carries a kind segment
// that must be dropped.
func TestAppleProviderIDKeepsEmbeddedColons(t *testing.T) {
	for _, test := range []struct{ input, wantID string }{
		{"fake:album", "am:fake:album"},
		{"am:fake:album", "am:fake:album"},
		{"fake:track:1", "am:fake:track:1"},
		{"apple-music:album:fake:album", "am:fake:album"},
		{"apple-music:song:1", "am:1"},
	} {
		got := NewIdentity(SourceAppleMusic, KindAlbum, test.input, "")
		if got.StableID != test.wantID {
			t.Errorf("NewIdentity(%q).StableID = %q, want %q", test.input, got.StableID, test.wantID)
		}
	}
}
