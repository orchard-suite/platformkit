package device

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The assertion's protected header values.
const (
	AlgES256 = "ES256"
	AlgPS256 = "PS256"
	Type     = "tender+jwt"
)

// maxChain is the most certificates an assertion may carry.
const maxChain = 5

var b64 = base64.RawURLEncoding

// Claims are what a device asserts about one request.
type Claims struct {
	// Audience is the member's origin.
	Audience string `json:"aud"`
	// Method is the HTTP method, upper case.
	Method string `json:"htm"`
	// URL is the full request URL without the query.
	URL string `json:"htu"`
	// BodyHash is BodyHash of the exact body bytes.
	BodyHash string `json:"bh"`
	// IssuedAt and Expires are seconds since the epoch; Expires is at most
	// IssuedAt + 300.
	IssuedAt int64 `json:"iat"`
	Expires  int64 `json:"exp"`
	// ID is 16 random bytes, base64url.
	ID string `json:"jti"`
}

type header struct {
	Alg  string   `json:"alg"`
	Typ  string   `json:"typ"`
	X5c  []string `json:"x5c"`
	Crit []string `json:"crit,omitempty"`
}

// BodyHash is the bh claim for body: base64url SHA-256, no padding.
func BodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return b64.EncodeToString(sum[:])
}

// NewClaims makes the claims for req at now, with a fresh jti and the
// longest life. It reads req's body to hash it and puts the same bytes
// back. req.URL must be absolute, as a client request's is.
func NewClaims(req *http.Request, audience string, now time.Time) (Claims, error) {
	if !req.URL.IsAbs() {
		return Claims{}, errors.New("device: the request URL must be absolute")
	}
	var body []byte
	if req.Body != nil {
		var err error
		if body, err = io.ReadAll(req.Body); err != nil {
			return Claims{}, fmt.Errorf("device: read body: %w", err)
		}
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
		req.ContentLength = int64(len(body))
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return Claims{}, fmt.Errorf("device: jti: %w", err)
	}
	u := url.URL{Scheme: req.URL.Scheme, Host: req.URL.Host, Path: req.URL.Path, RawPath: req.URL.RawPath}
	return Claims{
		Audience: strings.TrimSuffix(audience, "/"),
		Method:   strings.ToUpper(req.Method),
		URL:      u.String(),
		BodyHash: BodyHash(body),
		IssuedAt: now.Unix(),
		Expires:  now.Add(MaxLife).Unix(),
		ID:       b64.EncodeToString(id),
	}, nil
}

// Encode signs claims with key into a compact JWS, with chain (the device
// certificate first, then any intermediates, never the root) in x5c. An
// ECDSA P-256 key signs ES256 and an RSA key signs PS256. key may be any
// crypto.Signer, such as a key held in the operating system's store.
func Encode(key crypto.Signer, chain []*x509.Certificate, claims Claims) (string, error) {
	if len(chain) == 0 {
		return "", errors.New("device: the chain is empty")
	}
	h := header{Typ: Type}
	switch pub := key.Public().(type) {
	case *ecdsa.PublicKey:
		if pub.Curve != elliptic.P256() {
			return "", errors.New("device: an ECDSA key must be P-256")
		}
		h.Alg = AlgES256
	case *rsa.PublicKey:
		h.Alg = AlgPS256
	default:
		return "", fmt.Errorf("device: unsupported key %T", pub)
	}
	for _, c := range chain {
		h.X5c = append(h.X5c, base64.StdEncoding.EncodeToString(c.Raw))
	}
	hj, err := json.Marshal(h)
	if err != nil {
		return "", err
	}
	cj, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	input := b64.EncodeToString(hj) + "." + b64.EncodeToString(cj)
	digest := sha256.Sum256([]byte(input))
	var sig []byte
	if h.Alg == AlgPS256 {
		sig, err = key.Sign(rand.Reader, digest[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: crypto.SHA256})
	} else {
		var der []byte
		if der, err = key.Sign(rand.Reader, digest[:], crypto.SHA256); err == nil {
			sig, err = rawECDSA(der)
		}
	}
	if err != nil {
		return "", fmt.Errorf("device: sign: %w", err)
	}
	return input + "." + b64.EncodeToString(sig), nil
}

