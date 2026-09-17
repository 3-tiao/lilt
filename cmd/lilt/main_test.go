package main

import (
	"errors"
	"testing"

	"github.com/caiguo/lilt/internal/api"
)

func TestErrorResponsePassesServerErrorsThrough(t *testing.T) {
	serverErr := api.Errorf(api.CodeInvalidReference, "bad ref")
	response := api.Failure("r1", serverErr)
	got := errorResponse(response, serverErr)
	if got.Error == nil || got.Error.Code != api.CodeInvalidReference || got.RequestID != "r1" {
		t.Fatalf("server error = %+v", got)
	}
}

func TestErrorResponseMapsClientFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"transport", api.ErrTransport, api.CodeSessionUnavailable},
		{"no session", api.ErrNoActiveSession, api.CodeNoActiveSession},
		{"usage", errors.New("usage"), api.CodeInvalidRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := errorResponse(api.Response{}, test.err)
			if got.Error == nil || got.Error.Code != test.want {
				t.Fatalf("code = %+v, want %s", got.Error, test.want)
			}
		})
	}
}
