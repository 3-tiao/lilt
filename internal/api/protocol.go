// Package api defines the lilt Client API contract shared by the server and
// every client: the request/response envelopes, public data models, the command
// registry that also generates api.describe, and the Unix-socket transport.
package api

import (
	"encoding/json"
	"fmt"
)

// Request is a single NDJSON request. Params is left raw so the registry can
// validate it against the command's schema before decoding.
type Request struct {
	RequestID string          `json:"requestId"`
	Command   string          `json:"command"`
	Params    json.RawMessage `json:"params,omitempty"`
}

// Response is the single response to a normal command, or the first line of a
// session.watch stream.
type Response struct {
	OK        bool            `json:"ok"`
	RequestID string          `json:"requestId,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Error     *Error          `json:"error,omitempty"`
}

// Error is the stable error shape. Clients branch on Code only.
type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Message
}

// WithDetails attaches structured error details and returns the error.
func (e *Error) WithDetails(details map[string]any) *Error {
	if e == nil {
		return e
	}
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	for key, value := range details {
		e.Details[key] = value
	}
	return e
}

// Event is one line of a session.watch stream after the initial response.
type Event struct {
	Event    string          `json:"event"`
	Sequence uint64          `json:"sequence"`
	Data     json.RawMessage `json:"data,omitempty"`
}

// Success builds an ok response carrying data.
func Success(requestID string, data any) Response {
	return Response{OK: true, RequestID: requestID, Data: mustJSON(data)}
}

// Failure builds an error response.
func Failure(requestID string, err *Error) Response {
	return Response{OK: false, RequestID: requestID, Error: err}
}

// Errorf builds a stable error with a formatted message.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Stable error codes. Clients MUST branch only on these.
const (
	CodeNoActiveSession            = "no_active_session"
	CodeActiveSession              = "active_session"
	CodeInvalidRequest             = "invalid_request"
	CodeInvalidState               = "invalid_state"
	CodeDuplicateResultUnavailable = "duplicate_result_unavailable"
	CodeInvalidReference           = "invalid_reference"
	CodeUnknownCommand             = "unknown_command"
	CodeUnsupportedCommand         = "unsupported_command"
	CodeSourceUnavailable          = "source_unavailable"
	CodeSourceMismatch             = "source_mismatch"
	CodeAuthorizationRequired      = "authorization_required"
	CodeAuthorizationInProgress    = "authorization_in_progress"
	CodeAuthorizationFlowNotFound  = "authorization_flow_not_found"
	CodeAuthorizationFailed        = "authorization_failed"
	CodeSubscriptionRequired       = "subscription_required"
	CodeQueueUnavailable           = "queue_unavailable"
	CodeQueueNotJumpable           = "queue_not_jumpable"
	CodeUndoUnavailable            = "undo_unavailable"

	CodePreviewUnavailable      = "preview_unavailable"
	CodePreviewUnsupported      = "preview_unsupported"
	CodePartialFailure          = "partial_failure"
	CodeInternalError           = "internal_error"
	CodeConflict                = "conflict"
	CodePlaybackError           = "playback_error"
	CodePlaybackStalled         = "playback_stalled"
	CodePlaybackSkipped         = "playback_skipped"
	CodeSearchFailed            = "search_failed"
	CodeStateSaveFailed         = "state_save_failed"
	CodeStorageUnavailable      = "storage_unavailable"
	CodeEngineRestarting        = "engine_restarting"
	CodeOperationOutcomeUnknown = "operation_outcome_unknown"
	CodeSessionUnavailable      = "session_unavailable"
)

// ErrorCatalog maps each stable code to a short human description, used by
// api.describe.
var ErrorCatalog = map[string]string{
	CodeNoActiveSession:            "no server is running",
	CodeActiveSession:              "another server already owns the socket",
	CodeInvalidRequest:             "params are missing, mistyped, or out of range",
	CodeInvalidState:               "the command is legal but not in the current playback state",
	CodeDuplicateResultUnavailable: "the request already ran but its cached result was evicted; do not re-execute",
	CodeInvalidReference:           "the ref syntax or resource kind is invalid",
	CodeUnknownCommand:             "the command is not registered",
	CodeUnsupportedCommand:         "the current source or engine lacks this capability",
	CodeSourceUnavailable:          "the source is dynamically unavailable",
	CodeSourceMismatch:             "the refs span different sources or disagree with the active queue source",
	CodeAuthorizationRequired:      "the source needs authorization",
	CodeAuthorizationInProgress:    "the source already has an active flow",
	CodeAuthorizationFlowNotFound:  "the flow id is unknown or its terminal record expired",
	CodeAuthorizationFailed:        "the flow could not start or local credentials are unavailable",
	CodeSubscriptionRequired:       "the source needs an active subscription",
	CodeQueueUnavailable:           "the current mode or source has no queue",
	CodeQueueNotJumpable:           "a queue built by appends cannot be jumped; start the row from its list",
	CodeUndoUnavailable:            "the latest queue removal can no longer be undone exactly",
	CodePreviewUnavailable:         "no preview asset is available",
	CodePreviewUnsupported:         "preview mode does not support this control",
	CodePartialFailure:             "the primary operation happened but a follow-up failed",
	CodeConflict:                   "an ifQueueRevision precondition was not met",
	CodePlaybackError:              "the provider or engine failed to play",
	CodePlaybackStalled:            "a media stream stalled or failed and is being retried once; journal-only, never published",
	CodePlaybackSkipped:            "a queue item stayed dead through the retry and was skipped; playback continues",
	CodeSearchFailed:               "content discovery failed",
	CodeStateSaveFailed:            "state was not persisted and authoritative memory is unchanged",
	CodeStorageUnavailable:         "the activity store is unavailable; playback continues but favorites and history are read-only",
	CodeEngineRestarting:           "the engine is restarting and the command certainly did not run",
	CodeOperationOutcomeUnknown:    "the command timed out and may have had side effects; do not auto-replay",
	CodeSessionUnavailable:         "the socket or server internals are unavailable",
}

func mustJSON(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		// The server only encodes values it constructs; a failure here is a
		// programming error, not a client-visible condition.
		panic(fmt.Sprintf("api: marshal %T: %v", value, err))
	}
	return encoded
}
