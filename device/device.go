// Package device is the door a member puts in front of the routes a device
// posts to. A device proves itself on each request with one header,
//
//	Authorization: Device <compact JWS>
//
// a short assertion it signs with its own key, with its certificate chain
// beside it in the protected header's x5c (the device certificate first,
// then any intermediates, never the root). It is not a TLS client
// certificate, so it passes every proxy in front of the member.
//
// The door checks five things and refuses the request with 401 and a short
// reason when any fails:
//
//  1. Chain: the certificates chain to Config.Roots, for client
//     authentication (ExtKeyUsageClientAuth), and the assertion is signed
//     by the device certificate's key (ES256 with P-256, or PS256 with RSA
//     of at least 2048 bits).
//  2. Validity: every certificate sent is inside its validity window.
//  3. Revocation: every certificate below the root is absent from its
//     issuer's revocation list, read from the certificate's CRL
//     distribution point with Config.HTTP. A list is trusted until its
//     nextUpdate plus Config.CRLGrace. A certificate with no list the door
//     can read, or whose list is out of date, is refused.
//  4. Request: aud is Config.Audience, htm is the method, htu is the
//     audience plus the request's path, bh is the base64url SHA-256 of the
//     exact body, and the assertion lives at most five minutes (exp at most
//     iat + 300) and has not expired. Clocks may differ by 30 seconds.
//  5. Replay: each jti is taken once and held until its exp.
//
// The door reads the body to hash it, at most 10 MiB, and hands the handler
// the same bytes. From returns the device it let in.
//
// # Reading the device and the person
//
// Config.DeviceFrom and Config.PersonFrom each name where in the device
// certificate a value is read from, as a comma-separated list tried in
// order; the first one present wins:
//
//   - cn: the subject's common name
//   - email: the first email address in the subject alternative names
//   - upn: the first user principal name (otherName 1.3.6.1.4.1.311.20.2.3)
//   - dns: the first DNS name
//   - uri: the first URI, whole
//   - uri:<scheme>: the first URI with that scheme, without "<scheme>://",
//     such as uri:IntuneDeviceId
//
// DeviceFrom defaults to "cn" and PersonFrom to "email,upn". A certificate
// that names no device or no person is refused.
package device

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Limits the door holds every assertion to.
const (
	// MaxLife is the longest an assertion may live, exp minus iat.
	MaxLife = 300 * time.Second
	// Skew is how far the device's clock may differ from the member's.
	Skew = 30 * time.Second
	// MaxBody is the largest body the door reads.
	MaxBody = 10 << 20
	// Scheme is the Authorization scheme a device sends.
	Scheme = "Device"
)

// Config is how a member sets up its door.
type Config struct {
	// Audience is the member's own origin, scheme and host with no path,
	// such as https://muster.example.com. An assertion's aud must equal it.
	Audience string
	// Roots is the estate's device authority.
	Roots *x509.CertPool
	// DeviceFrom and PersonFrom say where the device id and the person are
	// read from the certificate; the package doc lists the values. The
	// defaults are "cn" and "email,upn".
	DeviceFrom, PersonFrom string
	// CRLGrace is how long past its nextUpdate a revocation list is still
	// trusted. Zero is one hour.
	CRLGrace time.Duration
	// HTTP fetches revocation lists. Nil is a client with a 10 second
	// timeout.
	HTTP *http.Client
	// Now is the clock. Nil is time.Now.
	Now func() time.Time
}

// Device is who the door let in, read from the device certificate.
type Device struct {
	// ID is the device's id, per Config.DeviceFrom.
	ID string
	// Name is the first SAN DNS name, else the subject's common name.
	Name string
	// Person is the device's person, per Config.PersonFrom.
	Person string
	// Serial is the certificate's serial number in lower-case hex.
	Serial string
	// Fingerprint is the SHA-256 of the certificate's DER, lower-case hex.
	Fingerprint string
	// NotAfter is when the certificate runs out.
	NotAfter time.Time
}

// Door checks a device's assertion on each request.
type Door struct {
	audience string
	roots    *x509.CertPool
	device   selector
	person   selector
	now      func() time.Time
	log      *slog.Logger
	lists    *revocation
	seen     *replay
}

