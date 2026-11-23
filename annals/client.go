package annals

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

	"github.com/orchard-suite/platformkit/v3/telemetry"
)

// Receipt is Annals' answer for one event: stored, duplicate, or
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

// Client posts one source's events to one Annals. A nil Client posts
// nothing, so a product without Annals calls it like any other.
type Client struct {
	url    string
	token  string
	source string
	http   *http.Client
}

// New returns a client for Annals' API at url (its token port), or
// nil when url or token is empty. source is the source the token was
// issued for.
func New(url, token, source string) *Client {
	if url == "" || token == "" {
		return nil
	}
	return &Client{url: strings.TrimRight(url, "/"), token: token, source: source, http: telemetry.Client(10 * time.Second)}
}

// Post sends one event. It returns nil when Annals stored it or
// already had it. An event that breaks the contract is refused before it
// is sent, and one Annals refuses comes back as an *ErrInvalid with
// Annals' reason; posting either again would not help.
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
		return fmt.Errorf("annals answered %d %s", res.StatusCode, rc.Error)
	}
	return nil
}

// PostBatch sends events in batches of at most MaxBatch and returns one
// receipt per event, in order. An event that breaks the contract is not sent
// and comes back refused with its reason, beside the others. An error means a
// batch could not be posted at all; the receipts before it stand.
func (c *Client) PostBatch(ctx context.Context, evs []Event) ([]Receipt, error) {
	out := make([]Receipt, len(evs))
	if c == nil {
		return out, nil
	}
	for start := 0; start < len(evs); start += MaxBatch {
		end := min(start+MaxBatch, len(evs))
		var send []Event
		var at []int
		for i := start; i < end; i++ {
			ev := evs[i]
			out[i] = Receipt{Index: i, EventKey: ev.EventKey}
			if err := ev.Validate(c.source); err != nil {
				out[i].Status, out[i].Reason, out[i].Error = "refused", Reason(err), err.Error()
				continue
			}
			send = append(send, ev)
			at = append(at, i)
		}
		if len(send) == 0 {
			continue
		}
		receipts, err := c.postArray(ctx, send)
		if err != nil {
			return out, err
		}
		for j, rc := range receipts {
			if j < len(at) {
				rc.Index = at[j]
				out[at[j]] = rc
			}
		}
	}
	return out, nil
}

func (c *Client) postArray(ctx context.Context, evs []Event) ([]Receipt, error) {
	body, err := json.Marshal(evs)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/v1/events", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var answer struct {
		Results []Receipt `json:"results"`
		Error   string    `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&answer)
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("annals answered %d %s", res.StatusCode, answer.Error)
	}
	return answer.Results, nil
}

// Clip makes text fit a title, a summary, or a reason: one line, within the
// Annals' limit in bytes, and each email address in it replaced by
// [email], since a person belongs only in a person field. It never cuts a
// character in half.
func Clip(s string) string {
	s = emailRe.ReplaceAllString(s, "[email]")
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
