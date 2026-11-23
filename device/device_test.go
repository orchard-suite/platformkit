package device_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orchard-suite/platformkit/v3/device"
	"github.com/orchard-suite/platformkit/v3/device/devicetest"
)

const aud = "https://muster.example.com"

// clock is a door's clock that tests move.
type clock struct{ t atomic.Int64 }

func newClock() *clock               { c := &clock{}; c.set(time.Now()); return c }
func (c *clock) set(t time.Time)     { c.t.Store(t.UnixNano()) }
func (c *clock) now() time.Time      { return time.Unix(0, c.t.Load()) }
func (c *clock) add(d time.Duration) { c.set(c.now().Add(d)) }

type fixture struct {
	t     *testing.T
	ca    *devicetest.Authority
	door  *device.Door
	clock *clock
	logs  *bytes.Buffer
}

func setup(t *testing.T, cfg device.Config) *fixture {
	t.Helper()
	ca := devicetest.New(t)
	f := &fixture{t: t, ca: ca, clock: newClock(), logs: &bytes.Buffer{}}
	cfg.Audience = aud
	cfg.Roots = ca.Roots
	cfg.HTTP = ca.Server.Client()
	cfg.Now = f.clock.now
	door, err := device.New(cfg, slog.New(slog.NewTextHandler(f.logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	f.door = door
	return f
}

// request makes a signed POST of body to path.
func (f *fixture) request(dev devicetest.Issued, method, path, body string, edit func(*device.Claims)) *http.Request {
	f.t.Helper()
	req := httptest.NewRequest(method, aud+path, strings.NewReader(body))
	claims, err := device.NewClaims(req, aud, f.clock.now())
	if err != nil {
		f.t.Fatal(err)
	}
	if edit != nil {
		edit(&claims)
	}
	token, err := device.Encode(dev.Key, dev.Chain, claims)
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Device "+token)
	return req
}

type result struct {
	code   int
	reason string
	dev    device.Device
	body   string
}

func (f *fixture) serve(req *http.Request) result {
	f.t.Helper()
	var res result
	h := f.door.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res.dev, _ = device.From(r.Context())
		b, _ := io.ReadAll(r.Body)
		res.body = string(b)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res.code = rec.Code
	res.reason = strings.TrimSpace(rec.Body.String())
	return res
}

func (f *fixture) admitted(req *http.Request) result {
	f.t.Helper()
	res := f.serve(req)
	if res.code != http.StatusOK {
		f.t.Fatalf("refused %d %q; logs:\n%s", res.code, res.reason, f.logs)
	}
	return res
}

func (f *fixture) refused(req *http.Request, check, reason string) {
	f.t.Helper()
	res := f.serve(req)
	if res.code != http.StatusUnauthorized {
		f.t.Fatalf("status %d %q, want 401", res.code, res.reason)
	}
	if !strings.Contains(res.reason, reason) {
		f.t.Fatalf("reason %q, want %q", res.reason, reason)
	}
	if !strings.Contains(f.logs.String(), "check="+check) {
		f.t.Fatalf("log names no check %q:\n%s", check, f.logs)
	}
}

func TestAdmitsES256AndPS256WithTheSameBody(t *testing.T) {
	for _, rsa := range []bool{false, true} {
		f := setup(t, device.Config{})
		dev := f.ca.Issue(t, devicetest.Leaf{CN: "dev-42", Email: "user@example.com", DNS: []string{"laptop.example.com"}, RSA: rsa})
		res := f.admitted(f.request(dev, http.MethodPost, "/tender/v1/readers/muster", `{"a":1}`, nil))
		if res.body != `{"a":1}` {
			t.Fatalf("handler body %q", res.body)
		}
		d := res.dev
		if d.ID != "dev-42" || d.Person != "user@example.com" || d.Name != "laptop.example.com" {
			t.Fatalf("device %+v", d)
		}
		if d.Serial != dev.Cert.SerialNumber.Text(16) || len(d.Fingerprint) != 64 || !d.NotAfter.Equal(dev.Cert.NotAfter) {
			t.Fatalf("device %+v", d)
		}
		if strings.Contains(f.logs.String(), "Device ") {
			t.Fatal("a log line holds the token")
		}
	}
}

func TestSignHelperIsAdmitted(t *testing.T) {
	f := setup(t, device.Config{})
	f.clock.set(time.Now())
	dev := f.ca.Issue(t, devicetest.Leaf{Email: "user@example.com"})
	req := httptest.NewRequest(http.MethodGet, aud+"/tender/v1/readers/muster?x=1", nil)
	if err := devicetest.Sign(req, dev.Key, dev.Chain, aud); err != nil {
		t.Fatal(err)
	}
	f.admitted(req)
}

func TestPersonFallsBackToUPNAndSelectorsApply(t *testing.T) {
	f := setup(t, device.Config{})
	dev := f.ca.Issue(t, devicetest.Leaf{CN: "Laptop", UPN: "user@example.com"})
	if d := f.admitted(f.request(dev, http.MethodGet, "/x", "", nil)).dev; d.Person != "user@example.com" || d.Name != "Laptop" {
		t.Fatalf("device %+v", d)
	}

	f = setup(t, device.Config{DeviceFrom: "uri:IntuneDeviceId,cn", PersonFrom: "upn"})
	dev = f.ca.Issue(t, devicetest.Leaf{CN: "Laptop", Email: "other@example.com", UPN: "user@example.com", URIs: []string{"IntuneDeviceId://abc-123"}})
	if d := f.admitted(f.request(dev, http.MethodGet, "/x", "", nil)).dev; d.ID != "abc-123" || d.Person != "user@example.com" {
		t.Fatalf("device %+v", d)
	}
}

func TestNewRefusesBadConfig(t *testing.T) {
	ca := devicetest.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, cfg := range []device.Config{
		{Audience: "https://muster.example.com/path", Roots: ca.Roots},
		{Audience: "muster.example.com", Roots: ca.Roots},
		{Audience: aud},
		{Audience: aud, Roots: ca.Roots, DeviceFrom: "serial"},
		{Audience: aud, Roots: ca.Roots, PersonFrom: "email,nope"},
	} {
		if _, err := device.New(cfg, log); err == nil {
			t.Fatalf("%+v was accepted", cfg)
		}
	}
}

func TestRefusesNoOrMalformedAssertion(t *testing.T) {
	f := setup(t, device.Config{})
	f.refused(httptest.NewRequest(http.MethodGet, aud+"/x", nil), "assertion", "no device assertion")
	req := httptest.NewRequest(http.MethodGet, aud+"/x", nil)
	req.Header.Set("Authorization", "Device a.b.c")
	f.refused(req, "assertion", "malformed")
}

func TestChain(t *testing.T) {
	f := setup(t, device.Config{})
	other := devicetest.New(t).Issue(t, devicetest.Leaf{Email: "user@example.com"})
	f.refused(f.request(other, http.MethodGet, "/x", "", nil), "chain", "not from the device authority")

	noClientAuth := f.ca.Issue(t, devicetest.Leaf{Email: "user@example.com", NoClientAuth: true})
	f.refused(f.request(noClientAuth, http.MethodGet, "/x", "", nil), "chain", "not from the device authority")

	// A chain that verifies, signed by a key that is not the certificate's.
	dev := f.ca.Issue(t, devicetest.Leaf{Email: "user@example.com"})
	swapped := f.ca.Issue(t, devicetest.Leaf{Email: "user@example.com"})
	dev.Key = swapped.Key
	f.refused(f.request(dev, http.MethodGet, "/x", "", nil), "chain", "not signed by its certificate")
}

func TestValidity(t *testing.T) {
	f := setup(t, device.Config{})
	expired := f.ca.Issue(t, devicetest.Leaf{Email: "user@example.com", NotBefore: time.Now().Add(-48 * time.Hour), NotAfter: time.Now().Add(-time.Hour)})
	f.refused(f.request(expired, http.MethodGet, "/x", "", nil), "validity", "not valid now")

	early := f.ca.Issue(t, devicetest.Leaf{Email: "user@example.com", NotBefore: time.Now().Add(time.Hour)})
	f.refused(f.request(early, http.MethodGet, "/x", "", nil), "validity", "not valid now")
}

func TestRevokedLeafAndIntermediate(t *testing.T) {
	f := setup(t, device.Config{})
	dev := f.ca.Issue(t, devicetest.Leaf{Email: "user@example.com"})
	f.ca.Revoke(dev.Cert)
	f.refused(f.request(dev, http.MethodGet, "/x", "", nil), "revocation", "revoked")

	f = setup(t, device.Config{})
	dev = f.ca.Issue(t, devicetest.Leaf{Email: "user@example.com"})
	f.ca.Revoke(f.ca.Intermediate)
	f.refused(f.request(dev, http.MethodGet, "/x", "", nil), "revocation", "revoked")
}

func TestNoDistributionPointIsRefused(t *testing.T) {
	f := setup(t, device.Config{})
	dev := f.ca.Issue(t, devicetest.Leaf{Email: "user@example.com", NoCRL: true})
	f.refused(f.request(dev, http.MethodGet, "/x", "", nil), "revocation", "no current revocation list")
}

func TestStaleListIsRefused(t *testing.T) {
	f := setup(t, device.Config{CRLGrace: time.Hour})
	dev := f.ca.Issue(t, devicetest.Leaf{Email: "user@example.com"})
	f.ca.SignLists(func() time.Time { return time.Now().Add(-3 * time.Hour) }, time.Hour)
	f.refused(f.request(dev, http.MethodGet, "/x", "", nil), "revocation", "no current revocation list")
}

func TestListIsCachedThroughGraceThenRefused(t *testing.T) {
	f := setup(t, device.Config{CRLGrace: time.Hour})
	dev := f.ca.Issue(t, devicetest.Leaf{Email: "user@example.com", NotAfter: time.Now().Add(48 * time.Hour)})
	f.admitted(f.request(dev, http.MethodGet, "/x", "", nil))

	f.ca.Down(true)
	f.admitted(f.request(dev, http.MethodGet, "/x", "", nil)) // cached, before nextUpdate
	f.clock.add(90 * time.Minute)
	f.admitted(f.request(dev, http.MethodGet, "/x", "", nil)) // past nextUpdate, inside the grace
	f.clock.add(time.Hour)
	f.refused(f.request(dev, http.MethodGet, "/x", "", nil), "revocation", "no current revocation list")

	f.ca.Down(false)
	f.ca.SignLists(f.clock.now, time.Hour)
	f.clock.add(time.Minute) // past the retry wait
	f.admitted(f.request(dev, http.MethodGet, "/x", "", nil))
}

func TestRequestClaims(t *testing.T) {
	f := setup(t, device.Config{})
	dev := f.ca.Issue(t, devicetest.Leaf{Email: "user@example.com"})
	cases := []struct {
		name, reason string
		edit         func(*device.Claims)
	}{
		{"wrong audience", "for another member", func(c *device.Claims) { c.Audience = "https://tally.example.com" }},
		{"wrong method", "for another method", func(c *device.Claims) { c.Method = http.MethodGet }},
		{"wrong address", "for another address", func(c *device.Claims) { c.URL = aud + "/other" }},
		{"too long a life", "lives too long", func(c *device.Claims) { c.Expires = c.IssuedAt + 301 }},
		{"no jti", "no proper jti", func(c *device.Claims) { c.ID = "" }},
		{"from the future", "from the future", func(c *device.Claims) {
			c.IssuedAt += 120
			c.Expires += 120
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.t = t
			f.refused(f.request(dev, http.MethodPost, "/x", "{}", tc.edit), "request", tc.reason)
		})
	}
}

func TestExpiry(t *testing.T) {
	f := setup(t, device.Config{})
	dev := f.ca.Issue(t, devicetest.Leaf{Email: "user@example.com"})
	req := f.request(dev, http.MethodGet, "/x", "", nil)
	f.clock.add(device.MaxLife + device.Skew - time.Second)
	f.admitted(req)

	req = f.request(dev, http.MethodGet, "/x", "", nil)
	f.clock.add(device.MaxLife + device.Skew + time.Second)
	f.refused(req, "request", "expired")
}

func TestChangedBodyIsRefused(t *testing.T) {
	f := setup(t, device.Config{})
	dev := f.ca.Issue(t, devicetest.Leaf{Email: "user@example.com"})
	req := f.request(dev, http.MethodPost, "/x", `{"report":1}`, nil)
	req.Body = io.NopCloser(strings.NewReader(`{"report":2}`))
	f.refused(req, "request", "body does not match")
}

func TestReplayedJTIIsRefusedUntilItExpires(t *testing.T) {
	f := setup(t, device.Config{})
	dev := f.ca.Issue(t, devicetest.Leaf{Email: "user@example.com"})
	var jti string
	keep := func(c *device.Claims) { jti = c.ID }
	f.admitted(f.request(dev, http.MethodGet, "/x", "", keep))

	same := func(c *device.Claims) { c.ID = jti }
	f.refused(f.request(dev, http.MethodGet, "/x", "", same), "replay", "already used")

	// A request that fails another check does not spend its jti.
	fresh := ""
	f.refused(f.request(dev, http.MethodGet, "/x", "", func(c *device.Claims) { fresh = c.ID; c.Audience = "https://other.example.com" }), "request", "another member")
	f.admitted(f.request(dev, http.MethodGet, "/x", "", func(c *device.Claims) { c.ID = fresh }))
}

func TestIdentityIsRequired(t *testing.T) {
	f := setup(t, device.Config{})
	dev := f.ca.Issue(t, devicetest.Leaf{CN: "dev-1"})
	f.refused(f.request(dev, http.MethodGet, "/x", "", nil), "identity", "names no person")
}

func TestTooLargeBody(t *testing.T) {
	f := setup(t, device.Config{})
	dev := f.ca.Issue(t, devicetest.Leaf{Email: "user@example.com"})
	req := f.request(dev, http.MethodPost, "/x", strings.Repeat("a", device.MaxBody+1), nil)
	if res := f.serve(req); res.code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", res.code)
	}
}
