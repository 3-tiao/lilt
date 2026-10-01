package api

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRegistryDescribeCatalog(t *testing.T) {
	registry := NewRegistry()
	description := registry.Describe()
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

func TestPlaybackStartCatalogDeclaresFailureClasses(t *testing.T) {
	for _, command := range NewRegistry().Describe().Commands {
		if command.Name != "playback.play" && command.Name != "playback.playSongs" {
			continue
		}
		for _, code := range []string{
			CodeInvalidRequest, CodeInvalidReference, CodeSourceUnavailable, CodeSourceMismatch,
			CodeUnsupportedCommand, CodeAuthorizationRequired, CodeEngineRestarting,
			CodePartialFailure, CodePlaybackError, CodeOperationOutcomeUnknown,
		} {
			if !slices.Contains(command.Errors, code) {
				t.Errorf("%s does not declare %s", command.Name, code)
			}
		}
		if command.Name == "playback.play" && !slices.Contains(command.Errors, CodePreviewUnavailable) {
			t.Error("playback.play does not declare preview_unavailable")
		}
	}
}

func TestMusicKitControlBudgetsPermitSettlingBeforeReportingFailure(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{"playback.toggle", "playback.resume", "playback.next", "playback.previous", "queue.jump"} {
		if budget := registry.Timeout(name); budget < 15*time.Second {
			t.Errorf("%s budget = %s, less than MusicKit's 12s start wait plus RPC overhead", name, budget)
		}
	}
	if budget := registry.Timeout("playback.pause"); budget < 8*time.Second {
		t.Errorf("pause budget = %s, less than the 4s confirmation plus RPC overhead", budget)
	}
	for _, command := range registry.Describe().Commands {
		switch command.Name {
		case "playback.pause", "playback.toggle", "playback.resume", "playback.next", "playback.previous", "queue.jump":
			if !slices.Contains(command.Errors, CodePlaybackError) {
				t.Errorf("%s does not declare playback_error", command.Name)
			}
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

// The side-effect classification must not drift from the catalog: every name in
// queryCommands is registered, and each registered command's Query flag agrees
// with the declaration. A new command defaults to "has side effects", so
// forgetting to classify one fails closed (it requires an epoch) instead of
// silently becoming epoch-optional.
func TestQueryClassificationMatchesCatalog(t *testing.T) {
	registry := NewRegistry()
	registered := map[string]bool{}
	for _, name := range registry.List() {
		registered[name] = true
		if got, want := registry.Query(name), queryCommands[name]; got != want {
			t.Errorf("command %q query=%v, want %v", name, got, want)
		}
	}
	for name := range queryCommands {
		if !registered[name] {
			t.Errorf("queryCommands lists %q, which is not in the catalog", name)
		}
	}
	if !registered["session.status"] || !registered["ui.set"] {
		t.Fatal("expected session.status and ui.set in the catalog")
	}
}

// api.describe tells agents which commands may omit the epoch, so the flag must
// reach the wire description.
func TestDescribeExposesQueryClassification(t *testing.T) {
	registry := NewRegistry()
	for _, command := range registry.Describe().Commands {
		switch command.Name {
		case "session.status":
			if !command.Query {
				t.Error("session.status is described as having side effects")
			}
		case "playback.next", "ui.set", "session.shutdown":
			if command.Query {
				t.Errorf("%s is described as a query", command.Name)
			}
		}
	}
}

// Only serialized side-effecting commands advertise an admission budget: a
// query and a concurrent command never wait for the mutation slot, so
// describing either with a budget would be misleading.
func TestDescribeExposesAdmissionOnlyForSerializedMutations(t *testing.T) {
	registry := NewRegistry()
	for _, command := range registry.Describe().Commands {
		serialized := !command.Query && !command.Concurrent
		switch {
		case !serialized && command.AdmissionMS != 0:
			t.Errorf("%s is not serialized but advertises admission %dms", command.Name, command.AdmissionMS)
		case serialized && command.AdmissionMS <= 0:
			t.Errorf("%s is serialized with side effects but advertises no admission budget", command.Name)
		}
	}
	if !registry.Concurrent("radio.search") || !registry.Concurrent("discovery.search") {
		t.Error("discovery and radio search must stay outside the mutation slot")
	}
	if registry.Concurrent("playback.play") {
		t.Error("playback.play must be serialized")
	}
}

// Every mutation declares server_busy, and shutdown's admission budget is
// derived to outlast the longest command so draining does not time out first.
func TestMutationBudgetsAndDeclaredErrors(t *testing.T) {
	registry := NewRegistry()
	var longest time.Duration
	for _, name := range registry.List() {
		if !registry.Query(name) && registry.Timeout(name) > longest {
			longest = registry.Timeout(name)
		}
	}
	for _, name := range registry.List() {
		if registry.Query(name) {
			continue
		}
		definition, ok := registry.Lookup(name)
		if !ok {
			t.Fatalf("mutation %q is missing from the registry", name)
		}
		if !slices.Contains(definition.Errors, CodeServerBusy) {
			t.Errorf("mutation %q does not declare %s", name, CodeServerBusy)
		}
	}
	shutdown := registry.Admission("session.shutdown")
	if shutdown <= longest {
		t.Errorf("shutdown admission %s must outlast the longest command budget %s", shutdown, longest)
	}
	if got := registry.Admission("playback.next"); got != DefaultAdmission() {
		t.Errorf("ordinary mutation admission = %s, want %s", got, DefaultAdmission())
	}
}

// Concurrent commands are declared once, and the declaration is what both the
// dispatcher and api.describe report: a command cannot be "concurrent" in one
// place and queued in another.
func TestConcurrentClassificationMatchesCatalog(t *testing.T) {
	registry := NewRegistry()
	registered := map[string]bool{}
	for _, command := range registry.Describe().Commands {
		registered[command.Name] = true
		switch {
		case command.Concurrent && command.AdmissionMS != 0:
			t.Errorf("%s is concurrent but advertises admission %dms", command.Name, command.AdmissionMS)
		case !command.Concurrent && !command.Query && command.AdmissionMS <= 0:
			t.Errorf("%s is neither a query nor concurrent but has no admission budget", command.Name)
		}
		// Draining follows the same classification: only side-effect-free
		// commands (and shutdown itself) stay available.
		if want := command.Query || command.Name == "session.shutdown"; registry.ServeWhileDraining(command.Name) != want {
			t.Errorf("%s ServeWhileDraining=%v, want %v", command.Name, registry.ServeWhileDraining(command.Name), want)
		}
		if !command.Query && !slices.Contains(definitionErrors(t, registry, command.Name), CodeServerBusy) {
			t.Errorf("mutation %q does not declare %s", command.Name, CodeServerBusy)
		}
	}
	for name := range concurrentCommands {
		if !registered[name] {
			t.Errorf("concurrentCommands lists %q, which is not in the catalog", name)
		}
	}
	// Queries never need an epoch and never carry server_busy as a precondition;
	// concurrent mutations do carry it (the ledger cap still applies).
	for _, name := range []string{"session.status", "state.get", "radio.options"} {
		if slices.Contains(definitionErrors(t, registry, name), CodeServerBusy) {
			t.Errorf("query %q declares %s", name, CodeServerBusy)
		}
	}
	if !slices.Contains(definitionErrors(t, registry, "radio.search"), CodeServerBusy) {
		t.Error("radio.search is a mutation and must declare server_busy")
	}
}

func definitionErrors(t *testing.T, registry *Registry, name string) []string {
	t.Helper()
	definition, ok := registry.Lookup(name)
	if !ok {
		t.Fatalf("command %q is missing from the registry", name)
	}
	return definition.Errors
}

// session.shutdown's queue budget must be computed from the finished catalog,
// not hand-kept: when any execution budget grows, `lilt quit` has to keep
// draining instead of reporting server_busy after a stale constant.
func TestShutdownAdmissionWaitIsDerivedFromTheCatalog(t *testing.T) {
	registry := NewRegistry()
	longest := time.Duration(0)
	for _, name := range registry.List() {
		if name == "session.shutdown" {
			continue
		}
		if budget := registry.Timeout(name); budget > longest {
			longest = budget
		}
	}
	want := longest + defaultAdmissionWait + registry.Timeout("session.shutdown")
	if got := registry.Admission("session.shutdown"); got != want {
		t.Fatalf("session.shutdown admission = %s, want longest execution budget %s + ordinary admission %s + own execution %s",
			got, longest, defaultAdmissionWait, registry.Timeout("session.shutdown"))
	}
}
