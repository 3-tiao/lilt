package server

import (
	"context"

	"github.com/caiguo/lilt/internal/api"
	"github.com/caiguo/lilt/internal/jamendo"
	"github.com/caiguo/lilt/internal/securestore"
)

// jamendoAuthProvider exists to keep authorization.list and sources.list
// structurally aligned. The source needs an application-level client_id, not a
// user authorization flow, so its status is always not_required.
type jamendoAuthProvider struct {
	store securestore.Store
}

func newJamendoAuthProvider(store securestore.Store) *jamendoAuthProvider {
	if store == nil {
		store = securestore.NewMemory()
	}
	return &jamendoAuthProvider{store: store}
}

func (*jamendoAuthProvider) Source() api.SourceID { return api.SourceJamendo }

func (*jamendoAuthProvider) Describe(context.Context) api.SourceAuthorization {
	return api.SourceAuthorization{Source: api.SourceJamendo, Status: api.AuthNotRequired}
}

func (*jamendoAuthProvider) Begin(context.Context, string, func(api.AuthorizationFlow), func(api.AuthorizationFlow)) error {
	return api.Errorf(api.CodeUnsupportedCommand, "Jamendo does not use a user authorization flow; run `lilt jamendo setup`")
}

func (*jamendoAuthProvider) Cancel(string) {}

func (p *jamendoAuthProvider) Disconnect(context.Context) *api.Error {
	if err := jamendo.DeleteClientID(p.store); err != nil {
		return api.Errorf(api.CodeAuthorizationFailed, "Jamendo client_id could not be removed")
	}
	return nil
}
