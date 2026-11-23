// Package door reads the person the front door signed in. The door answers
// Traefik's forward auth with the person's email and the roles they hold on
// the app, and Traefik replaces any copy of those headers a caller sent. An
// app trusts them only on a port its network policy keeps to Traefik.
//
// A role is an app role named `<app>.<Role>` on the door's sign-in
// registration. The door strips the app's part: `annals.Admin` arrives as
// `Admin`. A gate is configured with the bare role, and NewGate refuses a
// role with the app's part still on it.
package door

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
)

// The headers the door sets on every request it lets through.
const (
	HeaderEmail  = "X-Forwarded-Email"
	HeaderGroups = "X-Forwarded-Groups"
)

// Person is who the door let in, with their roles on this app.
type Person struct {
	Email string
	Roles []string
}

// Has says whether the person holds role.
func (p Person) Has(role string) bool { return slices.Contains(p.Roles, role) }

// HasAny says whether the person holds any of roles.
func (p Person) HasAny(roles ...string) bool {
	return slices.ContainsFunc(roles, p.Has)
}

// Read returns the person the request names, and false when the door named
// nobody.
func Read(r *http.Request) (Person, bool) {
	email := strings.TrimSpace(r.Header.Get(HeaderEmail))
	if email == "" {
		return Person{}, false
	}
	return Person{Email: email, Roles: ParseRoles(r.Header.Get(HeaderGroups))}, true
}

// ParseRoles splits the door's comma-separated role list.
func ParseRoles(header string) []string {
	var roles []string
	for _, role := range strings.Split(header, ",") {
		if role = strings.TrimSpace(role); role != "" {
			roles = append(roles, role)
		}
	}
	return roles
}

type personKey struct{}

// From returns the person a gate admitted, or the zero Person outside one.
func From(ctx context.Context) Person {
	p, _ := ctx.Value(personKey{}).(Person)
	return p
}

// With returns ctx carrying p, as a gate leaves it.
func With(ctx context.Context, p Person) context.Context {
	return context.WithValue(ctx, personKey{}, p)
}

// Refusal writes a refused request's answer. message is plain prose for
// the person.
type Refusal func(w http.ResponseWriter, r *http.Request, status int, message string)

// Gate admits requests the door signed in, holding one of its roles.
type Gate struct {
	roles []string
	log   *slog.Logger
	// Refuse writes a refusal. Nil writes the message as plain text.
	Refuse Refusal
}

// NewGate makes a gate that admits a person holding any of roles; with no
// roles it admits anyone the door signed in. A role carrying the app's part
// (`annals.Admin`) is an error, because the door never sends one.
func NewGate(log *slog.Logger, roles ...string) (*Gate, error) {
	for _, role := range roles {
		if role == "" || strings.ContainsAny(role, ". ,") {
			return nil, fmt.Errorf("door: role %q must be the bare role, such as Admin for the app role annals.Admin", role)
		}
	}
	return &Gate{roles: roles, log: log}, nil
}

// Wrap admits a request to next, with the person in its context. Wrap
// each route, not the mux, so the log names the route.
func (g *Gate) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := Read(r)
		if !ok {
			g.log.WarnContext(r.Context(), "door refused: no identity", "route", r.Pattern)
			g.refuse(w, r, http.StatusUnauthorized, "Sign in through the house first.")
			return
		}
		if len(g.roles) > 0 && !p.HasAny(g.roles...) {
			g.log.WarnContext(r.Context(), "door refused: no role", "route", r.Pattern, "want", g.roles, "held", p.Roles)
			g.refuse(w, r, http.StatusForbidden, "This is not yours to open.")
			return
		}
		g.log.DebugContext(r.Context(), "door admitted", "route", r.Pattern, "held", p.Roles)
		next.ServeHTTP(w, r.WithContext(With(r.Context(), p)))
	})
}

// Func is Wrap for a handler function.
func (g *Gate) Func(next http.HandlerFunc) http.Handler { return g.Wrap(next) }

func (g *Gate) refuse(w http.ResponseWriter, r *http.Request, status int, message string) {
	if g.Refuse != nil {
		g.Refuse(w, r, status, message)
		return
	}
	http.Error(w, message, status)
}

// SameOrigin admits a change only from the app's own pages, origin being
// its public origin such as https://annals.example.org. Sibling hosts of one
// site share cookies, so a cookie alone cannot tell them apart.
func SameOrigin(origin string, log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Origin"); got != origin {
			log.WarnContext(r.Context(), "change refused: foreign origin", "route", r.Pattern, "origin", got)
			http.Error(w, "This change must come from the app's own pages.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
