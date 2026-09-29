package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/audius"
)

func TestAudiusLibraryCapabilityFollowsAuthorization(t *testing.T) {
	raw := audiusProvider{}
	if capability := raw.Descriptor(context.Background()).Capabilities[api.CapLibrary]; capability.Available || capability.Reason == "" {
		t.Fatalf("unlinked Audius library capability = %+v", capability)
	}
	if _, apiErr := raw.LibraryPlaylists(context.Background()); apiErr == nil || apiErr.Code != api.CodeAuthorizationRequired {
		t.Fatalf("unlinked library error = %+v", apiErr)
	}

	linked := audiusProvider{credentials: func() (string, string, bool) { return "token", "u1", true }}
	if capability := linked.Descriptor(context.Background()).Capabilities[api.CapLibrary]; !capability.Available {
		t.Fatalf("linked Audius library capability = %+v", capability)
	}
}

func TestAudiusLibraryPlaylistsUsesBearerToken(t *testing.T) {
	var gotAuth, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		_, _ = w.Write([]byte(`{"data":[{"id":"p1","playlist_name":"My List","permalink":"/u/my-list","user":{"name":"A"}}]}`))
	}))
	defer upstream.Close()

	client := audius.Client{BaseURL: upstream.URL, HTTP: upstream.Client()}
	provider := audiusProvider{client: client, credentials: func() (string, string, bool) { return "tok", "u1", true }}
	items, apiErr := provider.LibraryPlaylists(context.Background())
	if apiErr != nil {
		t.Fatalf("library: %+v", apiErr)
	}
	if gotAuth != "Bearer tok" || !strings.HasSuffix(gotPath, "/users/u1/playlists") {
		t.Fatalf("request auth=%q path=%q", gotAuth, gotPath)
	}
	if len(items) != 1 || items[0].Kind != api.KindPlaylist || items[0].Title != "My List" {
		t.Fatalf("items = %#v", items)
	}
}
