package api

import (
	"encoding/json"
	"testing"
)

func TestValidateParamsRejectsUnknownFields(t *testing.T) {
	registry := NewRegistry()
	_, err := registry.ValidateParams("playback.play", json.RawMessage(`{"ref":"apple-music:song:1","bogus":true}`))
	if err == nil || err.Code != CodeInvalidRequest {
		t.Fatalf("err = %v, want invalid_request", err)
	}
}

func TestValidateParamsRequiresMandatoryFields(t *testing.T) {
	registry := NewRegistry()
	for _, test := range []struct {
		command string
		params  string
	}{
		{"playback.play", `{}`},
		{"queue.remove", `{}`},
		{"queue.add", `{"ref":"apple-music:song:1"}`},
		{"favorites.set", `{"favorited":true}`},
	} {
		if _, err := registry.ValidateParams(test.command, json.RawMessage(test.params)); err == nil || err.Code != CodeInvalidRequest {
			t.Fatalf("%s %s: err = %v, want invalid_request", test.command, test.params, err)
		}
	}
}

func TestValidateParamsRejectsBadTypesAndEnums(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.ValidateParams("playback.play", json.RawMessage(`{"ref":"apple-music:song:1","shuffle":"yes"}`)); err == nil || err.Code != CodeInvalidRequest {
		t.Fatalf("boolean type err = %v", err)
	}
	if _, err := registry.ValidateParams("playback.play", json.RawMessage(`{"ref":"apple-music:song:1","repeat":"sometimes"}`)); err == nil || err.Code != CodeInvalidRequest {
		t.Fatalf("repeat enum err = %v", err)
	}
	if _, err := registry.ValidateParams("playback.playSongs", json.RawMessage(`{"refs":"apple-music:song:1"}`)); err == nil || err.Code != CodeInvalidRequest {
		t.Fatalf("refs-as-string err = %v", err)
	}
	if _, err := registry.ValidateParams("queue.add", json.RawMessage(`{"ref":"apple-music:song:1","position":"middle"}`)); err == nil || err.Code != CodeInvalidRequest {
		t.Fatalf("position enum err = %v", err)
	}
}

func TestValidateParamsAcceptsAndNormalizes(t *testing.T) {
	registry := NewRegistry()
	normalized, err := registry.ValidateParams("playback.play", json.RawMessage(`{"repeat":"all","ref":"apple-music:song:1"}`))
	if err != nil {
		t.Fatalf("valid params rejected: %v", err)
	}
	// Closed schema normalizes to sorted keys regardless of input order.
	if string(normalized) != `{"ref":"apple-music:song:1","repeat":"all"}` {
		t.Fatalf("normalized = %s", normalized)
	}
	if _, err := registry.ValidateParams("does.not.exist", nil); err == nil || err.Code != CodeUnknownCommand {
		t.Fatalf("unknown command err = %v", err)
	}
	if out, err := registry.ValidateParams("sources.list", nil); err != nil || out != nil {
		t.Fatalf("nil schema: out=%s err=%v", out, err)
	}
}
