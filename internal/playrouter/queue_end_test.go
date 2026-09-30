package playrouter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/appleweb"
	"github.com/3-tiao/lilt/internal/server"
	"github.com/3-tiao/lilt/internal/state"
)

// newManuallySampledPlayer uses the real dispatcher with explicit sampler input,
// so tests control ordering without racing the production ticker.
func newManuallySampledPlayer(t *testing.T, apple *fakeApple) *Player {
	t.Helper()
	player := &Player{
		streams: newFakeStreams(), apple: apple,
		updates: make(chan core.PlaybackStateUpdate),
		wake:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go player.dispatch()
	t.Cleanup(func() {
		_ = player.Close()
		<-player.done
	})
	return player
}

// The playback path only reads Song and Authorized; unexpected discovery calls
// through the embedded interface fail instead of silently returning empty data.
type endEventCatalog struct{ server.PageCatalog }

func (endEventCatalog) Authorized(context.Context) (bool, error) { return true, nil }

func (endEventCatalog) Song(_ context.Context, id string) (appleweb.CatalogSong, error) {
	return appleweb.CatalogSong{
		ID: id, Title: "Fixture " + id, DurationMs: 204000,
		URL: "https://music.apple.com/cn/song/fixture/" + id,
	}, nil
}

// Status reads at EOF must not keep a real server-owned queue on its old item.
func TestAppleStatusReadBeforeEndNotificationAdvancesServerQueue(t *testing.T) {
	apple := newFakeApple()
	player := newManuallySampledPlayer(t, apple)
	dir, err := os.MkdirTemp("/tmp", "lilt-end-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	srv, err := server.Start(server.Options{
		SocketPath: socket, Store: state.NewMemory(),
		AudioEngineFactory: func() (server.AudioEngine, error) { return player, nil },
		Providers: []server.ContentProvider{
			server.NewAppleWebProvider(endEventCatalog{}, func() error { return nil }),
		},
	})
	if err != nil {
		t.Fatalf("server.Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, watcher, err := api.Watch(ctx, socket, []string{"playback"}, false)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	defer func() { _ = watcher.Close() }()

	status := func() api.PlaybackState {
		t.Helper()
		response, err := api.Command(ctx, socket, "session.status", map[string]any{"includeQueue": true})
		if err != nil || !response.OK {
			t.Fatalf("status response=%+v err=%v", response, err)
		}
		var value api.PlaybackState
		if err := json.Unmarshal(response.Data, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	response, err := api.Command(ctx, socket, "playback.playSongs", map[string]any{
		"refs": []string{"apple-music:song:111", "apple-music:song:222", "apple-music:song:333"},
	})
	if err != nil || !response.OK {
		t.Fatalf("playSongs response=%+v err=%v", response, err)
	}

	for index, id := range []string{"111", "222"} {
		apple.setState(appleweb.State{Ready: true, Status: "playing", ItemID: id, Duration: 204, Position: 1})
		if value := status(); value.Status != "playing" || value.Track == nil || value.Track.ProviderID != id {
			t.Fatalf("item %s did not settle: %+v", id, value)
		}
		finished := appleweb.State{Ready: true, Status: "completed", ItemID: id, Duration: 204, Position: 204}
		apple.setState(finished)
		if value := status(); value.Status != "ended" {
			t.Fatalf("status did not observe EOF before the notification: %+v", value)
		}
		player.mu.Lock()
		epoch, generation, session := player.appleEpoch, player.appleGeneration, player.appleSession
		player.mu.Unlock()
		player.publishApple(finished, epoch, generation, session)

		wantID := []string{"222", "333"}[index]
		advanced := false
		for !advanced {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					t.Fatal("watch closed before queue advancement")
				}
				if event.Event != "playback.changed" {
					continue
				}
				var data struct {
					State api.PlaybackState `json:"state"`
				}
				if err := json.Unmarshal(event.Data, &data); err != nil {
					t.Fatal(err)
				}
				advanced = data.State.Track != nil && data.State.Track.ProviderID == wantID && data.State.QueueIndex == index+1
			case <-ctx.Done():
				t.Fatalf("status read consumed EOF for %s; queue never advanced to %s", id, wantID)
			}
		}
		// A delayed duplicate from the old item must not skip the new one.
		player.publishApple(finished, epoch, generation, session)
	}
	apple.mu.Lock()
	played := append([]string(nil), apple.played...)
	apple.mu.Unlock()
	if len(played) != 3 || played[0] != "111" || played[1] != "222" || played[2] != "333" {
		t.Fatalf("played catalog ids=%v, want each queue item exactly once", played)
	}
}
