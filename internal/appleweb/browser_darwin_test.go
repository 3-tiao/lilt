//go:build darwin

package appleweb

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDarwinChromiumCandidatesOrder(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "Users", "fixture")
	got := darwinChromiumCandidates(home)
	want := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		filepath.Join(home, "Applications/Google Chrome.app/Contents/MacOS/Google Chrome"),
		filepath.Join(home, "Applications/Chromium.app/Contents/MacOS/Chromium"),
		filepath.Join(home, "Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"),
		filepath.Join(home, "Applications/Brave Browser.app/Contents/MacOS/Brave Browser"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}
}

func TestDefaultProfileDirDarwin(t *testing.T) {
	t.Setenv("LILT_APPLE_PROFILE", "")
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := DefaultProfileDir(), filepath.Join(base, "lilt", "apple-browser"); got != want {
		t.Fatalf("DefaultProfileDir() = %q, want %q", got, want)
	}

	override := filepath.Join(t.TempDir(), "profile")
	t.Setenv("LILT_APPLE_PROFILE", override)
	if got := DefaultProfileDir(); got != override {
		t.Fatalf("override = %q, want %q", got, override)
	}
}
