//go:build linux

package main

import (
	"context"
	"os"
	"testing"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/server"
)

// The Linux composition must never fall back to the macOS helpers: MusicKit is
// Apple-only, so the stream backend is mpv, and it must serve the finite URL
// queues (Audius, Jamendo) that the macOS audio helper also holds.
func TestConfigurePlatformWiresTheLinuxBackends(t *testing.T) {
	options := server.Options{}
	if err := configurePlatform(&options); err != nil {
		t.Fatal(err)
	}

	if options.EngineFactory != nil || options.AppleResourceFactory != nil {
		t.Fatalf("linux composition wired a macOS MusicKit helper: %+v", options)
	}
	if options.AudioEngineFactory == nil {
		t.Fatal("linux composition wired no stream backend")
	}
	engine, err := options.AudioEngineFactory()
	if err != nil {
		t.Fatalf("AudioEngineFactory: %v", err)
	}
	if engine == nil {
		t.Fatal("AudioEngineFactory returned no engine")
	}
	closer, ok := engine.(interface{ Close() error })
	if !ok {
		t.Fatalf("stream backend %T cannot be closed; the server leaks it on rebuild", engine)
	}
	defer func() { _ = closer.Close() }()
	if _, ok := engine.(server.URLPlaybackDriver); !ok {
		t.Fatalf("stream backend %T does not serve URL queues; Audius and Jamendo playback would be advertised but unavailable", engine)
	}
}

// Apple Music on Linux plays through a browser running Apple's own web player, so
// the MusicKit-shaped provider and auth provider are both replaced.
func TestConfigurePlatformReplacesTheAppleSourceWithTheBrowserOne(t *testing.T) {
	// A real browser is not needed to check the wiring: the probe only stats the
	// configured binary, so any existing file stands in for Chromium here.
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv("LILT_CHROMIUM_PATH", executable)

	options := server.Options{}
	if err := configurePlatform(&options); err != nil {
		t.Fatal(err)
	}

	var descriptor api.SourceDescriptor
	found := false
	for _, provider := range options.Providers {
		if provider.Source() != api.SourceAppleMusic {
			continue
		}
		found = true
		descriptor = provider.Descriptor(context.Background())
		if _, ok := provider.(server.PlaybackPreparer); !ok {
			t.Fatalf("Apple provider %T declares playback but cannot prepare it", provider)
		}
		// Album refs are expanded through the provider, so it must resolve them.
		if _, ok := provider.(server.AlbumProvider); !ok {
			t.Fatalf("Apple provider %T cannot resolve album tracks", provider)
		}
	}
	if !found {
		t.Fatal("linux composition registered no Apple provider; the MusicKit one would claim full playback")
	}
	if descriptor.Availability != api.AvailabilityReady {
		t.Fatalf("availability = %q, want ready with a browser present", descriptor.Availability)
	}
	if !descriptor.Capabilities[api.CapPlaybackFull].Available {
		t.Fatal("full playback must be available through the browser")
	}

	found = false
	for _, provider := range options.AuthProviders {
		if provider.Source() != api.SourceAppleMusic {
			continue
		}
		found = true
		if status := provider.Describe(context.Background()); status.Status != api.AuthNotDetermined {
			t.Fatalf("Apple authorization status = %q, want not_determined while no session is up", status.Status)
		}
	}
	if !found {
		t.Fatal("linux composition registered no Apple auth provider")
	}
}

// Without a browser the source must say what is missing rather than advertise
// playback that cannot happen.
func TestConfigurePlatformWithoutABrowserReportsTheRequirement(t *testing.T) {
	t.Setenv("LILT_CHROMIUM_PATH", t.TempDir()+"/absent-chromium")
	options := server.Options{}
	if err := configurePlatform(&options); err != nil {
		t.Fatal(err)
	}

	for _, provider := range options.Providers {
		if provider.Source() != api.SourceAppleMusic {
			continue
		}
		descriptor := provider.Descriptor(context.Background())
		if descriptor.Availability != api.AvailabilityUnavailable {
			t.Fatalf("availability = %q, want unavailable", descriptor.Availability)
		}
		if descriptor.Capabilities[api.CapPlaybackFull].Available {
			t.Fatal("full playback must not be advertised without a browser")
		}
		return
	}
	t.Fatal("no Apple provider registered")
}
