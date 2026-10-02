package auditcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const apiFixture = `package api

const (
	KindSong               = "song"
	KindPlaylist           = "playlist"
	KindAlbum              = "album"
	KindStation            = "station"
	KindStream             = "stream"
	WatchKindDisconnected  = "session.disconnected"
	KindNotAString         = 3
)
`

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPublicKindsReadsDeclaredStringConstants(t *testing.T) {
	api := t.TempDir()
	writeFile(t, api, "models.go", apiFixture)
	writeFile(t, api, "models_test.go", `package api
const KindBogus = "bogus"
`)

	kinds, err := PublicKinds(api)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"song": "KindSong", "playlist": "KindPlaylist", "album": "KindAlbum",
		"station": "KindStation", "stream": "KindStream",
	}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for literal, name := range want {
		if kinds[literal] != name {
			t.Fatalf("kinds[%q] = %q, want %q", literal, kinds[literal], name)
		}
	}
}

func TestPublicKindsRejectsMissingDeclaration(t *testing.T) {
	api := t.TempDir()
	writeFile(t, api, "empty.go", "package api\n")
	if _, err := PublicKinds(api); err == nil {
		t.Fatal("an api package without Kind* constants must be an error")
	}
}

func TestBareKindLiteralsFindsOnlyKindPositions(t *testing.T) {
	target := t.TempDir()
	writeFile(t, target, "target.go", `package target

func build() Item {
	song := Item{Kind: "song", Title: "song"}
	other := Item{Kind: "browse", Title: "browse"}
	return song
}

func compare(item Item) bool {
	return item.Kind == "album" && item.Kind != "station"
}

func glyph(item Item) string {
	switch item.Kind {
	case "playlist":
		return "="
	case "header":
		return "-"
	}
	return ""
}

func axis(kind string) int {
	switch kind {
	case "language":
		return 1
	}
	return 0
}

func message() string { return "song and album are kinds" }
`)

	// KindNotAString is declared in the fixture but must not be inherited here.
	kinds := map[string]string{
		"song": "KindSong", "playlist": "KindPlaylist", "album": "KindAlbum",
		"station": "KindStation", "stream": "KindStream",
	}
	found, err := BareKindLiterals(target, kinds)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, v := range found {
		got[v.Literal]++
	}
	want := map[string]int{"song": 1, "album": 1, "station": 1, "playlist": 1}
	if len(got) != len(want) {
		t.Fatalf("violations = %v (%v), want %v", got, found, want)
	}
	for literal, count := range want {
		if got[literal] != count {
			t.Fatalf("violations[%q] = %d, want %d (all: %v)", literal, got[literal], count, found)
		}
	}
	for _, v := range found {
		if v.Constant == "" || v.Line == 0 || v.Path == "" || v.Length == 0 {
			t.Fatalf("incomplete violation: %+v", v)
		}
	}
}

// TestClientSurfacesUseAPIConstants is the gate: the public media kind values
// are declared once in api and must not be re-spelled in the client surfaces.
// Adding api.Kind* to a switch is the fix; client-only node kinds such as
// "browse" or "entry-account" are unaffected because they are not in the enum.
func TestClientSurfacesUseAPIConstants(t *testing.T) {
	kinds, err := PublicKinds("../api")
	if err != nil {
		t.Fatal(err)
	}
	var all []Violation
	for _, dir := range []string{"../tui", "../client", "../../cmd/lilt"} {
		found, err := BareKindLiterals(dir, kinds)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, found...)
	}
	if len(all) == 0 {
		return
	}
	lines := make([]string, 0, len(all))
	for _, v := range all {
		lines = append(lines, v.String())
	}
	t.Fatalf("%d bare public-kind literals; use the api constant:\n%s",
		len(all), strings.Join(lines, "\n"))
}
