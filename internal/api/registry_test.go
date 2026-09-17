package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRegistryDescribeCatalog(t *testing.T) {
	registry := NewRegistry()
	description := registry.Describe()
	if description.APIVersion != Version {
		t.Fatalf("apiVersion = %d, want %d", description.APIVersion, Version)
	}
	if len(description.Commands) == 0 {
		t.Fatal("no commands registered")
	}
	seen := map[string]bool{}
	for _, command := range description.Commands {
		if seen[command.Name] {
			t.Fatalf("duplicate command %q", command.Name)
		}
		seen[command.Name] = true
		if command.Name != "session.watch" && command.TimeoutMS <= 0 {
			t.Errorf("command %q has non-positive timeout", command.Name)
		}
		if len(command.ParamsSchema) == 0 {
			continue
		}
		var schema struct {
			Type                 string                     `json:"type"`
			AdditionalProperties *bool                      `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(command.ParamsSchema, &schema); err != nil {
			t.Errorf("command %q params schema is not valid JSON: %v", command.Name, err)
			continue
		}
		if schema.Type != "object" {
			t.Errorf("command %q params schema type = %q", command.Name, schema.Type)
		}
		if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
			t.Errorf("command %q params schema must set additionalProperties:false", command.Name)
		}
	}
	for _, required := range []string{
		"api.describe", "sources.list", "playback.play", "playback.playSongs",
		"queue.add", "discovery.search", "radio.search", "session.status",
		"session.watch", "session.shutdown", "authorization.begin",
	} {
		if !seen[required] {
			t.Errorf("command %q missing from catalog", required)
		}
	}
}

func TestDescribeRefsResolveToModels(t *testing.T) {
	registry := NewRegistry()
	description := registry.Describe()
	for _, command := range description.Commands {
		for _, model := range refTargets(command.ParamsSchema) {
			if _, ok := description.Models[model]; !ok {
				t.Errorf("command %q references unknown model %q", command.Name, model)
			}
		}
	}
}

func refTargets(raw json.RawMessage) []string {
	var found []string
	needle := "#/models/"
	text := string(raw)
	for {
		index := strings.Index(text, needle)
		if index < 0 {
			return found
		}
		text = text[index+len(needle):]
		end := strings.IndexAny(text, "\"")
		if end < 0 {
			return found
		}
		found = append(found, text[:end])
		text = text[end:]
	}
}

func TestRegistryHandlerBinding(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.Handler("playback.play"); err == nil || err.Code != CodeUnsupportedCommand {
		t.Fatalf("unbound handler error = %v, want unsupported_command", err)
	}
	registry.Bind("playback.play", func(_ context.Context, _ json.RawMessage) (any, *Error) {
		return "ok", nil
	})
	handler, err := registry.Handler("playback.play")
	if err != nil {
		t.Fatal(err)
	}
	value, handlerErr := handler(nil, nil)
	if handlerErr != nil || value != "ok" {
		t.Fatalf("handler = %v, %v", value, handlerErr)
	}
	if _, err := registry.Handler("nope"); err == nil || err.Code != CodeUnknownCommand {
		t.Fatalf("unknown command error = %v, want unknown_command", err)
	}
}
