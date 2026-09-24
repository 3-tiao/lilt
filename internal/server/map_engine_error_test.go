package server

import (
	"errors"
	"testing"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/player"
)

// playback_error carries stable user copy: raw helper/provider error text (an
// NSError description, a MusicKit dump) goes to details, never into the
// message clients show verbatim (batch 2026-09-23-postaudit M4).
func TestMapEngineErrorKeepsRawTextOutOfMessage(t *testing.T) {
	s := &Server{}
	rpc := &player.RPCError{Code: "music_error", Message: "NSError domain=AppStoreKit code=2 something"}
	err := s.mapEngineError(rpc)
	if err.Code != api.CodePlaybackError {
		t.Fatalf("code = %q, want playback_error", err.Code)
	}
	if err.Message != "Playback could not be started" {
		t.Fatalf("message = %q, want stable copy", err.Message)
	}
	if err.Details["providerCode"] != "music_error" || err.Details["detail"] != rpc.Message {
		t.Fatalf("details = %#v, want providerCode + raw detail", err.Details)
	}

	raw := errors.New("compose failed: boom")
	err = s.mapEngineError(raw)
	if err.Code != api.CodePlaybackError || err.Message != "Playback could not be started" {
		t.Fatalf("fallback error = %+v, want stable playback_error copy", err)
	}
	if err.Details["detail"] != raw.Error() {
		t.Fatalf("fallback details = %#v, want raw detail", err.Details)
	}

	// Other mapped codes keep their provider message: it is already user copy
	// (for example authorization_required guidance).
	rpc2 := &player.RPCError{Code: api.CodeAuthorizationRequired, Message: "Sign in to Apple Music"}
	err = s.mapEngineError(rpc2)
	if err.Code != api.CodeAuthorizationRequired || err.Message != rpc2.Message {
		t.Fatalf("auth error = %+v, want provider message kept", err)
	}
}

func TestStartTimeoutDiagnosticStaysOutOfUserMessage(t *testing.T) {
	s := &Server{}
	rpc := &player.RPCError{Code: api.CodePlaybackError, Message: "MusicKit playback start was not confirmed; startDiagnostic=playing:100,playingAdvance:80,wrongSong:80",
		Debug: map[string]string{"expectedID": "private-library-id", "actualID": "private-catalog-id"}}
	err := s.mapEngineError(rpc)
	if err.Code != api.CodePlaybackError || err.Message != "Playback could not be started" {
		t.Fatalf("start timeout surfaced as user copy: %+v", err)
	}
	if err.Details["detail"] != rpc.Message || err.Details["providerCode"] != api.CodePlaybackError {
		t.Fatalf("diagnostic not preserved in details: %+v", err.Details)
	}
	if _, exists := err.Details["debug"]; exists {
		t.Fatalf("private helper identity leaked into public details: %+v", err.Details)
	}
}

// An append-built queue jump refusal keeps its distinct code and the helper's
// actionable message (batch 2026-09-23-polish P1).
func TestMapEngineErrorKeepsQueueNotJumpable(t *testing.T) {
	s := &Server{}
	rpc := &player.RPCError{
		Code:    "queue_not_jumpable",
		Message: "this queue was built track by track and cannot be jumped; playback continues — start the row from its list instead",
	}
	err := s.mapEngineError(rpc)
	if err.Code != api.CodeQueueNotJumpable {
		t.Fatalf("code = %q, want queue_not_jumpable", err.Code)
	}
	if err.Message != rpc.Message {
		t.Fatalf("message = %q, want the helper's actionable copy kept", err.Message)
	}
	if err.Details["providerCode"] != "queue_not_jumpable" {
		t.Fatalf("details = %#v, want providerCode preserved", err.Details)
	}
}
