package jamendo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/caiguo/lilt/internal/api"
)

// TestMain removes the retry backoff: the tests assert the retry mechanics, so
// the wall-clock pause would only slow the suite down.
func TestMain(m *testing.M) {
	retryDelay = []time.Duration{0, 0}
	os.Exit(m.Run())
}

// credentials returns a reader for a configured client_id.
func credentials(clientID string) Credentials {
	return func() (string, error) {
		if clientID == "" {
			return "", ErrNotConfigured
		}
		return clientID, nil
	}
}

func TestDiscoveryRequestsAndErrorsAreHermetic(t *testing.T) {
	mode := "tracks"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("client_id") != "test-client" {
			t.Errorf("client_id = %q, want the configured credential", query.Get("client_id"))
		}
		if query.Get("format") != "json" {
			t.Errorf("format = %q, want json", query.Get("format"))
		}
		if r.URL.Path == "/tracks" && query.Get("id") == "t1" && query.Get("audioformat") == PlaybackAudioFormat {
			_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[{"id":"t1","name":"Track","duration":272,"artist_name":"Artist","audio":"https://media.example/t1.mp3?format=mp32","shareurl":"https://www.jamendo.com/track/t1"}]}`))
			return
		}
		if r.URL.Path == "/playlists/tracks" {
			_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[` + trackJSON("t9") + `]}`))
			return
		}
		if r.URL.Path == "/playlists" {
			if query.Get("namesearch") != "" || query.Get("id") == "p1" {
				_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[{"id":"p1","name":"List","user_name":"Curator","shareurl":"https://www.jamendo.com/list/p1"}]}`))
				return
			}
		}
		switch mode {
		case "tracks":
			_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0,"results_count":1},"results":[` + trackJSON("t1") + `]}`))
		case "empty":
			_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0,"results_count":0},"results":[]}`))
		case "invalid-client":
			_, _ = w.Write([]byte(`{"headers":{"status":"failed","code":5,"error_message":"Your credential is not authorized."},"results":[]}`))
		case "suspended":
			_, _ = w.Write([]byte(`{"headers":{"status":"failed","code":11,"error_message":"Your application has been suspended"},"results":[]}`))
		case "rate-limited":
			_, _ = w.Write([]byte(`{"headers":{"status":"failed","code":6,"error_message":"rate limit exceeded"},"results":[]}`))
		case "bad-request":
			_, _ = w.Write([]byte(`{"headers":{"status":"failed","code":3,"error_message":"parameter type"},"results":[]}`))
		case "malformed":
			_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":`))
		default:
			http.Error(w, "private upstream body", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: server.Client(), Credentials: credentials("test-client")}

	tracks, apiErr := client.SearchTracks(context.Background(), "lofi", 3)
	if apiErr != nil || len(tracks) != 1 || tracks[0].ID != "t1" {
		t.Fatalf("tracks=%#v err=%v", tracks, apiErr)
	}
	if tracks[0].DurationSeconds() != 272 || tracks[0].ArtworkURL() != "https://art.example/t1.jpg" {
		t.Fatalf("track projection = %#v", tracks[0])
	}

	mode = "empty"
	if tracks, apiErr = client.SearchTracks(context.Background(), "nothing", 3); apiErr != nil || len(tracks) != 0 {
		t.Fatalf("empty tracks=%#v err=%v", tracks, apiErr)
	}

	mode = "tracks"
	playlists, apiErr := client.SearchPlaylists(context.Background(), "lofi", 3)
	if apiErr != nil || len(playlists) != 1 || playlists[0].ID != "p1" {
		t.Fatalf("playlists=%#v err=%v", playlists, apiErr)
	}
	if playlist, apiErr := client.Playlist(context.Background(), "p1"); apiErr != nil || playlist.Name != "List" {
		t.Fatalf("playlist=%#v err=%v", playlist, apiErr)
	}
	if listTracks, apiErr := client.PlaylistTracks(context.Background(), "p1"); apiErr != nil || len(listTracks) != 1 || listTracks[0].ID != "t9" {
		t.Fatalf("playlist tracks=%#v err=%v", listTracks, apiErr)
	}
	if track, apiErr := client.Track(context.Background(), "t1", PlaybackAudioFormat); apiErr != nil || !strings.Contains(track.Audio, "format=mp32") {
		t.Fatalf("playback track=%#v err=%v", track, apiErr)
	}
	// Jamendo reports failures inside an HTTP 200 body, so each body code must
	// map to its own stable api.Error code.
	for _, test := range []struct {
		mode string
		want string
	}{
		{mode: "invalid-client", want: api.CodeAuthorizationFailed},
		{mode: "suspended", want: api.CodeAuthorizationFailed},
		{mode: "rate-limited", want: api.CodeSearchFailed},
		{mode: "bad-request", want: api.CodeSearchFailed},
		{mode: "malformed", want: api.CodeSearchFailed},
		{mode: "http-error", want: api.CodeSearchFailed},
	} {
		mode = test.mode
		_, apiErr := client.SearchTracks(context.Background(), "lofi", 3)
		if apiErr == nil || apiErr.Code != test.want {
			t.Fatalf("mode %q: err=%v, want code %q", test.mode, apiErr, test.want)
		}
		if apiErr.Code == api.CodeSearchFailed && strings.Contains(apiErr.Message, "private upstream body") {
			t.Fatalf("mode %q leaked the upstream body: %v", test.mode, apiErr)
		}
		if apiErr.Details != nil {
			if _, leaked := apiErr.Details["client_id"]; leaked {
				t.Fatalf("mode %q leaked the credential in details", test.mode)
			}
		}
	}

	// A missing track is a reference problem, not an empty success.
	mode = "empty"
	if _, apiErr := client.Track(context.Background(), "missing", PlaybackAudioFormat); apiErr == nil || apiErr.Code != api.CodeInvalidReference {
		t.Fatalf("missing track err=%v", apiErr)
	}
	if _, apiErr := client.Track(context.Background(), "  ", ""); apiErr == nil || apiErr.Code != api.CodeInvalidReference {
		t.Fatalf("empty track id err=%v", apiErr)
	}
}

