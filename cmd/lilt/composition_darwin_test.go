//go:build darwin

package main

import (
	"os"
	"testing"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/mpvplayer"
	"github.com/caiguo/lilt/internal/playrouter"
	"github.com/caiguo/lilt/internal/server"
)

func TestAppleEngineMode(t *testing.T) {
	for _, test := range []struct {
		value string
		want  string
		bad   bool
	}{
		{"", appleEngineHelper, false},
		{appleEngineHelper, appleEngineHelper, false},
		{appleEngineBrowser, appleEngineBrowser, false},
		{"automatic", "", true},
	} {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv("LILT_APPLE_ENGINE", test.value)
			got, err := appleEngineMode()
			if (err != nil) != test.bad || got != test.want {
				t.Fatalf("appleEngineMode() = %q, %v; want %q, bad=%v", got, err, test.want, test.bad)
			}
		})
	}
}

func TestConfigurePlatformHelperRemainsDefault(t *testing.T) {
	t.Setenv("LILT_APPLE_ENGINE", "")
	options := server.Options{}
	if err := configurePlatform(&options); err != nil {
		t.Fatal(err)
	}
	if options.EngineFactory == nil || options.AppleResourceFactory == nil || options.AudioEngineFactory == nil {
		t.Fatalf("default helper composition is incomplete: %+v", options)
	}
	if len(options.Providers) != 0 || len(options.AuthProviders) != 0 {
		t.Fatal("default helper composition unexpectedly overrides Apple providers")
	}
}

func TestConfigurePlatformBrowserUsesRouterAndWebProviders(t *testing.T) {
	t.Setenv("LILT_APPLE_ENGINE", appleEngineBrowser)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LILT_CHROMIUM_PATH", executable)

	original := startAudioPlayback
	startAudioPlayback = func() (playrouter.Streams, error) { return mpvplayer.New(), nil }
	t.Cleanup(func() { startAudioPlayback = original })

	options := server.Options{}
	if err := configurePlatform(&options); err != nil {
		t.Fatal(err)
	}
	if options.EngineFactory != nil || options.AppleResourceFactory != nil {
		t.Fatal("browser mode retained a MusicKit helper factory")
	}
	engine, err := options.AudioEngineFactory()
	if err != nil {
		t.Fatal(err)
	}
	router, ok := engine.(*playrouter.Player)
	if !ok {
		t.Fatalf("audio engine = %T, want *playrouter.Player", engine)
	}
	defer router.Close()

	provider, auth := false, false
	for _, candidate := range options.Providers {
		provider = provider || candidate.Source() == api.SourceAppleMusic
	}
	for _, candidate := range options.AuthProviders {
		auth = auth || candidate.Source() == api.SourceAppleMusic
	}
	if !provider || !auth {
		t.Fatalf("browser providers: content=%v auth=%v", provider, auth)
	}
}

func TestConfigurePlatformRejectsInvalidMode(t *testing.T) {
	t.Setenv("LILT_APPLE_ENGINE", "invalid")
	if err := configurePlatform(&server.Options{}); err == nil {
		t.Fatal("invalid LILT_APPLE_ENGINE was accepted")
	}
}

func TestDoctorRejectsBrowserModeBeforeStartingHelper(t *testing.T) {
	t.Setenv("LILT_APPLE_ENGINE", appleEngineBrowser)
	t.Setenv("LILT_PLAYER_PATH", t.TempDir()+"/missing.app")
	if code := runDoctor(true); code == 0 {
		t.Fatal("doctor succeeded in browser mode")
	}
}
