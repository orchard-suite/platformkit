package device

import (
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"strings"
)

var (
	oidSAN = asn1.ObjectIdentifier{2, 5, 29, 17}
	// OIDUPN is the user principal name otherName.
	OIDUPN = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 20, 2, 3}
)

// source reads one value from a certificate, or "".
type source func(*x509.Certificate) string

// selector is a list of sources; the first that is present wins.
type selector []source

func (s selector) read(c *x509.Certificate) string {
	for _, src := range s {
		if v := strings.TrimSpace(src(c)); v != "" {
			return v
		}
	}
	return ""
}

func parseSelector(spec, fallback string) (selector, error) {
	if strings.TrimSpace(spec) == "" {
		spec = fallback
	}
	var sel selector
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		switch {
		case part == "cn":
			sel = append(sel, func(c *x509.Certificate) string { return c.Subject.CommonName })
		case part == "email":
			sel = append(sel, func(c *x509.Certificate) string { return first(c.EmailAddresses) })
		case part == "upn":
			sel = append(sel, upn)
		case part == "dns":
			sel = append(sel, func(c *x509.Certificate) string { return first(c.DNSNames) })
		case part == "uri":
			sel = append(sel, func(c *x509.Certificate) string {
				if len(c.URIs) == 0 {
					return ""
				}
				return c.URIs[0].String()
			})
		case strings.HasPrefix(part, "uri:") && len(part) > len("uri:"):
			scheme := part[len("uri:"):]
			sel = append(sel, func(c *x509.Certificate) string {
				for _, u := range c.URIs {
					if strings.EqualFold(u.Scheme, scheme) {
						return strings.TrimPrefix(u.String(), u.Scheme+"://")
					}
				}
				return ""
			})
		default:
			return nil, fmt.Errorf("%q is not one of cn, email, upn, dns, uri, uri:<scheme>", part)
		}
	}
	return sel, nil
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// name is the device's display name: the first SAN DNS name, else the CN.
func name(c *x509.Certificate) string {
	if dns := first(c.DNSNames); dns != "" {
		return dns
	}
	return c.Subject.CommonName
}

// upn reads the first user principal name otherName from the certificate's
// subject alternative names, which crypto/x509 does not parse.
func upn(c *x509.Certificate) string {
	for _, ext := range c.Extensions {
		if !ext.Id.Equal(oidSAN) {
			continue
		}
		var names asn1.RawValue
		if _, err := asn1.Unmarshal(ext.Value, &names); err != nil || names.Tag != asn1.TagSequence {
			return ""
		}
		for rest := names.Bytes; len(rest) > 0; {
			var gn asn1.RawValue
			var err error
			if rest, err = asn1.Unmarshal(rest, &gn); err != nil {
				return ""
			}
			// otherName [0] { type-id OID, value [0] EXPLICIT ANY }
			if gn.Class != asn1.ClassContextSpecific || gn.Tag != 0 {
				continue
			}
			var id asn1.ObjectIdentifier
			inner, err := asn1.Unmarshal(gn.Bytes, &id)
			if err != nil || !id.Equal(OIDUPN) {
				continue
			}
			var value asn1.RawValue
			if _, err := asn1.Unmarshal(inner, &value); err != nil || value.Class != asn1.ClassContextSpecific || value.Tag != 0 {
				continue
			}
			var s string
			if _, err := asn1.Unmarshal(value.Bytes, &s); err == nil {
				return s
			}
		}
	}
	return ""
}
