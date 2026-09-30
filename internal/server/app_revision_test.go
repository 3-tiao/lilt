package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/3-tiao/lilt/core"
	"github.com/3-tiao/lilt/internal/activity"
	"github.com/3-tiao/lilt/internal/api"
	"github.com/3-tiao/lilt/internal/state"
)

func newAppRevisionServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "activity.sqlite3")
	db, err := activity.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		registry: api.NewRegistry(), dedup: newDedupCache(0, 0), closed: make(chan struct{}),
		store: state.New(filepath.Join(dir, "state.json")), activity: db, activityPath: path,
		watchers: newWatchHub(), logf: func(string, map[string]any) {},
	}
	s.bindHandlers()
	t.Cleanup(func() {
		if s.activity != nil {
			_ = s.activity.Close()
		}
	})
	return s
}

func readAppRevision(t *testing.T, s *Server, requestID string) api.AppState {
	t.Helper()
	response := s.dispatch(api.Request{RequestID: requestID, Command: "state.get"})
	var app api.AppState
	if err := json.Unmarshal(response.Data, &app); err != nil || !response.OK {
		t.Fatalf("state.get = %s, error=%+v, decode=%v", response.Data, response.Error, err)
	}
	return app
}

func TestEveryAppStateStoreCommitAdvancesOneRevision(t *testing.T) {
	s := newAppRevisionServer(t)
	watcher := s.watchers.register(map[string]bool{"state": true})
	defer s.watchers.unregister(watcher)
	item := api.Item{Source: api.SourceAppleMusic, Kind: api.KindSong, ProviderID: "1", Ref: "apple-music:song:1", Title: "Fixture song"}
	for index, operation := range []struct {
		command string
		params  any
	}{
		{"ui.set", map[string]any{"theme": "fixture-theme"}},
		{"favorites.set", map[string]any{"item": item, "favorited": true}},
		{"favorites.add", map[string]any{"ref": item.Ref}},
		{"qualified_play", nil},
		{"history.clear", map[string]any{"confirm": true}},
		{"favorites.remove", map[string]any{"ref": item.Ref}},
		{"activity.reset", map[string]any{"confirm": true}},
	} {
		t.Run(operation.command, func(t *testing.T) {
			before := readAppRevision(t, s, fmt.Sprintf("before-%d", index))
			beforeSequence := s.sequence
			if operation.command == "qualified_play" {
				s.mu.Lock()
				ok := s.recordRecentLocked(&recentOccurrence{
					id: "fixture-occurrence", source: string(api.SourceAppleMusic), qualifiedAt: time.Unix(1700000000, 0),
					item: core.Item{Kind: api.KindSong, ID: "1", Title: "Fixture song"},
				})
				s.mu.Unlock()
				if !ok {
					t.Fatal("qualified play did not commit")
				}
			} else {
				raw, _ := json.Marshal(operation.params)
				response := s.dispatch(api.Request{RequestID: fmt.Sprintf("mutate-%d", index), Command: operation.command, Params: raw})
				if !response.OK {
					t.Fatalf("mutation failed: %+v", response.Error)
				}
			}
			after := readAppRevision(t, s, fmt.Sprintf("after-%d", index))
			if after.Revision != before.Revision+1 {
				t.Errorf("revision before=%d after=%d, want one increment after the durable commit", before.Revision, after.Revision)
			}
			event := waitURLTransition(t, watcher.events)
			var payload struct {
				State api.AppState `json:"state"`
			}
			if err := json.Unmarshal(event.Data, &payload); err != nil {
				t.Fatal(err)
			}
			if event.Event != "state.changed" || event.Sequence != beforeSequence+1 || payload.State.Revision != before.Revision+1 {
				t.Errorf("watch event=%s sequence=%d revision=%d, want state.changed sequence=%d revision=%d", event.Event, event.Sequence, payload.State.Revision, beforeSequence+1, before.Revision+1)
			}
			if payload.State.Revision != after.Revision {
				t.Fatal("watch and state.get disagree")
			}
			switch operation.command {
			case "favorites.set", "favorites.add":
				if len(after.Favorites) != 1 {
					t.Fatalf("favorites=%+v", after.Favorites)
				}
			case "qualified_play":
				if len(after.Recent) != 1 {
					t.Fatalf("recent=%+v", after.Recent)
				}
			case "history.clear":
				if len(after.Recent) != 0 {
					t.Fatalf("cleared recent=%+v", after.Recent)
				}
			case "favorites.remove", "activity.reset":
				if len(after.Favorites) != 0 {
					t.Fatalf("removed favorites=%+v", after.Favorites)
				}
			}
			select {
			case extra := <-watcher.events:
				t.Fatalf("extra state event: %+v", extra)
			default:
			}
		})
	}
}

