package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/orchard-suite/platformkit/telemetry"
)

// Receipt is the ledger's answer for one event: stored, duplicate, or
// refused with a reason.
type Receipt struct {
	Index     int    `json:"index"`
	ID        string `json:"id,omitempty"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	Error     string `json:"error,omitempty"`
	EventKey  string `json:"event_key,omitempty"`
	Duplicate bool   `json:"duplicate,omitempty"`
}

// Client posts one source's events to one ledger. A nil Client posts
// nothing, so a product without a ledger calls it like any other.
type Client struct {
	url    string
	token  string
	source string
	http   *http.Client
}

// New returns a client for the ledger's API at url (its token port), or
// nil when url or token is empty. source is the source the token was
// issued for.
func New(url, token, source string) *Client {
	if url == "" || token == "" {
		return nil
	}
	return &Client{url: strings.TrimRight(url, "/"), token: token, source: source, http: telemetry.Client(10 * time.Second)}
}

// Post sends one event. It returns nil when the ledger stored it or
// already had it. An event that breaks the contract is refused before it
// is sent, and one the ledger refuses comes back as an *ErrInvalid with
// the ledger's reason; posting either again would not help.
func (c *Client) Post(ctx context.Context, ev Event) error {
	if c == nil {
		return nil
	}
	if err := ev.Validate(c.source); err != nil {
		return err
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/v1/events", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var rc Receipt
	_ = json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&rc)
	switch {
	case res.StatusCode == http.StatusUnprocessableEntity:
		return &ErrInvalid{Reason: rc.Reason, Detail: rc.Error}
	case res.StatusCode >= 300:
		return fmt.Errorf("ledger answered %d %s", res.StatusCode, rc.Error)
	}
	return nil
}

// Clip keeps text to one line within the ledger's limit, in bytes, for a
// title, a summary, or a reason. It never cuts a character in half.
func Clip(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)
	if len(s) <= MaxTitle {
		return s
	}
	cut := MaxTitle
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
