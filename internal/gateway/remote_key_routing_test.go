package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/budget"
	"github.com/yetone/magpie/internal/provider"
)

// A Remote magpie's gateway keys are weighed by the limit that magpie
// holds each to, as a sub2api key's are: Least used first sends the next
// request to the key with the most left there, though this magpie has
// sent neither key anything (the other magpie's users spent it). A key's
// card read on the Usage page tells routing at once; a key held to no
// limit tells routing nothing, as before; and no card
// of this magpie's own is made of a remote key's limit for a third magpie.
func TestRemoteMagpieKeysWeighedByTheirLimits(t *testing.T) {
	fresh(t)
	t.Setenv("MAGPIE_ADDR", "")
	provider.ForgetBalances()
	t.Cleanup(provider.ForgetBalances)
	provider.ForgetRemoteCardsForTest()
	t.Cleanup(provider.ForgetRemoteCardsForTest)
	old := keyAllowance
	keyAllowance = provider.KeyAllowance // the keys' limits as read, here
	t.Cleanup(func() { keyAllowance = old })
	budget.Forget()
	limitedUpstream(t, 0)
	keys, secrets := newCaller(t, "Laptop", "Desk")
	// 30-day cycles from now, at the real clock: every reset the remote
	// tells is weeks ahead, so none passes while the test runs (a clock
	// pinned to a past day would tell resets already gone, and routing
	// reads a window whose reset has passed as empty)
	setLimit(t, keys[0].ID, &access.Limit{Period: "days", Days: 30, Tokens: 1000})
	setLimit(t, keys[1].ID, &access.Limit{Period: "days", Days: 30, Tokens: 5000})
	gw := httptest.NewServer(lanGuard(New().Handler()))
	defer gw.Close()

	// the laptop key spends 700 of its 1000 tokens there, straight at
	// that magpie: nothing of it passes through this one's routing
	for range 2 {
		r, _ := http.NewRequest("POST", gw.URL+"/v1/chat/completions", strings.NewReader(chatReq))
		r.Header.Set("Authorization", "Bearer "+secrets[0])
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatal(res.Status)
		}
	}
	for start := time.Now(); ; time.Sleep(10 * time.Millisecond) {
		all, _ := access.List()
		var k access.Key
		for _, a := range all {
			if a.ID == keys[0].ID {
				k = a
			}
		}
		if st := budget.Of(k, time.Now()); st != nil && st.Calls == 2 && st.InFlight == 0 {
			break
		}
		if time.Since(start) > 10*time.Second {
			t.Fatal("the laptop key's two calls were never counted")
		}
	}

	if err := provider.Save(provider.Provider{ID: "office", Name: "Office", Preset: provider.RemoteMagpiePreset, Routing: provider.LeastUsed,
		Key: secrets[0], KeyName: "laptop", Keys: []provider.KeyAccount{{Name: "desk", Key: secrets[1]}}, Chat: gw.URL}); err != nil {
		t.Fatal(err)
	}
	p, err := provider.Find("office")
	if err != nil {
		t.Fatal(err)
	}
	seat := func(key string) candidate {
		q := *p
		q.Key = key
		return candidate{p: q, model: "plan/m1", rest: "office#" + key[len(key)-4:]}
	}
	laptop, desk := seat(secrets[0]), seat(secrets[1])
	known := func(c candidate) provider.Allowance {
		t.Helper()
		for start := time.Now(); time.Since(start) < 10*time.Second; time.Sleep(10 * time.Millisecond) {
			if a, ok := provider.KeyAllowance(c.p); ok {
				return a
			}
		}
		t.Fatalf("%s's limit never read", c.rest)
		return nil
	}
	if a := known(laptop); len(a) != 1 || a[0].Used != 70 || a[0].Of != 1000 || a[0].Resets.IsZero() {
		t.Fatalf("laptop's limit as routing reads it: %+v", a)
	}
	if a := known(desk); len(a) != 1 || a[0].Used != 0 || a[0].Of != 5000 {
		t.Fatalf("desk's limit as routing reads it: %+v", a)
	}
	// listed first, laptop goes after the key with the most left there
	got, wg := weigh(*p, []candidate{laptop, desk}, "plan/m1", provider.Chat)
	if restsOf(got) != desk.rest+" "+laptop.rest+" " {
		t.Fatalf("least used: %s, want desk first", restsOf(got))
	}
	if w := weighed(got[1], *p, wg, false, provider.Chat); !w.Known || w.Used != 70 {
		t.Fatalf("laptop's trace: %+v", w)
	}

	// a remote key's limit is no card of this magpie's for another one
	for _, q := range provider.CachedCards(time.Now()) {
		if q.Provider == "office" {
			t.Fatalf("a card of the remote's key told on: %+v", q)
		}
	}

	// held to no limit there: its card, read now, tells routing it has
	// none, and it is as unknown as any key that tells nothing
	setLimit(t, keys[1].ID, nil)
	provider.RefreshUsage(context.Background(), "office/~keys", "desk")
	if a, ok := provider.KeyAllowance(desk.p); ok || len(a) != 0 {
		t.Fatalf("desk with no limit still weighed by one: %+v", a)
	}
	// a key telling no share goes after one with a told share and room,
	// as Least used keeps any such key (#4535)
	if got := restsOf(route(*p, []candidate{desk, laptop}, "plan/m1", provider.Chat)); got != laptop.rest+" "+desk.rest+" " {
		t.Fatalf("least used, desk unlimited: %s", got)
	}
}