// New makes a door from cfg.
func New(cfg Config, log *slog.Logger) (*Door, error) {
	audience, err := origin(cfg.Audience)
	if err != nil {
		return nil, err
	}
	if cfg.Roots == nil {
		return nil, errors.New("device: Roots is required")
	}
	if log == nil {
		return nil, errors.New("device: a logger is required")
	}
	device, err := parseSelector(cfg.DeviceFrom, "cn")
	if err != nil {
		return nil, fmt.Errorf("device: DeviceFrom: %w", err)
	}
	person, err := parseSelector(cfg.PersonFrom, "email,upn")
	if err != nil {
		return nil, fmt.Errorf("device: PersonFrom: %w", err)
	}
	grace := cfg.CRLGrace
	if grace == 0 {
		grace = time.Hour
	}
	if grace < 0 {
		return nil, errors.New("device: CRLGrace must not be negative")
	}
	client := cfg.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Door{
		audience: audience,
		roots:    cfg.Roots,
		device:   device,
		person:   person,
		now:      now,
		log:      log,
		lists:    newRevocation(client, grace),
		seen:     &replay{seen: map[string]time.Time{}},
	}, nil
}

// origin checks that s is a bare origin and returns it without a trailing
// slash.
func origin(s string) (string, error) {
	s = strings.TrimSuffix(s, "/")
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("device: Audience %q must be an origin such as https://member.example.com", s)
	}
	return s, nil
}

// The checks a refusal names in its log line.
const (
	checkAssertion  = "assertion"
	checkChain      = "chain"
	checkValidity   = "validity"
	checkRevocation = "revocation"
	checkRequest    = "request"
	checkReplay     = "replay"
	checkIdentity   = "identity"
)

// refusal is a failed check: what the device is told, and what is logged.
type refusal struct {
	check  string
	status int
	reason string
	err    error
}

func (r *refusal) Error() string { return r.check + ": " + r.reason }

func refuse(check, reason string, err error) *refusal {
	return &refusal{check: check, status: http.StatusUnauthorized, reason: reason, err: err}
}

// Wrap admits a request to h only when its assertion passes every check,
// with the device in its context.
func (d *Door) Wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dev, body, ref := d.check(r)
		if ref != nil {
			attrs := []any{"check", ref.check, "reason", ref.reason, "route", r.Pattern}
			if ref.err != nil {
				attrs = append(attrs, "error", ref.err.Error())
			}
			if dev.Fingerprint != "" {
				attrs = append(attrs, "fingerprint", dev.Fingerprint)
			}
			d.log.InfoContext(r.Context(), "device refused", attrs...)
			if ref.status == http.StatusUnauthorized {
				w.Header().Set("WWW-Authenticate", Scheme)
			}
			http.Error(w, ref.reason, ref.status)
			return
		}
		d.log.DebugContext(r.Context(), "device admitted", "route", r.Pattern, "device", dev.ID, "fingerprint", dev.Fingerprint)
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), deviceKey{}, dev)))
	})
}

