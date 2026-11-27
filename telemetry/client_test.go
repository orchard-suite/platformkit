package telemetry

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Parallel calls reuse the connections a Client keeps, round after round,
// instead of opening one for every call past Go's default two.
func TestClientReusesConnectionsInParallel(t *testing.T) {
	var opened atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			opened.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()
	c := Client(5 * time.Second)
	const parallel, rounds = 16, 5
	for range rounds {
		var wg sync.WaitGroup
		for range parallel {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := c.Get(srv.URL)
				if err != nil {
					t.Error(err)
					return
				}
				io.Copy(io.Discard, res.Body)
				res.Body.Close()
			}()
		}
		wg.Wait()
	}
	if n := opened.Load(); n > parallel {
		t.Fatalf("%d calls in rounds of %d opened %d connections", parallel*rounds, parallel, n)
	}
}
