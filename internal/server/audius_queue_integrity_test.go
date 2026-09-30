package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/audius"
)

func audiusIntegrityTracks() []audius.Track {
	return []audius.Track{
		{ID: "c", Title: "C", IsStreamable: true, Duration: 100},
		{ID: "a", Title: "A", IsStreamable: true, Duration: 100},
		{ID: "b", Title: "B", IsStreamable: true, Duration: 100},
	}
}

func TestAudiusPreparePlaybackRejectsIncompleteExplicitQueue(t *testing.T) {
	for _, id := range []string{"a", "b", "c"} {
		for _, failure := range []string{"missing", "unplayable"} {
			t.Run(id+"/"+failure, func(t *testing.T) {
				tracks := audiusIntegrityTracks()
				for i, track := range tracks {
					if track.ID != id {
						continue
					}
					if failure == "missing" {
						tracks = append(tracks[:i], tracks[i+1:]...)
					} else {
						tracks[i].IsStreamable = false
					}
					break
				}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/tracks" {
						t.Errorf("prepare made unexpected request: %s", r.URL.Path)
						http.NotFound(w, r)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": tracks})
				}))
				defer upstream.Close()
				provider := audiusProvider{client: audius.Client{BaseURL: upstream.URL, HTTP: upstream.Client()}}
				plan, apiErr := provider.PreparePlayback(context.Background(), PlaybackRequest{
					References: []api.Reference{
						{Source: api.SourceAudius, Kind: api.KindSong, ID: "a"},
						{Source: api.SourceAudius, Kind: api.KindSong, ID: "b"},
						{Source: api.SourceAudius, Kind: api.KindSong, ID: "c"},
					},
					StartIndex: 1,
				})
				if apiErr == nil || apiErr.Code != api.CodeInvalidReference || plan != nil {
					t.Fatalf("prepare = plan %+v, error %+v; want no plan and invalid_reference", plan, apiErr)
				}
			})
		}
	}
}

