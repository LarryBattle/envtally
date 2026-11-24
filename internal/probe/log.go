package probe

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxLoggedBody = 4 << 10 // -vv truncates bodies to 4 KB
	redacted      = "[REDACTED]"
)

// alwaysSecret are header names whose values are never logged.
var alwaysSecret = []string{"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie"}

// Logger writes verbose diagnostics. Level 1 (-v) logs each call; level 2
// (-vv) adds headers and bodies with secrets redacted. A nil *Logger is silent.
type Logger struct {
	mu    sync.Mutex
	w     io.Writer
	level int
}

// NewLogger returns a logger writing to w, or nil when level is 0.
func NewLogger(w io.Writer, level int) *Logger {
	if level <= 0 {
		return nil
	}
	return &Logger{w: w, level: level}
}

// Printf writes one line when the logger's level is at least level.
func (l *Logger) Printf(level int, format string, args ...any) {
	if l == nil || l.level < level {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintf(l.w, format+"\n", args...)
}

func (l *Logger) retry(c *Call) {
	l.Printf(1, "GET %s -> error: %s (retrying)", c.URL, c.Err)
}

func (l *Logger) call(c *Call, sent map[string]string) {
	if l == nil {
		return
	}
	var b strings.Builder
	outcome := fmt.Sprintf("HTTP %d", c.Status)
	if c.Err != "" {
		outcome = "error: " + c.Err
	}
	fmt.Fprintf(&b, "GET %s -> %s in %s (retries: %d)", c.URL, outcome, c.Duration.Round(time.Millisecond), c.Retries)
	if l.level >= 2 {
		secretNames := append([]string{}, alwaysSecret...)
		var secretValues []string
		sentHeader := http.Header{}
		for name, value := range sent {
			sentHeader.Set(name, value)
			secretNames = append(secretNames, name)
			secretValues = append(secretValues, value)
		}
		writeHeaders(&b, "  > ", sentHeader, secretNames)
		writeHeaders(&b, "  < ", c.Header, secretNames)
		if len(c.Body) > 0 {
			b.WriteString("\n  body: " + RedactBody(c.Body, secretValues))
		}
	}
	l.Printf(1, "%s", b.String())
}

func writeHeaders(b *strings.Builder, prefix string, h http.Header, secretNames []string) {
	names := make([]string, 0, len(h))
	for name := range h {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := strings.Join(h[name], ", ")
		for _, secret := range secretNames {
			if strings.EqualFold(name, secret) {
				value = redacted
			}
		}
		fmt.Fprintf(b, "\n%s%s: %s", prefix, name, value)
	}
}

// RedactBody truncates a body to 4 KB and masks any configured header value
// that a server echoed back.
func RedactBody(body []byte, secretValues []string) string {
	text := string(body)
	truncated := len(text) > maxLoggedBody
	if truncated {
		text = text[:maxLoggedBody]
	}
	for _, secret := range secretValues {
		if len(secret) >= 4 {
			text = strings.ReplaceAll(text, secret, redacted)
		}
	}
	text = strings.TrimSpace(text)
	if truncated {
		text += " ...[truncated]"
	}
	return text
}
