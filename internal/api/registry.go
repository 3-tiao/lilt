package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Handler executes one command. It receives validated-by-schema raw params and
// returns the result data or a stable error.
type Handler func(ctx context.Context, params json.RawMessage) (any, *Error)

// Definition is one registered command.
type Definition struct {
	Name         string
	CLI          string
	Timeout      time.Duration
	ParamsSchema json.RawMessage
	ResultSchema string
	// Errors lists the stable error codes this command may return. It is
	// informational metadata for agents; it is not yet validated against the
	// handler's actual behavior, so it can drift until we add a consistency gate.
	Errors []string
	// Description is optional, non-normative guidance for agents; the TUI and
	// other clients MUST NOT branch on it.
	Description string

	handler Handler
}

// Registry is the single source of truth for the command catalog. `api.describe`
// serializes it, and the server dispatches through it, so the two cannot drift.
type Registry struct {
	mu    sync.RWMutex
	order []string
	defs  map[string]*Definition
}

// NewRegistry builds the full command catalog. Handlers are bound later by the
// server; a command with no handler returns unsupported_command.
func NewRegistry() *Registry {
	r := &Registry{defs: make(map[string]*Definition)}
	for _, def := range catalog() {
		r.register(def)
	}
	return r
}

func (r *Registry) register(def *Definition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.defs[def.Name]; !exists {
		r.order = append(r.order, def.Name)
	}
	r.defs[def.Name] = def
}

// Bind attaches the handler for a registered command. Binding an unknown
// command is a programming error and panics so it surfaces at startup.
func (r *Registry) Bind(name string, handler Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	def, ok := r.defs[name]
	if !ok {
		panic(fmt.Sprintf("api: bind unknown command %q", name))
	}
	def.handler = handler
}

// Handler returns the bound handler for name.
func (r *Registry) Handler(name string) (Handler, *Error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.defs[name]
	if !ok {
		return nil, Errorf(CodeUnknownCommand, "unknown command %q", name)
	}
	if def.handler == nil {
		return nil, Errorf(CodeUnsupportedCommand, "command %q is not implemented", name)
	}
	return def.handler, nil
}

// Timeout returns the per-command execution budget.
func (r *Registry) Timeout(name string) time.Duration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if def, ok := r.defs[name]; ok {
		return def.Timeout
	}
	return 5 * time.Second
}

// List returns the command names in registration order.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.order...)
}

// Describe builds the api.describe payload.
func (r *Registry) Describe() ApiDescription {
	r.mu.RLock()
	defer r.mu.RUnlock()
	commands := make([]CommandDescription, 0, len(r.order))
	for _, name := range r.order {
		def := r.defs[name]
		commands = append(commands, CommandDescription{
			Name:         def.Name,
			CLI:          def.CLI,
			TimeoutMS:    def.Timeout.Milliseconds(),
			ParamsSchema: def.ParamsSchema,
			ResultSchema: def.ResultSchema,
			Errors:       append([]string(nil), def.Errors...),
			Description:  def.Description,
		})
	}
	return ApiDescription{
		Commands: commands,
		Models:   modelSchemas(),
		Errors:   ErrorCatalog,
	}
}

// ApiDescription is the machine-readable command catalog.
type ApiDescription struct {
	Commands []CommandDescription       `json:"commands"`
	Models   map[string]json.RawMessage `json:"models"`
	Errors   map[string]string          `json:"errors"`
}

// CommandDescription is one command's metadata.
type CommandDescription struct {
	Name         string          `json:"name"`
	CLI          string          `json:"cli,omitempty"`
	TimeoutMS    int64           `json:"timeoutMs"`
	ParamsSchema json.RawMessage `json:"paramsSchema"`
	ResultSchema string          `json:"resultSchema,omitempty"`
	Errors       []string        `json:"errors,omitempty"`
	// Description is non-normative guidance for agents. Clients MUST NOT branch
	// on it; use the schemas and stable error codes for behavior.
	Description string `json:"description,omitempty"`
}

// schemaProp describes one JSON Schema property. Ref points at a $defs entry.
type schemaProp struct {
	Type   string
	Enum   []string
	Ref    string
	Items  string
	Format string
}

func params(props map[string]schemaProp, required ...string) json.RawMessage {
	return buildObject(props, required, true)
}

func buildObject(props map[string]schemaProp, required []string, closed bool) json.RawMessage {
	properties := map[string]any{}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		spec := props[name]
		property := map[string]any{}
		if spec.Ref != "" {
			property["$ref"] = "#/models/" + spec.Ref
		} else {
			typ := spec.Type
			if typ == "" {
				typ = "object"
			}
			property["type"] = typ
		}
		if len(spec.Enum) > 0 {
			property["enum"] = spec.Enum
		}
		if spec.Items != "" {
			if isPrimitiveType(spec.Items) {
				property["items"] = map[string]any{"type": spec.Items}
			} else {
				property["items"] = map[string]any{"$ref": "#/models/" + spec.Items}
			}
		}
		if spec.Format != "" {
			property["format"] = spec.Format
		}
		properties[name] = property
	}
	object := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		sorted := append([]string(nil), required...)
		sort.Strings(sorted)
		object["required"] = sorted
	}
	if closed {
		object["additionalProperties"] = false
	}
	return toRaw(object)
}

func toRaw(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("api: marshal schema: %v", err))
	}
	return encoded
}

func isPrimitiveType(name string) bool {
	switch name {
	case "string", "integer", "number", "boolean":
		return true
	}
	return false
}
