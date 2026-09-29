package presentation

import (
	"strings"
	"testing"

	"github.com/3-tiao/lilt/core"
)

func TestTextRemovesTerminalControlsAndPreservesUnicode(t *testing.T) {
	input := "爵士\x1b]8;;https://evil.invalid\aCLICK\x1b]8;;\a\x1b[31m红\x1b[0m\nnext\u009b2J\tend"
	got := Text(input)
	if strings.ContainsAny(got, "\t\x1b\a\u009b\n\r") {
		t.Fatalf("controls remain in %q", got)
	}
	for _, want := range []string{"爵士", "CLICK", "红", "next", " end"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sanitized text %q is missing %q", got, want)
		}
	}
}

func TestTextLeavesNoC0OrC1Controls(t *testing.T) {
	var input strings.Builder
	for r := rune(0); r <= 0x9f; r++ {
		input.WriteRune(r)
	}
	got := Text(input.String())
	for _, r := range got {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			t.Fatalf("control U+%04X survived in %q", r, got)
		}
	}
	if !strings.HasPrefix(got, "    !") { // tab, LF, CR, then printable U+0020.
		t.Fatalf("control replacement has unstable prefix: %q", got)
	}
}

func TestItemTrimsStationNameWhitespace(t *testing.T) {
	item := Item(core.Item{Kind: "stream", Title: " CAPITAL - Hit Music ", Artist: "\tFrance\n"})
	if item.Title != "CAPITAL - Hit Music" {
		t.Fatalf("title = %q", item.Title)
	}
	if item.Artist != "France" {
		t.Fatalf("artist = %q", item.Artist)
	}
	// Interior spaces are directory data and must survive.
	if got := Item(core.Item{Title: " 50s 60s RETRO "}).Title; got != "50s 60s RETRO" {
		t.Fatalf("interior spacing changed: %q", got)
	}
}

func TestPlaybackSanitizesExternalMetadata(t *testing.T) {
	got := Playback(core.PlaybackState{Track: &core.Item{Title: "bad\x1b[2Jtitle"}, Queue: []core.Item{{Artist: "x\u0085y"}}, Error: "oops\x1b[31m"})
	if strings.Contains(got.Track.Title, "\x1b") || strings.Contains(got.Queue[0].Artist, "\u0085") || strings.Contains(got.Error, "\x1b") {
		t.Fatalf("unsanitized playback: %#v", got)
	}
}