// check runs the five checks in order of cost, and the replay check last,
// so a request that fails another check never spends its jti.
func (d *Door) check(r *http.Request) (Device, []byte, *refusal) {
	token, ok := credential(r.Header.Get("Authorization"))
	if !ok {
		return Device{}, nil, refuse(checkAssertion, "no device assertion", nil)
	}
	a, err := parse(token)
	if err != nil {
		return Device{}, nil, refuse(checkAssertion, "the device assertion is malformed", err)
	}
	leaf := a.chain[0]
	dev := Device{
		Name:        name(leaf),
		Serial:      leaf.SerialNumber.Text(16),
		Fingerprint: fingerprint(leaf),
		NotAfter:    leaf.NotAfter,
	}
	now := d.now()

	for _, c := range a.chain {
		if now.Before(c.NotBefore) || now.After(c.NotAfter) {
			return dev, nil, refuse(checkValidity, "the device certificate is not valid now", fmt.Errorf("certificate %q valid %s to %s", c.Subject.CommonName, c.NotBefore.UTC().Format(time.RFC3339), c.NotAfter.UTC().Format(time.RFC3339)))
		}
	}
	chain, err := d.verifyChain(a.chain, now)
	if err != nil {
		return dev, nil, refuse(checkChain, "the device certificate is not from the device authority", err)
	}
	if err := a.verifySignature(); err != nil {
		return dev, nil, refuse(checkChain, "the device assertion is not signed by its certificate", err)
	}

	if dev.ID = d.device.read(leaf); dev.ID == "" {
		return dev, nil, refuse(checkIdentity, "the device certificate names no device", nil)
	}
	if dev.Person = d.person.read(leaf); dev.Person == "" {
		return dev, nil, refuse(checkIdentity, "the device certificate names no person", nil)
	}

	if ref := d.checkClaims(r, a.claims, now); ref != nil {
		return dev, nil, ref
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if err != nil {
		return dev, nil, &refusal{check: checkRequest, status: http.StatusBadRequest, reason: "the body could not be read", err: err}
	}
	if len(body) > MaxBody {
		return dev, nil, &refusal{check: checkRequest, status: http.StatusRequestEntityTooLarge, reason: "the body is too large"}
	}
	if BodyHash(body) != a.claims.BodyHash {
		return dev, nil, refuse(checkRequest, "the body does not match the device assertion", nil)
	}

	if err := d.lists.check(r.Context(), chain, now); err != nil {
		if errors.Is(err, errRevoked) {
			return dev, nil, refuse(checkRevocation, "the device certificate is revoked", err)
		}
		return dev, nil, refuse(checkRevocation, "no current revocation list for the device certificate", err)
	}

	until := time.Unix(a.claims.Expires, 0).Add(Skew)
	if !d.seen.take(dev.Fingerprint+" "+a.claims.ID, until, now) {
		return dev, nil, refuse(checkReplay, "the device assertion was already used", nil)
	}
	return dev, body, nil
}

func (d *Door) verifyChain(certs []*x509.Certificate, now time.Time) ([]*x509.Certificate, error) {
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	chains, err := certs[0].Verify(x509.VerifyOptions{
		Roots:         d.roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if err != nil {
		return nil, err
	}
	return chains[0], nil
}

func (d *Door) checkClaims(r *http.Request, c Claims, now time.Time) *refusal {
	if c.Audience != d.audience {
		return refuse(checkRequest, "the device assertion is for another member", fmt.Errorf("aud %q", c.Audience))
	}
	if c.Method != r.Method {
		return refuse(checkRequest, "the device assertion is for another method", fmt.Errorf("htm %q", c.Method))
	}
	if want := d.audience + r.URL.EscapedPath(); c.URL != want {
		return refuse(checkRequest, "the device assertion is for another address", fmt.Errorf("htu %q, want %q", c.URL, want))
	}
	issued, expires := time.Unix(c.IssuedAt, 0), time.Unix(c.Expires, 0)
	if c.IssuedAt == 0 || c.Expires == 0 || expires.Sub(issued) > MaxLife || expires.Before(issued) {
		return refuse(checkRequest, "the device assertion lives too long", fmt.Errorf("iat %d exp %d", c.IssuedAt, c.Expires))
	}
	if issued.After(now.Add(Skew)) {
		return refuse(checkRequest, "the device assertion is from the future", fmt.Errorf("iat %d", c.IssuedAt))
	}
	if now.After(expires.Add(Skew)) {
		return refuse(checkRequest, "the device assertion has expired", fmt.Errorf("exp %d", c.Expires))
	}
	if id, err := b64.DecodeString(c.ID); err != nil || len(id) < 16 {
		return refuse(checkRequest, "the device assertion has no proper jti", nil)
	}
	return nil
}

// credential returns the token of an Authorization header in the Device
// scheme.
func credential(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, Scheme) {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

func fingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return hex.EncodeToString(sum[:])
}

type deviceKey struct{}

// From returns the device the door let in, and false outside a door.
func From(ctx context.Context) (Device, bool) {
	d, ok := ctx.Value(deviceKey{}).(Device)
	return d, ok
}

// replay holds each jti until it expires.
type replay struct {
	mu    sync.Mutex
	seen  map[string]time.Time
	sweep time.Time
}

// take records id until until, and says false when it is already held.
func (r *replay) take(id string, until, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if held, ok := r.seen[id]; ok && now.Before(held) {
		return false
	}
	if now.After(r.sweep) {
		for k, held := range r.seen {
			if !now.Before(held) {
				delete(r.seen, k)
			}
		}
		r.sweep = now.Add(time.Minute)
	}
	r.seen[id] = until
	return true
}
