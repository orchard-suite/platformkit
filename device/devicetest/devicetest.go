// Package devicetest mints a device authority in memory for tests: a root,
// an intermediate, device certificates, and a server for both revocation
// lists. Sign is the device's side of the door.
package devicetest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/orchard-suite/platformkit/v3/device"
)

// Sign sets req's Authorization header to a fresh assertion for audience.
var Sign = device.Sign

// Authority is a root and an intermediate that issues device certificates,
// with a server that answers both revocation lists.
type Authority struct {
	Root, Intermediate *x509.Certificate
	// Roots holds Root, for device.Config.
	Roots *x509.CertPool
	// Server serves the lists. Its client is the one to give the door.
	Server *httptest.Server

	rootKey, intKey crypto.Signer

	mu      sync.Mutex
	now     func() time.Time
	life    time.Duration
	down    bool
	revoked map[*x509.Certificate][]*big.Int
	serial  int64
}

// New makes an authority whose lists are signed at time.Now and live an
// hour. The server closes when the test ends.
func New(t testing.TB) *Authority {
	t.Helper()
	a := &Authority{now: time.Now, life: time.Hour, revoked: map[*x509.Certificate][]*big.Int{}, serial: 100}
	a.Server = httptest.NewServer(http.HandlerFunc(a.serveCRL))
	t.Cleanup(a.Server.Close)

	a.rootKey = ecKey(t)
	a.Root = a.create(t, &x509.Certificate{
		Subject:               pkix.Name{CommonName: "Example Devices Root"},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}, nil, a.rootKey, a.rootKey.Public())

	a.intKey = ecKey(t)
	a.Intermediate = a.create(t, &x509.Certificate{
		Subject:               pkix.Name{CommonName: "Example Devices Issuing CA"},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(5 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		CRLDistributionPoints: []string{a.Server.URL + "/root.crl"},
	}, a.Root, a.rootKey, a.intKey.Public())

	a.Roots = x509.NewCertPool()
	a.Roots.AddCert(a.Root)
	return a
}

// Leaf is what a device certificate request asks for. Zero fields take
// the defaults Issue names.
type Leaf struct {
	// CN is the subject's common name. Default "device-1".
	CN string
	// Email, UPN, DNS, and URIs are the subject alternative names.
	Email, UPN string
	DNS        []string
	URIs       []string
	// RSA makes a 2048-bit RSA key in place of a P-256 one.
	RSA bool
	// NotBefore and NotAfter default to an hour ago and a day from now.
	NotBefore, NotAfter time.Time
	// NoClientAuth leaves out the client authentication key usage.
	NoClientAuth bool
	// NoCRL leaves out the CRL distribution point.
	NoCRL bool
}

// Issued is a device certificate, its key, and the chain a device sends.
type Issued struct {
	Cert  *x509.Certificate
	Key   crypto.Signer
	Chain []*x509.Certificate
}

// Issue signs a device certificate from the intermediate.
func (a *Authority) Issue(t testing.TB, l Leaf) Issued {
	t.Helper()
	if l.CN == "" {
		l.CN = "device-1"
	}
	if l.NotBefore.IsZero() {
		l.NotBefore = time.Now().Add(-time.Hour)
	}
	if l.NotAfter.IsZero() {
		l.NotAfter = time.Now().Add(24 * time.Hour)
	}
	var key crypto.Signer = ecKey(t)
	if l.RSA {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		key = k
	}
	tmpl := &x509.Certificate{
		Subject:   pkix.Name{CommonName: l.CN},
		NotBefore: l.NotBefore,
		NotAfter:  l.NotAfter,
		KeyUsage:  x509.KeyUsageDigitalSignature,
	}
	if !l.NoClientAuth {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	if !l.NoCRL {
		tmpl.CRLDistributionPoints = []string{a.Server.URL + "/issuing.crl"}
	}
	if l.Email != "" || l.UPN != "" || len(l.DNS) > 0 || len(l.URIs) > 0 {
		tmpl.ExtraExtensions = []pkix.Extension{san(t, l)}
	}
	cert := a.create(t, tmpl, a.Intermediate, a.intKey, key.Public())
	return Issued{Cert: cert, Key: key, Chain: []*x509.Certificate{cert, a.Intermediate}}
}

// Revoke puts cert on its issuer's list, the root's for the intermediate
// and the intermediate's for a device.
func (a *Authority) Revoke(cert *x509.Certificate) {
	a.mu.Lock()
	defer a.mu.Unlock()
	issuer := a.Intermediate
	if cert.Equal(a.Intermediate) {
		issuer = a.Root
	}
	a.revoked[issuer] = append(a.revoked[issuer], cert.SerialNumber)
}

// SignLists sets when each list served from now on is signed, and how long
// it lives.
func (a *Authority) SignLists(at func() time.Time, life time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.now, a.life = at, life
}

// Down makes the list server answer 503, or answer again.
func (a *Authority) Down(down bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.down = down
}

func (a *Authority) serveCRL(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.down {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	issuer, key := a.Intermediate, a.intKey
	switch r.URL.Path {
	case "/root.crl":
		issuer, key = a.Root, a.rootKey
	case "/issuing.crl":
	default:
		http.NotFound(w, r)
		return
	}
	at := a.now()
	tmpl := &x509.RevocationList{Number: big.NewInt(at.Unix()), ThisUpdate: at, NextUpdate: at.Add(a.life)}
	for _, serial := range a.revoked[issuer] {
		tmpl.RevokedCertificateEntries = append(tmpl.RevokedCertificateEntries, x509.RevocationListEntry{SerialNumber: serial, RevocationTime: at})
	}
	der, err := x509.CreateRevocationList(rand.Reader, tmpl, issuer, key)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/pkix-crl")
	w.Write(der)
}

func (a *Authority) create(t testing.TB, tmpl, parent *x509.Certificate, signer crypto.Signer, pub crypto.PublicKey) *x509.Certificate {
	t.Helper()
	a.mu.Lock()
	a.serial++
	tmpl.SerialNumber = big.NewInt(a.serial)
	a.mu.Unlock()
	if parent == nil {
		parent = tmpl
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func ecKey(t testing.TB) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// san encodes the subject alternative names by hand, because crypto/x509
// cannot write an otherName.
func san(t testing.TB, l Leaf) pkix.Extension {
	t.Helper()
	var names []asn1.RawValue
	if l.Email != "" {
		names = append(names, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 1, Bytes: []byte(l.Email)})
	}
	for _, d := range l.DNS {
		names = append(names, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 2, Bytes: []byte(d)})
	}
	for _, u := range l.URIs {
		if _, err := url.Parse(u); err != nil {
			t.Fatal(err)
		}
		names = append(names, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 6, Bytes: []byte(u)})
	}
	if l.UPN != "" {
		value, err := asn1.MarshalWithParams(l.UPN, "utf8")
		if err != nil {
			t.Fatal(err)
		}
		explicit, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: value})
		if err != nil {
			t.Fatal(err)
		}
		id, err := asn1.Marshal(device.OIDUPN)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: append(id, explicit...)})
	}
	value, err := asn1.Marshal(names)
	if err != nil {
		t.Fatal(err)
	}
	return pkix.Extension{Id: asn1.ObjectIdentifier{2, 5, 29, 17}, Value: value}
}