// Sign sets req's Authorization header to a fresh assertion for audience,
// signed by key with chain beside it. It reads req's body to hash it and
// puts the same bytes back.
func Sign(req *http.Request, key crypto.Signer, chain []*x509.Certificate, audience string) error {
	claims, err := NewClaims(req, audience, time.Now())
	if err != nil {
		return err
	}
	token, err := Encode(key, chain, claims)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", Scheme+" "+token)
	return nil
}

type ecdsaSignature struct{ R, S *big.Int }

// rawECDSA turns an ASN.1 ECDSA signature into JWS's fixed 64 bytes, r then s.
func rawECDSA(der []byte) ([]byte, error) {
	var s ecdsaSignature
	if rest, err := asn1.Unmarshal(der, &s); err != nil || len(rest) > 0 {
		return nil, errors.New("malformed ECDSA signature")
	}
	out := make([]byte, 64)
	if s.R.BitLen() > 256 || s.S.BitLen() > 256 {
		return nil, errors.New("ECDSA signature is not P-256")
	}
	s.R.FillBytes(out[:32])
	s.S.FillBytes(out[32:])
	return out, nil
}

// assertion is a parsed, not yet trusted, JWS.
type assertion struct {
	alg    string
	input  []byte
	sig    []byte
	chain  []*x509.Certificate
	claims Claims
}

func parse(token string) (*assertion, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("not a compact JWS")
	}
	hj, err := b64.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("header: %w", err)
	}
	var h header
	if err := json.Unmarshal(hj, &h); err != nil {
		return nil, fmt.Errorf("header: %w", err)
	}
	if h.Alg != AlgES256 && h.Alg != AlgPS256 {
		return nil, fmt.Errorf("alg %q", h.Alg)
	}
	if h.Typ != Type {
		return nil, fmt.Errorf("typ %q", h.Typ)
	}
	if len(h.Crit) > 0 {
		return nil, errors.New("crit is not understood")
	}
	if len(h.X5c) == 0 || len(h.X5c) > maxChain {
		return nil, fmt.Errorf("x5c holds %d certificates", len(h.X5c))
	}
	a := &assertion{alg: h.Alg, input: []byte(parts[0] + "." + parts[1])}
	for i, s := range h.X5c {
		der, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("x5c[%d]: %w", i, err)
		}
		c, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("x5c[%d]: %w", i, err)
		}
		a.chain = append(a.chain, c)
	}
	cj, err := b64.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("claims: %w", err)
	}
	if err := json.Unmarshal(cj, &a.claims); err != nil {
		return nil, fmt.Errorf("claims: %w", err)
	}
	if a.sig, err = b64.DecodeString(parts[2]); err != nil {
		return nil, fmt.Errorf("signature: %w", err)
	}
	return a, nil
}

// verifySignature checks the JWS against the device certificate's key, which
// must be the kind alg names.
func (a *assertion) verifySignature() error {
	digest := sha256.Sum256(a.input)
	switch pub := a.chain[0].PublicKey.(type) {
	case *ecdsa.PublicKey:
		if a.alg != AlgES256 || pub.Curve != elliptic.P256() {
			return fmt.Errorf("%s with an ECDSA %s key", a.alg, pub.Curve.Params().Name)
		}
		if len(a.sig) != 64 {
			return errors.New("ES256 signature is not 64 bytes")
		}
		r, s := new(big.Int).SetBytes(a.sig[:32]), new(big.Int).SetBytes(a.sig[32:])
		if !ecdsa.Verify(pub, digest[:], r, s) {
			return errors.New("bad signature")
		}
	case *rsa.PublicKey:
		if a.alg != AlgPS256 {
			return fmt.Errorf("%s with an RSA key", a.alg)
		}
		if pub.N.BitLen() < 2048 {
			return fmt.Errorf("RSA key of %d bits", pub.N.BitLen())
		}
		// Any salt length: some TPMs sign PSS with the longest salt.
		if err := rsa.VerifyPSS(pub, crypto.SHA256, digest[:], a.sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthAuto}); err != nil {
			return errors.New("bad signature")
		}
	default:
		return fmt.Errorf("unsupported key %T", pub)
	}
	return nil
}