func TestAudiusIncompleteQueueDoesNotStartAnotherTrack(t *testing.T) {
	for _, mode := range []struct {
		command  string
		fromHere bool
	}{
		{command: "playback.playSongs"},
		{command: "playback.play"},
		{command: "playback.play", fromHere: true},
	} {
		for _, id := range []string{"a", "b", "c"} {
			if mode.fromHere && id == "a" {
				// The caller explicitly discards a before preparation.
				continue
			}
			for _, failure := range []string{"missing", "unplayable"} {
				name := mode.command + "/" + id + "/" + failure
				if mode.fromHere {
					name += "/fromHere"
				}
				t.Run(name, func(t *testing.T) {
					var streamCalls atomic.Int32
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						switch {
						case r.URL.Path == "/tracks/old":
							_ = json.NewEncoder(w).Encode(map[string]any{"data": audius.Track{ID: "old", IsStreamable: true, Duration: 100}})
						case r.URL.Path == "/playlists/p1":
							_, _ = w.Write([]byte(`{"data":{"id":"p1","playlist_name":"Mix"}}`))
						case r.URL.Path == "/playlists/p1/tracks":
							// Discovery offers a complete, playable list; the second
							// metadata lookup changes before queue preparation.
							tracks := audiusIntegrityTracks()
							_ = json.NewEncoder(w).Encode(map[string]any{"data": []audius.Track{tracks[1], tracks[2], tracks[0]}})
						case r.URL.Path == "/tracks":
							tracks := audiusIntegrityTracks()
							for i, track := range tracks {
								if track.ID != id {
									continue
								}
								if failure == "missing" {
									tracks = append(tracks[:i], tracks[i+1:]...)
								} else {
									tracks[i].IsStreamable = false
								}
								break
							}
							_ = json.NewEncoder(w).Encode(map[string]any{"data": tracks})
						case strings.HasSuffix(r.URL.Path, "/stream"):
							streamCalls.Add(1)
							_, _ = w.Write([]byte(`{"data":"https://media.invalid/track"}`))
						default:
							_, _ = w.Write([]byte(`{"data":{}}`))
						}
					}))
					defer upstream.Close()
					driver := &recordingURLDriver{}
					_, socket, _ := startAudiusPlaybackServer(t, upstream, driver)
					if response := call(t, socket, "playback.play", map[string]any{"ref": "audius:song:old"}); !response.OK {
						t.Fatalf("initial play: %+v", response.Error)
					}
					params := map[string]any{"refs": []string{"audius:song:a", "audius:song:b", "audius:song:c"}, "startIndex": 1}
					if mode.command == "playback.play" {
						params = map[string]any{"ref": "audius:playlist:p1", "startAt": 1, "fromHere": mode.fromHere}
					}
					response := call(t, socket, mode.command, params)
					if response.OK || response.Error == nil || response.Error.Code != api.CodeInvalidReference {
						t.Errorf("request = %+v, data %s; want invalid_reference", response.Error, response.Data)
					} else {
						data, err := json.Marshal(response.Error.Details["state"])
						if err != nil {
							t.Fatal(err)
						}
						var state api.PlaybackState
						if err := json.Unmarshal(data, &state); err != nil {
							t.Fatal(err)
						}
						if state.Status != "stopped" || len(state.Queue) != 0 || state.Track != nil {
							t.Errorf("failure state = %+v; want stopped, empty queue, no track", state)
						}
					}
					if got := driver.count(); got != 1 {
						t.Errorf("PlayURL calls = %d; want only the initial track", got)
					}
					if got := streamCalls.Load(); got != 1 {
						t.Errorf("stream resolutions = %d; want only the initial track", got)
					}
					if driver.stopCount() == 0 {
						t.Error("failed new start did not stop the old playback")
					}
					status := call(t, socket, "session.status", map[string]any{"includeQueue": true})
					var state api.PlaybackState
					if !status.OK {
						t.Fatalf("status: %+v", status.Error)
					}
					if err := json.Unmarshal(status.Data, &state); err != nil {
						t.Fatal(err)
					}
					if state.Status != "stopped" || len(state.Queue) != 0 || state.Track != nil {
						t.Errorf("status = %+v; want stopped, empty queue, no track", state)
					}
				})
			}
		}
	}
}

func TestAudiusPreparePlaybackPreservesDuplicatesAndSelectedTrack(t *testing.T) {
	for _, fromHere := range []bool{false, true} {
		t.Run(map[bool]string{false: "whole queue", true: "from here"}[fromHere], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/tracks" {
					t.Errorf("prepare made unexpected request: %s", r.URL.Path)
				}
				tracks := append(audiusIntegrityTracks(), audius.Track{ID: "extra", IsStreamable: true})
				_ = json.NewEncoder(w).Encode(map[string]any{"data": tracks})
			}))
			defer upstream.Close()
			provider := audiusProvider{client: audius.Client{BaseURL: upstream.URL, HTTP: upstream.Client()}}
			ids := []string{"a", "b", "a", "c"}
			refs := make([]api.Reference, len(ids))
			for i, id := range ids {
				refs[i] = api.Reference{Source: api.SourceAudius, Kind: api.KindSong, ID: id}
			}
			plan, apiErr := provider.PreparePlayback(context.Background(), PlaybackRequest{References: refs, StartIndex: 1, FromHere: fromHere})
			if apiErr != nil {
				t.Fatal(apiErr)
			}
			wantIndex := 1
			if fromHere {
				ids, wantIndex = ids[1:], 0
			}
			var got []string
			for _, item := range plan.PublicQueue() {
				got = append(got, item.ProviderID)
			}
			if !reflect.DeepEqual(got, ids) || plan.StartIndex() != wantIndex || plan.PublicQueue()[plan.StartIndex()].ProviderID != "b" {
				t.Fatalf("queue %v index %d; want %v index %d selecting b", got, plan.StartIndex(), ids, wantIndex)
			}
		})
	}
}