func TestPlaylistTracksPaginatesWithoutTruncation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset := r.URL.Query().Get("offset")
		var tracks []string
		switch offset {
		case "0":
			tracks = make([]string, playlistPageSize)
			for i := range tracks {
				tracks[i] = trackJSON(fmt.Sprintf("t%03d", i))
			}
		case fmt.Sprint(playlistPageSize):
			tracks = []string{trackJSON("last")}
		default:
			t.Fatalf("unexpected offset %q", offset)
		}
		_, _ = fmt.Fprintf(w, `{"headers":{"status":"success","code":0},"results":[%s]}`, strings.Join(tracks, ","))
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: server.Client(), Credentials: credentials("test-client")}

	tracks, apiErr := client.PlaylistTracks(context.Background(), "p1")
	if apiErr != nil || len(tracks) != playlistPageSize+1 || tracks[len(tracks)-1].ID != "last" {
		t.Fatalf("tracks=%d last=%#v err=%v", len(tracks), tracks[len(tracks)-1], apiErr)
	}
}

// Jamendo answers a valid read with an empty result set for 30-50% of requests.
// The retry layer is the only thing standing between that and "no results" /
// "track not found" in the UI, so it is pinned here.
func TestEmptyResultIsRetriedThenBelieved(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[]}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"headers":{"status":"success","code":0},"results":[%s]}`, trackJSON("t1"))
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: server.Client(), Credentials: credentials("test-client")}

	tracks, apiErr := client.SearchTracks(context.Background(), "jazz", 5)
	if apiErr != nil || len(tracks) != 1 || tracks[0].ID != "t1" {
		t.Fatalf("tracks=%d err=%v calls=%d", len(tracks), apiErr, calls)
	}
	if calls != 3 {
		t.Fatalf("calls=%d, want 3: it stops at the first non-empty attempt", calls)
	}
}

func TestPersistentlyEmptyResultIsAccepted(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[]}`))
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: server.Client(), Credentials: credentials("test-client")}

	tracks, apiErr := client.SearchTracks(context.Background(), "zzz-no-match", 5)
	if apiErr != nil || len(tracks) != 0 {
		t.Fatalf("tracks=%d err=%v", len(tracks), apiErr)
	}
	if calls != readAttempts {
		t.Fatalf("calls=%d, want %d (bounded)", calls, readAttempts)
	}
	// A single-resource lookup may not read the empty reply as "no such id".
	calls = 0
	if _, apiErr := client.Track(context.Background(), "1347774", "mp32"); apiErr == nil || apiErr.Code != api.CodeInvalidReference {
		t.Fatalf("track err=%v, want %s", apiErr, api.CodeInvalidReference)
	}
	if calls != readAttempts {
		t.Fatalf("lookup calls=%d, want %d", calls, readAttempts)
	}
}

