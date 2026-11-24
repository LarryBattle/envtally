package probe

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// dropFirst closes the connection without answering for the first n requests.
func dropFirst(n int32, hits *atomic.Int32, then http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= n {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		then(w, r)
	}
}

func respond(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestRetryOnConnectionErrorOnly(t *testing.T) {
	tests := []struct {
		name        string
		drop        int32 // connections dropped before answering
		status      int
		retries     int
		wantHits    int32
		wantStatus  int
		wantErr     bool
		wantRetries int
	}{
		{"success first time", 0, 200, 1, 1, 200, false, 0},
		{"one dropped connection is retried", 1, 200, 1, 2, 200, false, 1},
		{"two drops exceed one retry", 2, 200, 1, 2, 0, true, 1},
		{"retries 0 means one attempt", 1, 200, 0, 1, 0, true, 0},
		{"HTTP 500 is never retried", 0, 500, 3, 1, 500, false, 0},
		{"HTTP 503 is never retried", 0, 503, 3, 1, 503, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(dropFirst(tt.drop, &hits, respond(tt.status, `{}`)))
			defer srv.Close()

			targets := []Target{{HealthURL: srv.URL + "/health"}}
			res := Run(context.Background(), NewClient(2*time.Second, false), targets, Options{Concurrency: 1, Retries: tt.retries})
			h := res[0].Health
			if hits.Load() != tt.wantHits {
				t.Errorf("server hits = %d, want %d", hits.Load(), tt.wantHits)
			}
			if h.Status != tt.wantStatus || (h.Err != "") != tt.wantErr || h.Retries != tt.wantRetries {
				t.Errorf("got status=%d err=%q retries=%d; want status=%d err=%v retries=%d",
					h.Status, h.Err, h.Retries, tt.wantStatus, tt.wantErr, tt.wantRetries)
			}
		})
	}
}

func TestWhichEndpointsAreCalled(t *testing.T) {
	tests := []struct {
		name         string
		healthStatus int
		hasInfo      bool
		only         string
		wantHealth   bool
		wantInfo     bool
	}{
		{"both", 200, true, "", true, true},
		{"info skipped when health fails", 503, true, "", true, false},
		{"profile without infoPath", 200, false, "", true, false},
		{"only health", 200, true, OnlyHealth, true, false},
		{"only info", 503, true, OnlyInfo, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/health", respond(tt.healthStatus, `{"status":"UP"}`))
			mux.HandleFunc("/info", respond(200, `{"version":"1"}`))
			srv := httptest.NewServer(mux)
			defer srv.Close()

			target := Target{HealthURL: srv.URL + "/health"}
			if tt.hasInfo {
				target.InfoURL = srv.URL + "/info"
			}
			res := Run(context.Background(), srv.Client(), []Target{target}, Options{Concurrency: 1, Only: tt.only})[0]
			if (res.Health != nil) != tt.wantHealth || (res.Info != nil) != tt.wantInfo {
				t.Errorf("health called=%v info called=%v; want %v, %v", res.Health != nil, res.Info != nil, tt.wantHealth, tt.wantInfo)
			}
			if tt.wantInfo && string(res.Info.Body) != `{"version":"1"}` {
				t.Errorf("info body = %q", res.Info.Body)
			}
		})
	}
}

func TestRunKeepsOrderSendsHeadersAndTimes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		_, _ = w.Write([]byte(r.URL.Path + " " + r.Header.Get("X-Api-Key")))
	}))
	defer srv.Close()

	var targets []Target
	for _, name := range []string{"/a", "/b", "/c", "/d", "/e", "/f"} {
		targets = append(targets, Target{HealthURL: srv.URL + name, Headers: map[string]string{"X-Api-Key": "k-" + name}})
	}
	results := Run(context.Background(), srv.Client(), targets, Options{Concurrency: 3})
	for i, res := range results {
		path := strings.TrimPrefix(targets[i].HealthURL, srv.URL)
		if want := path + " k-" + path; string(res.Health.Body) != want {
			t.Errorf("result %d body = %q, want %q", i, res.Health.Body, want)
		}
		if res.Health.Duration < 5*time.Millisecond {
			t.Errorf("result %d duration %v is shorter than the server delay", i, res.Health.Duration)
		}
	}
}

func TestTimeoutIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()
	res := Run(context.Background(), NewClient(30*time.Millisecond, false), []Target{{HealthURL: srv.URL}}, Options{Concurrency: 1})
	if got := res[0].Health; got.Err != "timeout" || got.OK() {
		t.Errorf("got err=%q status=%d; want timeout", got.Err, got.Status)
	}
}

func TestVerboseLogRedactsSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "session=topsecret")
		w.Header().Set("X-Request-Id", "req-1")
		_, _ = w.Write([]byte(`{"echo":"` + r.Header.Get("X-Api-Key") + `","pad":"` + strings.Repeat("x", 5000) + `"}`))
	}))
	defer srv.Close()

	headers := map[string]string{"Authorization": "Bearer abc123token", "X-Api-Key": "key-98765"}
	tests := []struct {
		level   int
		want    []string
		notWant []string
	}{
		{0, nil, []string{"GET"}},
		{1, []string{"GET " + srv.URL, "HTTP 200", "retries: 0"}, []string{"X-Request-Id", "body:"}},
		{2, []string{"X-Request-Id: req-1", "Authorization: [REDACTED]", "X-Api-Key: [REDACTED]", "Set-Cookie: [REDACTED]",
			`"echo":"[REDACTED]"`, "...[truncated]"}, []string{"abc123token", "key-98765", "topsecret"}},
	}
	for _, tt := range tests {
		var buf bytes.Buffer
		opt := Options{Concurrency: 1, Log: NewLogger(&buf, tt.level)}
		Run(context.Background(), srv.Client(), []Target{{HealthURL: srv.URL, Headers: headers}}, opt)
		out := buf.String()
		for _, s := range tt.want {
			if !strings.Contains(out, s) {
				t.Errorf("level %d: log lacks %q\n%s", tt.level, s, out[:min(len(out), 600)])
			}
		}
		for _, s := range tt.notWant {
			if strings.Contains(out, s) {
				t.Errorf("level %d: log must not contain %q", tt.level, s)
			}
		}
		if tt.level == 2 && len(out) > 6000 {
			t.Errorf("level 2: body not truncated to 4 KB (log is %d bytes)", len(out))
		}
	}
}
