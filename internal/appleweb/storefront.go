package appleweb

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// storefrontAlignLimit bounds how many navigations one browser start may make
// to move the page onto the account's storefront. Alignment is a loop — read
// the account region, navigate, wait for the page to land — and a page that
// never reaches the account region (redirects, a storefront the web player
// refuses to serve) must not turn that loop into a carousel: after the limit
// the session continues on whatever region the page ended up in.
const storefrontAlignLimit = 2

// storefrontSettle is how long one navigation gets to land the page on the
// account's storefront before alignment gives that attempt up. It is a
// variable so hermetic tests can shorten it.
var storefrontSettle = 30 * time.Second

// accountStorefrontJS reads both storefronts in one evaluation: the signed-in
// account's own region from /v1/me/storefront and the region the page is
// actually on from mk.storefrontId. The account call is the part that fails
// without a usable session (signed out, expired), and that failure is an
// answer, not an error: an account that cannot be asked has no region to
// align to. The response shape is the MusicKit envelope — r.data.data[0].id,
// one level shallower than the catalog queries' r.data.results.
const accountStorefrontJS = `(async () => {
  const mk = window.MusicKit.getInstance();
  try {
    const r = await mk.api.music('/v1/me/storefront');
    const account = r && r.data && r.data.data && r.data.data[0] && r.data.data[0].id;
    return JSON.stringify({ account: account ? String(account) : '', page: String(mk.storefrontId || '') });
  } catch (e) {
    return JSON.stringify({ signedOut: true });
  }
})()`

// storefrontProbe is the page's answer to accountStorefrontJS.
type storefrontProbe struct {
	Account   string `json:"account"`
	Page      string `json:"page"`
	SignedOut bool   `json:"signedOut"`
}

// storefronts asks the page for the account's storefront and its own.
func (b *Browser) storefronts(ctx context.Context) (storefrontProbe, error) {
	raw, err := b.Evaluate(ctx, accountStorefrontJS)
	if err != nil {
		return storefrontProbe{}, err
	}
	var probe storefrontProbe
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return storefrontProbe{}, fmt.Errorf("appleweb: unreadable storefront answer")
	}
	return probe, nil
}

// alignStorefront moves the page onto the signed-in account's storefront.
//
// mk.storefrontId follows the page's URL region, not the account: a fresh
// profile lands on the US page and signing in does not move it. MusicKit
// grants full playback per "account subscription region × catalog region", so
// a mismatched page finds the catalog in a region the account cannot play
// whole tracks from — everything plays as a preview while the session still
// reports authorized. Only a signed-in account has a region to align to:
// previews are previews anywhere, so a signed-out page stays where it is.
//
// Alignment is best effort by design. A navigation that fails, or a page that
// never reaches the account region, leaves the session running on the current
// region — the catalog still answers there and playback degrades to preview —
// rather than failing the browser start. It runs before the Widevine probe,
// so the capability answer and the catalog both see the final region.
func (b *Browser) alignStorefront(ctx context.Context, log func(kind string, fields map[string]any)) {
	for range storefrontAlignLimit {
		state, err := b.State(ctx)
		if err != nil || !state.Authorized {
			// No usable account session, so no region to align to.
			return
		}
		probe, err := b.storefronts(ctx)
		if err != nil {
			// The account cannot be asked; treat it like a signed-out
			// session and keep the page where it is.
			return
		}
		if probe.SignedOut || probe.Account == "" || probe.Page == probe.Account {
			return
		}
		url := "https://music.apple.com/" + probe.Account + "/listen-now"
		if _, err := b.Evaluate(ctx, fmt.Sprintf("(() => { window.location.href = %s; return 'navigating'; })()", strconv.Quote(url))); err != nil {
			return
		}
		if log != nil {
			// Storefront ids are stable region codes, not upstream
			// messages: nothing here needs sanitizing.
			log("apple.storefront_aligned", map[string]any{"from": probe.Page, "to": probe.Account})
		}
		// Wait for the navigated page, not the old one: the old document keeps
		// answering evaluates until the navigation commits, so readiness alone
		// would read as settled while the page is still the old region. The
		// positive signal — MusicKit booted and reporting the account's region —
		// is what "ready again" actually means here.
		b.waitStorefront(ctx, probe.Account)
	}
}

// waitStorefront polls until the page reports the wanted storefront or the
// settle budget runs out. A probe that throws (no MusicKit yet) or that still
// answers with the previous region simply keeps the poll going.
func (b *Browser) waitStorefront(ctx context.Context, want string) {
	deadline := time.Now().Add(storefrontSettle)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return
		}
		probe, err := b.storefronts(ctx)
		if err == nil && !probe.SignedOut && probe.Page == want {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}