func TestBodyErrorCodeIsNotRetried(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"headers":{"status":"failed","code":5},"results":[]}`))
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: server.Client(), Credentials: credentials("test-client")}

	if _, apiErr := client.SearchTracks(context.Background(), "jazz", 5); apiErr == nil || apiErr.Code != api.CodeAuthorizationFailed {
		t.Fatalf("err=%v", apiErr)
	}
	if calls != 1 {
		t.Fatalf("calls=%d, want 1: a rejected client_id is not flakiness", calls)
	}
}

func TestTransportFailureIsRetried(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			hijack, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Fatal(err)
			}
			_ = hijack.Close() // reset the connection mid-response
			return
		}
		_, _ = fmt.Fprintf(w, `{"headers":{"status":"success","code":0},"results":[%s]}`, trackJSON("t1"))
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: server.Client(), Credentials: credentials("test-client")}

	tracks, apiErr := client.SearchTracks(context.Background(), "jazz", 5)
	if apiErr != nil || len(tracks) != 1 || calls != 2 {
		t.Fatalf("tracks=%d err=%v calls=%d", len(tracks), apiErr, calls)
	}
}

// A flaky empty page must not be mistaken for the end of a playlist, which
// would silently truncate the queue.
func TestSpuriousEmptyPageDoesNotTruncateAPlaylist(t *testing.T) {
	page := make([]string, playlistPageSize)
	for i := range page {
		page[i] = trackJSON(fmt.Sprintf("t%03d", i))
	}
	second := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("offset") {
		case "0":
			_, _ = fmt.Fprintf(w, `{"headers":{"status":"success","code":0},"results":[%s]}`, strings.Join(page, ","))
		case fmt.Sprint(playlistPageSize):
			second++
			if second == 1 {
				_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[]}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"headers":{"status":"success","code":0},"results":[%s]}`, trackJSON("last"))
		default:
			t.Fatalf("unexpected offset %q", r.URL.Query().Get("offset"))
		}
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: server.Client(), Credentials: credentials("test-client")}

	tracks, apiErr := client.PlaylistTracks(context.Background(), "p1")
	if apiErr != nil || len(tracks) != playlistPageSize+1 || tracks[len(tracks)-1].ID != "last" {
		t.Fatalf("tracks=%d err=%v", len(tracks), apiErr)
	}
}

func TestPlaylistTracksRejectsANonAdvancingPage(t *testing.T) {
	page := make([]string, playlistPageSize)
	for i := range page {
		page[i] = trackJSON(fmt.Sprintf("t%03d", i))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"headers":{"status":"success","code":0},"results":[%s]}`, strings.Join(page, ","))
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: server.Client(), Credentials: credentials("test-client")}

	_, apiErr := client.PlaylistTracks(context.Background(), "p1")
	if apiErr == nil || apiErr.Code != api.CodeSearchFailed {
		t.Fatalf("err=%v, want %s", apiErr, api.CodeSearchFailed)
	}
}

// An unconfigured client must fail before it touches the network: the setup
// hint is the whole user-visible contract for a missing client_id.
func TestUnconfiguredClientDoesNotCallTheAPI(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"headers":{"status":"success","code":0},"results":[]}`))
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, HTTP: server.Client()}
	apiErr := errorOf(client.SearchTracks(context.Background(), "lofi", 3))
	if apiErr == nil || apiErr.Code != api.CodeAuthorizationFailed {
		t.Fatalf("err=%v, want %s", apiErr, api.CodeAuthorizationFailed)
	}
	if !strings.Contains(apiErr.Message, "lilt jamendo setup") {
		t.Fatalf("message %q does not point at the setup command", apiErr.Message)
	}
	if called {
		t.Fatal("an unconfigured client sent a request")
	}
}

func TestContextCancellationIsNotAFakeSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, HTTP: server.Client(), Credentials: credentials("test-client")}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	apiErr := errorOf(client.SearchTracks(ctx, "lofi", 3))
	if apiErr == nil || apiErr.Code != api.CodeSearchFailed {
		t.Fatalf("err=%v, want %s", apiErr, api.CodeSearchFailed)
	}
}

// trackJSON is one upstream track fixture. Jamendo sends numeric fields as
// strings, which the projection has to survive.
func trackJSON(id string) string {
	return fmt.Sprintf(`{"id":%q,"name":"Track","duration":272,"artist_id":"a1","artist_name":"Artist","album_name":"Album","image":"https://art.example/%s.jpg","audio":"https://media.example/%s.mp3?format=mp31","shareurl":"https://www.jamendo.com/track/%s","license_ccurl":"http://creativecommons.org/licenses/by-nc-nd/3.0/"}`, id, id, id, id)
}

func errorOf(_ []Track, apiErr *api.Error) *api.Error { return apiErr }
