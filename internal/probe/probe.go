// Package probe calls health and info endpoints in parallel.
package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/LarryBattle/envtally/internal/config"
)

// Values for Options.Only.
const (
	OnlyHealth = "health"
	OnlyInfo   = "info"
)

const maxBodyBytes = 1 << 20 // read at most 1 MiB of any response

// Target is one instance to probe.
type Target struct {
	Instance  config.Instance
	HealthURL string
	InfoURL   string // empty when the profile has no infoPath
	Headers   map[string]string
}

// Call is the outcome of one endpoint request (after retries).
type Call struct {
	URL      string
	Status   int    // HTTP status; 0 when no response arrived
	Err      string // transport error; empty when a response arrived
	Body     []byte
	Header   http.Header
	Duration time.Duration // time of the final attempt
	Retries  int           // retries used
}

// OK reports whether the call got an HTTP 2xx response.
func (c *Call) OK() bool { return c != nil && c.Err == "" && c.Status/100 == 2 }

// Result holds the calls made for one target. A nil call was not made.
type Result struct {
	Target Target
	Health *Call
	Info   *Call
}

// Options controls a probe run.
type Options struct {
	Concurrency int
	Retries     int    // retries on connection errors only, never on an HTTP response
	Only        string // "", OnlyHealth or OnlyInfo
	Log         *Logger
}

// NewClient builds the HTTP client used for probing.
func NewClient(timeout time.Duration, insecure bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // opt-in via --insecure
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

// Run probes all targets with a worker pool. Results keep the target order.
func Run(ctx context.Context, client *http.Client, targets []Target, opt Options) []Result {
	results := make([]Result, len(targets))
	workers := max(1, min(opt.Concurrency, len(targets)))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = probeOne(ctx, client, targets[i], opt)
			}
		}()
	}
	for i := range targets {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}

func probeOne(ctx context.Context, client *http.Client, t Target, opt Options) Result {
	res := Result{Target: t}
	if opt.Only != OnlyInfo {
		res.Health = call(ctx, client, t.HealthURL, t.Headers, opt)
	}
	wantInfo := t.InfoURL != "" && opt.Only != OnlyHealth
	if wantInfo && (opt.Only == OnlyInfo || res.Health.OK()) {
		res.Info = call(ctx, client, t.InfoURL, t.Headers, opt)
	}
	return res
}

// call requests one URL. It retries only when no HTTP response arrived
// (connection refused, reset, timeout). Any HTTP response, 5xx included, is final.
func call(ctx context.Context, client *http.Client, target string, headers map[string]string, opt Options) *Call {
	c := &Call{URL: target}
	for attempt := 0; ; attempt++ {
		c.Retries = attempt
		start := time.Now()
		err := attemptOnce(ctx, client, c, headers)
		c.Duration = time.Since(start)
		if err == nil {
			c.Err = ""
			break
		}
		c.Err = shortError(err)
		if attempt >= opt.Retries || ctx.Err() != nil {
			break
		}
		opt.Log.retry(c)
	}
	opt.Log.call(c, headers)
	return c
}

func attemptOnce(ctx context.Context, client *http.Client, c *Call, headers map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json, */*;q=0.5")
	req.Header.Set("User-Agent", "envtally")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() // nothing useful to do on close failure
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	c.Status, c.Header, c.Body = resp.StatusCode, resp.Header, body
	return nil
}

// shortError drops the `Get "url":` prefix Go adds; the URL is shown elsewhere.
func shortError(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Timeout() {
			return "timeout"
		}
		return urlErr.Err.Error()
	}
	return err.Error()
}