func TestFailedAppStateMutationsDoNotAdvanceRevisionOrPublishSuccess(t *testing.T) {
	for _, command := range []string{"ui.set", "favorites.set", "favorites.add", "favorites.remove", "qualified_play", "history.clear", "activity.reset"} {
		t.Run(command, func(t *testing.T) {
			s := newAppRevisionServer(t)
			s.stateRevision = 10
			watcher := s.watchers.register(nil)
			defer s.watchers.unregister(watcher)
			item := api.Item{Source: api.SourceAppleMusic, Kind: api.KindSong, ProviderID: "1", Ref: "apple-music:song:1", Title: "Fixture song"}
			var params any
			wantCode := api.CodeStorageUnavailable
			switch command {
			case "ui.set":
				blocked := filepath.Join(t.TempDir(), "state.json")
				if err := os.Mkdir(blocked, 0700); err != nil {
					t.Fatal(err)
				}
				s.store = state.New(blocked)
				params = map[string]any{"theme": "must-not-commit"}
				wantCode = api.CodeStateSaveFailed
			case "activity.reset":
				blocked := filepath.Join(t.TempDir(), "not-a-directory")
				if err := os.WriteFile(blocked, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
				s.activityPath = filepath.Join(blocked, "activity.sqlite3")
				params = map[string]any{"confirm": true}
			default:
				if err := s.activity.Close(); err != nil {
					t.Fatal(err)
				}
				params = map[string]any{"ref": item.Ref}
				if command == "favorites.set" {
					params = map[string]any{"item": item, "favorited": true}
				}
				if command == "history.clear" {
					params = map[string]any{"confirm": true}
				}
			}
			if command == "qualified_play" {
				s.mu.Lock()
				ok := s.recordRecentLocked(&recentOccurrence{id: "failed-occurrence", source: string(api.SourceAppleMusic),
					qualifiedAt: time.Unix(1700000000, 0), item: core.Item{Kind: api.KindSong, ID: "1", Title: "Fixture song"}})
				s.mu.Unlock()
				if ok {
					t.Fatal("failed history write reported success")
				}
			} else {
				raw, _ := json.Marshal(params)
				response := s.dispatch(api.Request{RequestID: "failed-mutation", Command: command, Params: raw})
				if response.Error == nil || response.Error.Code != wantCode {
					t.Fatalf("failure=%+v, want %s", response.Error, wantCode)
				}
			}
			if app := readAppRevision(t, s, "after-failure"); app.Revision != 10 {
				t.Fatalf("failed mutation revision=%d", app.Revision)
			}
			for len(watcher.events) > 0 {
				if event := <-watcher.events; event.Event == "state.changed" {
					t.Fatalf("failed mutation published success: %+v", event)
				}
			}
		})
	}
}

func TestAppStateReloadPreservesDataAndStartsNewRevisionEpoch(t *testing.T) {
	for _, version := range []int{1, 3} {
		t.Run(fmt.Sprintf("state-v%d", version), func(t *testing.T) {
			s := newAppRevisionServer(t)
			path := filepath.Join(filepath.Dir(s.activityPath), "state.json")
			fixture := fmt.Sprintf(`{"version":%d,"theme":"fixture-theme","lastSource":"radio"}`, version)
			if err := os.WriteFile(path, []byte(fixture), 0600); err != nil {
				t.Fatal(err)
			}
			loaded, err := state.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			s.store = loaded
			for id, command := range []api.Request{
				{Command: "ui.set", Params: json.RawMessage(`{"theme":"saved-theme"}`)},
				{Command: "favorites.set", Params: json.RawMessage(`{"item":{"id":"am:1","ref":"apple-music:song:1","source":"apple-music","kind":"song","providerId":"1","title":"Fixture song"},"favorited":true}`)},
			} {
				command.RequestID = fmt.Sprintf("seed-%d", id)
				if response := s.dispatch(command); !response.OK {
					t.Fatalf("seed failed: %+v", response.Error)
				}
			}
			s.mu.Lock()
			ok := s.recordRecentLocked(&recentOccurrence{id: "durable-occurrence", source: string(api.SourceAppleMusic),
				qualifiedAt: time.Unix(1700000000, 0), item: core.Item{Kind: api.KindSong, ID: "1", Title: "Fixture song"}})
			s.mu.Unlock()
			if !ok || s.appState().Revision != 3 {
				t.Fatalf("seed revision=%d, qualified=%t", s.appState().Revision, ok)
			}
			if err := s.activity.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := activity.Open(s.activityPath)
			if err != nil {
				t.Fatal(err)
			}
			s.activity = reopened
			for range 2 {
				prefs, err := state.Load(path)
				if err != nil {
					t.Fatal(err)
				}
				fresh := &Server{store: prefs, activity: reopened, logf: s.logf}
				app := fresh.appState()
				if prefs.Version != 3 || app.Revision != 0 || app.Theme != "saved-theme" || app.LastSource != api.SourceRadio || len(app.Favorites) != 1 || len(app.Recent) != 1 {
					t.Fatalf("reloaded state=%+v, disk version=%d", app, prefs.Version)
				}
			}
		})
	}
}
