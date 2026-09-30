package api

import (
	"encoding/json"
)

// paramSchema mirrors the JSON Schema subset the registry emits.
type paramSchema struct {
	Type   string       `json:"type"`
	Enum   []string     `json:"enum"`
	Ref    string       `json:"$ref"`
	Items  *paramSchema `json:"items"`
	Format string       `json:"format"`
	// Object shapes (used when a $ref resolves to a model).
	Properties           map[string]paramSchema `json:"properties"`
	Required             []string               `json:"required"`
	AdditionalProperties *bool                  `json:"additionalProperties"`
}

// ValidateParams validates raw params against the command's registered schema
// and returns a normalized encoding (sorted keys) for fingerprinting. Unknown
// commands return unknown_command; schema violations return invalid_request.
func (r *Registry) ValidateParams(name string, raw json.RawMessage) (json.RawMessage, *Error) {
	r.mu.RLock()
	def, ok := r.defs[name]
	r.mu.RUnlock()
	if !ok {
		return nil, Errorf(CodeUnknownCommand, "unknown command %q", name)
	}
	if def.ParamsSchema == nil {
		return nil, nil
	}
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return nil, Errorf(CodeInvalidRequest, "params must be an object")
	}
	var schema paramSchema
	if err := json.Unmarshal(def.ParamsSchema, &schema); err != nil {
		return nil, Errorf(CodeInvalidRequest, "command schema is invalid")
	}
	models := modelSchemas()
	if err := validateObject(values, schema, models, 0); err != nil {
		return nil, err
	}
	normalized, err := json.Marshal(values)
	if err != nil {
		return nil, Errorf(CodeInvalidRequest, "params could not be normalized")
	}
	return normalized, nil
}

const maxSchemaDepth = 12

func validateObject(values map[string]json.RawMessage, schema paramSchema, models map[string]json.RawMessage, depth int) *Error {
	if depth > maxSchemaDepth {
		return nil
	}
	for _, required := range schema.Required {
		if _, ok := values[required]; !ok {
			return Errorf(CodeInvalidRequest, "missing required param %q", required)
		}
	}
	closed := schema.AdditionalProperties == nil || !*schema.AdditionalProperties
	for key, value := range values {
		spec, ok := schema.Properties[key]
		if !ok {
			if closed {
				return Errorf(CodeInvalidRequest, "unknown param %q", key)
			}
			continue
		}
		if err := validateValue(value, spec, models, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func validateValue(raw json.RawMessage, spec paramSchema, models map[string]json.RawMessage, depth int) *Error {
	if spec.Ref != "" {
		modelName := refModelName(spec.Ref)
		if modelName == "" {
			return nil
		}
		model, ok := models[modelName]
		if !ok {
			return nil
		}
		var resolved paramSchema
		if err := json.Unmarshal(model, &resolved); err != nil {
			return nil
		}
		return validateValue(raw, resolved, models, depth+1)
	}
	typ := spec.Type
	if typ == "" {
		typ = "object"
	}
	switch typ {
	case "string":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return Errorf(CodeInvalidRequest, "param must be a string")
		}
		if len(spec.Enum) > 0 && !containsString(spec.Enum, value) {
			return Errorf(CodeInvalidRequest, "value %q is not allowed", value)
		}
	case "boolean":
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return Errorf(CodeInvalidRequest, "param must be a boolean")
		}
	case "integer":
		var value json.Number
		if err := json.Unmarshal(raw, &value); err != nil {
			return Errorf(CodeInvalidRequest, "param must be an integer")
		}
		if _, err := value.Int64(); err != nil {
			return Errorf(CodeInvalidRequest, "param must be an integer")
		}
	case "number":
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil {
			return Errorf(CodeInvalidRequest, "param must be a number")
		}
	case "array":
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return Errorf(CodeInvalidRequest, "param must be an array")
		}
		if spec.Items != nil {
			for _, item := range values {
				if err := validateValue(item, *spec.Items, models, depth+1); err != nil {
					return err
				}
			}
		}
	case "object":
		if spec.Properties != nil {
			var values map[string]json.RawMessage
			if err := json.Unmarshal(raw, &values); err != nil {
				return Errorf(CodeInvalidRequest, "param must be an object")
			}
			if err := validateObject(values, spec, models, depth+1); err != nil {
				return err
			}
		}
	default:
		return Errorf(CodeInvalidRequest, "unsupported parameter type %q", typ)
	}
	return nil
}

func refModelName(ref string) string {
	const prefix = "#/models/"
	if len(ref) > len(prefix) && ref[:len(prefix)] == prefix {
		return ref[len(prefix):]
	}
	return ""
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
