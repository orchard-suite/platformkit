package device

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// maxCRL is the largest revocation list the door reads.
	maxCRL = 32 << 20
	// retryAfter is how long the door waits after a failed fetch before it
	// fetches the same list again.
	retryAfter = 30 * time.Second
)

var errRevoked = errors.New("revoked")

// revocation fetches and caches each issuer's revocation list.
type revocation struct {
	http  *http.Client
	grace time.Duration

	mu    sync.Mutex
	lists map[string]*cached
}

// cached is one list, read from one address and checked against one issuer.
type cached struct {
	mu      sync.Mutex
	list    *crl
	failed  time.Time
	lastErr error
}

type crl struct {
	next    time.Time
	revoked map[string]struct{}
}

func newRevocation(client *http.Client, grace time.Duration) *revocation {
	return &revocation{http: client, grace: grace, lists: map[string]*cached{}}
}

// check refuses chain when any certificate below the root is revoked, or
// has no current list. chain runs from the leaf to the root.
func (v *revocation) check(ctx context.Context, chain []*x509.Certificate, now time.Time) error {
	for i := 0; i+1 < len(chain); i++ {
		cert, issuer := chain[i], chain[i+1]
		list, err := v.listFor(ctx, cert, issuer, now)
		if err != nil {
			return fmt.Errorf("%q: %w", cert.Subject.CommonName, err)
		}
		if _, ok := list.revoked[cert.SerialNumber.String()]; ok {
			return fmt.Errorf("%q serial %s: %w", cert.Subject.CommonName, cert.SerialNumber.Text(16), errRevoked)
		}
	}
	return nil
}

// listFor returns a current list for cert from the first of its
// distribution points that answers.
func (v *revocation) listFor(ctx context.Context, cert, issuer *x509.Certificate, now time.Time) (*crl, error) {
	var errs []error
	for _, point := range cert.CRLDistributionPoints {
		if !strings.HasPrefix(point, "http://") && !strings.HasPrefix(point, "https://") {
			continue
		}
		list, err := v.current(ctx, point, issuer, now)
		if err == nil {
			return list, nil
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil, errors.New("no http CRL distribution point")
	}
	return nil, errors.Join(errs...)
}

// current returns the list at point, fetching it again once it passes its
// nextUpdate and keeping the cached copy through the grace while a fetch
// fails.
func (v *revocation) current(ctx context.Context, point string, issuer *x509.Certificate, now time.Time) (*crl, error) {
	sum := sha256.Sum256(issuer.RawSubjectPublicKeyInfo)
	key := point + " " + hex.EncodeToString(sum[:])
	v.mu.Lock()
	c, ok := v.lists[key]
	if !ok {
		c = &cached{}
		v.lists[key] = c
	}
	v.mu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.list != nil && now.Before(c.list.next) {
		return c.list, nil
	}
	if now.Sub(c.failed) >= retryAfter {
		fresh, err := v.fetch(ctx, point, issuer)
		switch {
		case err != nil:
			c.failed, c.lastErr = now, err
		case !now.Before(fresh.next.Add(v.grace)):
			c.failed, c.lastErr = now, fmt.Errorf("%s is out of date: next update %s", point, fresh.next.UTC().Format(time.RFC3339))
		default:
			c.list, c.failed, c.lastErr = fresh, time.Time{}, nil
			return fresh, nil
		}
	}
	if c.list != nil && now.Before(c.list.next.Add(v.grace)) {
		return c.list, nil
	}
	if c.lastErr == nil {
		return nil, fmt.Errorf("%s is out of date", point)
	}
	return nil, c.lastErr
}

func (v *revocation) fetch(ctx context.Context, point string, issuer *x509.Certificate) (*crl, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, point, nil)
	if err != nil {
		return nil, err
	}
	res, err := v.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", point, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: %s", point, res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxCRL+1))
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", point, err)
	}
	if len(data) > maxCRL {
		return nil, fmt.Errorf("fetch %s: larger than %d bytes", point, maxCRL)
	}
	if block, _ := pem.Decode(data); block != nil && block.Type == "X509 CRL" {
		data = block.Bytes
	}
	list, err := x509.ParseRevocationList(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", point, err)
	}
	if !bytes.Equal(list.RawIssuer, issuer.RawSubject) {
		return nil, fmt.Errorf("%s is issued by another authority", point)
	}
	if err := list.CheckSignatureFrom(issuer); err != nil {
		return nil, fmt.Errorf("%s: %w", point, err)
	}
	if list.NextUpdate.IsZero() {
		return nil, fmt.Errorf("%s names no next update", point)
	}
	out := &crl{next: list.NextUpdate, revoked: make(map[string]struct{}, len(list.RevokedCertificateEntries))}
	for _, e := range list.RevokedCertificateEntries {
		out.revoked[e.SerialNumber.String()] = struct{}{}
	}
	return out, nil
}
