package mockserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strings"
	"time"
)

// DefaultDomain ends the fake host names when the config sets no domain.
const DefaultDomain = "example.com"

// Response is what the server sends for one request.
type Response struct {
	Status      int
	ContentType string
	Headers     map[string]string
	Body        string
}

// Server serves one profile of a config.
type Server struct {
	Profile     string
	Description string
	Domain      string

	envs []Env
	apps []*mockApp
}

type mockApp struct {
	name      string
	typ       *appType
	instances []Instance // by env (profile order), then instance
}

// New builds the server for a profile. An empty name picks the config's
// defaultProfile.
func New(cfg *Config, profile string) (*Server, error) {
	if profile == "" {
		profile = cfg.DefaultProfile
	}
	p, ok := cfg.Profiles[profile]
	if !ok {
		return nil, fmt.Errorf("unknown profile %q (available: %s)", profile, strings.Join(cfg.ProfileNames(), ", "))
	}
	c := &checker{prefix: "profiles." + profile + "."}
	types := allTypes(cfg)
	s := &Server{Profile: profile, Description: p.Description, Domain: cfg.Domain}
	if s.Domain == "" {
		s.Domain = DefaultDomain
	}
	// The envs are copied: instance defaults are filled in below.
	s.envs = append(s.envs, p.Envs...)
	if len(s.envs) == 0 {
		s.envs = append(s.envs, cfg.Envs...)
	}
	if len(s.envs) == 0 {
		c.add("envs", "at least one env is required (here or at the top level)")
	}
	known := map[string]bool{} // "env" and "env/instance"
	for i := range s.envs {
		env := &s.envs[i]
		path := fmt.Sprintf("envs[%d]", i)
		if !nameRE.MatchString(env.Name) {
			c.add(path+".name", "invalid name %q (use lowercase letters, digits, '_' and '-')", env.Name)
		}
		if known[env.Name] {
			c.add(path+".name", "duplicate env %q", env.Name)
		}
		known[env.Name] = true
		if len(env.Instances) == 0 {
			env.Instances = InstanceList{"na1"}
		}
		for _, inst := range env.Instances {
			switch {
			case !nameRE.MatchString(inst):
				c.add(path+".instances", "invalid instance name %q (use lowercase letters, digits, '_' and '-')", inst)
			case known[env.Name+"/"+inst]:
				c.add(path+".instances", "duplicate instance %q", inst)
			}
			known[env.Name+"/"+inst] = true
		}
		c.settings(path, env.Settings)
	}
	if len(p.Apps) == 0 {
		c.add("apps", "at least one app is required")
	}
	for _, name := range sortedKeys(p.Apps) {
		app, path := p.Apps[name], "apps."+name
		typ := typeFor(types, name)
		if !nameRE.MatchString(name) || typ == nil {
			c.add(path, "app names are \"<app type>-<name>\" in lowercase, without dots; known app types: %s", strings.Join(sortedKeys(types), ", "))
			continue
		}
		c.settings(path, app.Settings)
		for _, target := range sortedKeys(app.Overrides) {
			if !known[target] {
				c.add(path+".overrides."+target, "no such env or env/instance")
			}
			c.settings(path+".overrides."+target, app.Overrides[target])
		}
		entry := &mockApp{name: name, typ: typ}
		for _, env := range s.envs {
			for _, inst := range env.Instances {
				set := env.Settings.merge(app.Settings).merge(app.Overrides[env.Name]).merge(app.Overrides[env.Name+"/"+inst])
				if set.State == "" {
					set.State = Healthy
				}
				entry.instances = append(entry.instances, Instance{
					App: name, Type: typ.name, Env: env.Name, Name: inst, Domain: s.Domain,
					Version: set.Version, Revision: set.Revision, State: set.State, Status: set.Status,
					Delay: parseDelay(set.Delay), responses: set.Responses,
				})
			}
		}
		s.apps = append(s.apps, entry)
	}
	return s, c.err()
}

// Instances lists every instance: apps by name, envs in profile order.
func (s *Server) Instances() []Instance {
	var out []Instance
	for _, app := range s.apps {
		out = append(out, app.instances...)
	}
	return out
}

// Endpoints lists the endpoint paths an instance serves.
func (s *Server) Endpoints(inst Instance) []string {
	var out []string
	for _, app := range s.apps {
		if app.name == inst.App {
			for _, ep := range app.typ.endpoints {
				out = append(out, ep.path)
			}
		}
	}
	return out
}

// lookup finds the instance a fake host names. Only the first three labels
// count (<app>.<env>.<instance>), so any domain after them works; a port is
// ignored.
func (s *Server) lookup(host string) (*appType, Instance, bool) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	labels := strings.SplitN(strings.ToLower(host), ".", 4)
	if len(labels) < 3 {
		return nil, Instance{}, false
	}
	for _, app := range s.apps {
		if app.name != labels[0] {
			continue
		}
		for _, inst := range app.instances {
			if inst.Env == labels[1] && inst.Name == labels[2] {
				return app.typ, inst, true
			}
		}
	}
	return nil, Instance{}, false
}

// resolve finds the instance and endpoint a request is for. The fake host
// comes from the Host header, or else leads the path, with or without a
// scheme:
//
//	Host: flask-catalog.dev.na1.localhost:8099    GET /health
//	GET /flask-catalog.dev.na1.example.com/health
//	GET /https://flask-catalog.dev.na1.example.com/health
func (s *Server) resolve(r *http.Request) (t *appType, inst Instance, endpoint, fakeHost string, ok bool) {
	if t, inst, ok = s.lookup(r.Host); ok {
		return t, inst, r.URL.Path, r.Host, true
	}
	rest := strings.TrimPrefix(r.URL.Path, "/")
	// "https:/" covers clients and proxies that collapse the double slash.
	for _, scheme := range []string{"https://", "http://", "https:/", "http:/"} {
		if after, found := strings.CutPrefix(rest, scheme); found {
			rest = after
			break
		}
	}
	fakeHost, endpoint, _ = strings.Cut(rest, "/")
	if t, inst, ok = s.lookup(fakeHost); !ok {
		if !strings.Contains(fakeHost, ".") {
			fakeHost = r.Host // the path names no host, so the Host header was the attempt
		}
		return nil, Instance{}, "", fakeHost, false
	}
	endpoint = "/" + endpoint
	inst.base = strings.TrimSuffix(r.URL.Path, endpoint)
	return t, inst, endpoint, fakeHost, true
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t, inst, endpoint, fakeHost, ok := s.resolve(r)
	if !ok {
		if r.URL.Path == "/" {
			write(w, s.index(r))
			return
		}
		write(w, s.notFound(r, fakeHost))
		return
	}
	resp, delay := respond(t, inst, endpoint)
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return // the client gave up
		}
	}
	write(w, resp)
}

// respond picks the response for one endpoint of one instance. Order: a
// custom response for the path, then a forced status, then the app type's
// answer for the instance's state.
func respond(t *appType, inst Instance, path string) (Response, time.Duration) {
	if path == "/login" {
		// Where 3xx responses point; always served, or a forced 3xx would loop.
		return loginPage(inst), inst.Delay
	}
	typical := func(status int) Response {
		if status != 0 && status/100 != 2 {
			return t.errorPage(inst, status, path)
		}
		ep, ok := t.endpoint(path)
		if !ok {
			return t.errorPage(inst, http.StatusNotFound, path)
		}
		resp := ep.respond(inst)
		if status != 0 {
			resp.Status = status
		}
		return resp
	}
	custom, ok := inst.responses[path]
	if !ok {
		return typical(inst.Status), inst.Delay
	}
	delay := inst.Delay + parseDelay(custom.Delay)
	if custom.Body != nil {
		return render(custom, inst), delay
	}
	status := custom.Status
	if status == 0 {
		status = inst.Status
	}
	resp := typical(status)
	if custom.ContentType != "" {
		resp.ContentType = custom.ContentType
	}
	resp.Headers = mergeHeaders(resp.Headers, custom.Headers)
	return resp, delay
}

// render turns a response written in the config into a Response.
func render(r CustomResponse, inst Instance) Response {
	resp := Response{Status: r.Status, ContentType: r.ContentType, Headers: r.Headers}
	if resp.Status == 0 {
		resp.Status = http.StatusOK
	}
	var text string
	if err := json.Unmarshal(r.Body, &text); err == nil {
		// A JSON string is sent as-is.
		if resp.ContentType == "" {
			resp.ContentType = "text/plain; charset=utf-8"
		}
	} else {
		var buf bytes.Buffer
		_ = json.Indent(&buf, r.Body, "", "  ")
		text = buf.String() + "\n"
		if resp.ContentType == "" {
			resp.ContentType = contentJSON
		}
	}
	resp.Body = strings.NewReplacer(
		"${app}", inst.App, "${name}", inst.Service(), "${type}", inst.Type, "${env}", inst.Env,
		"${instance}", inst.Name, "${host}", inst.Host(), "${version}", inst.Version, "${revision}", inst.Revision,
	).Replace(text)
	return resp
}

func mergeHeaders(base, extra map[string]string) map[string]string {
	if len(extra) == 0 {
		return base
	}
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func write(w http.ResponseWriter, resp Response) {
	if resp.ContentType != "" {
		w.Header().Set("Content-Type", resp.ContentType)
	}
	for name, value := range resp.Headers {
		w.Header().Set(name, value)
	}
	w.WriteHeader(resp.Status)
	if resp.Status != http.StatusNoContent && resp.Status != http.StatusNotModified {
		_, _ = w.Write([]byte(resp.Body))
	}
}

func baseURL(r *http.Request) string {
	if r.TLS != nil {
		return "https://" + r.Host
	}
	return "http://" + r.Host
}

// loginPage is the sign-in page a redirected request lands on.
func loginPage(inst Instance) Response {
	return Response{Status: http.StatusOK, ContentType: contentHTML, Body: fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><title>Sign in - %s</title></head>
<body>
  <h1>Sign in</h1>
  <form method="post" action="%s">
    <input name="username" placeholder="Username">
    <input name="password" type="password" placeholder="Password">
    <button type="submit">Sign in</button>
  </form>
</body>
</html>
`, inst.App, inst.loginPath())}
}

// indexEntry describes one instance on the index.
type indexEntry struct {
	Host     string   `json:"host"`
	App      string   `json:"app"`
	Type     string   `json:"type"`
	Env      string   `json:"env"`
	Instance string   `json:"instance"`
	Version  string   `json:"version"`
	Revision string   `json:"revision"`
	Behavior string   `json:"behavior"`
	Links    []string `json:"urls"`
}

// behavior sums up an instance's settings for the index.
func behavior(inst Instance) string {
	parts := []string{string(inst.State)}
	if inst.Status != 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", inst.Status))
	}
	if inst.Delay > 0 {
		parts = append(parts, "delay "+inst.Delay.String())
	}
	for _, path := range sortedKeys(inst.responses) {
		parts = append(parts, "custom "+path)
	}
	return strings.Join(parts, ", ")
}

// URLs lists every endpoint URL under base, in index order. The fake host is
// in the path, so the URLs work without any DNS setup.
func (s *Server) URLs(base string) []string {
	var urls []string
	for _, entry := range s.entries(base) {
		urls = append(urls, entry.Links...)
	}
	return urls
}

func (s *Server) entries(base string) []indexEntry {
	var out []indexEntry
	for _, inst := range s.Instances() {
		entry := indexEntry{
			Host: inst.Host(), App: inst.App, Type: inst.Type, Env: inst.Env, Instance: inst.Name,
			Version: inst.Version, Revision: inst.Revision, Behavior: behavior(inst),
		}
		for _, path := range s.Endpoints(inst) {
			entry.Links = append(entry.Links, base+"/"+inst.Host()+path)
		}
		out = append(out, entry)
	}
	return out
}

var indexHTML = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html>
<head>
<title>mock-proxy-server: {{.Profile}}</title>
<style>
  body { font-family: system-ui, sans-serif; margin: 2rem; }
  table { border-collapse: collapse; }
  th, td { text-align: left; padding: 0.3rem 0.8rem; border-bottom: 1px solid #ccc; vertical-align: top; }
  code { background: #eee; padding: 0 0.2rem; }
</style>
</head>
<body>
<h1>mock-proxy-server</h1>
<p>Profile <strong>{{.Profile}}</strong>: {{.Description}}</p>
<p>Each instance has a fake host, <code>&lt;app&gt;.&lt;env&gt;.&lt;instance&gt;.{{.Domain}}</code>. Name it in the URL in any of these ways:</p>
<ul>{{range .Forms}}<li><code>{{.}}</code></li>{{end}}</ul>
<table>
<tr><th>Fake host</th><th>Version</th><th>Behavior</th><th>URLs</th></tr>
{{range .Instances}}<tr>
  <td>{{.Host}}</td><td>{{.Version}} {{.Revision}}</td><td>{{.Behavior}}</td>
  <td>{{range .Links}}<a href="{{.}}">{{.}}</a><br>{{end}}</td>
</tr>
{{end}}</table>
</body>
</html>
`))

type indexDoc struct {
	Profile     string       `json:"profile"`
	Description string       `json:"description"`
	Domain      string       `json:"domain"`
	Forms       []string     `json:"urlForms"`
	Instances   []indexEntry `json:"instances"`
}

// urlForms shows the ways a URL can name a fake host.
func (s *Server) urlForms(r *http.Request) []string {
	base := baseURL(r)
	port := ""
	if _, p, err := net.SplitHostPort(r.Host); err == nil {
		port = ":" + p
	}
	return []string{
		base + "/<app>.<env>.<instance>." + s.Domain + "/<endpoint>",
		base + "/https://<app>.<env>.<instance>." + s.Domain + "/<endpoint>",
		"http://<app>.<env>.<instance>.localhost" + port + "/<endpoint>  (any name that reaches this server; only the first three labels count)",
	}
}

// index describes what the server serves: a page with links for a browser,
// JSON for everything else.
func (s *Server) index(r *http.Request) Response {
	doc := indexDoc{
		Profile: s.Profile, Description: s.Description, Domain: s.Domain,
		Forms: s.urlForms(r), Instances: s.entries(baseURL(r)),
	}
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		var buf strings.Builder
		if err := indexHTML.Execute(&buf, doc); err == nil {
			return Response{Status: http.StatusOK, ContentType: contentHTML, Body: buf.String()}
		}
	}
	return jsonResponse(http.StatusOK, marshal(doc))
}

// notFound answers a URL that names no mock instance.
func (s *Server) notFound(r *http.Request, fakeHost string) Response {
	var hosts []string
	for _, inst := range s.Instances() {
		hosts = append(hosts, inst.Host())
	}
	return jsonResponse(http.StatusNotFound, marshal(struct {
		Error string   `json:"error"`
		Forms []string `json:"urlForms"`
		Hosts []string `json:"hosts"`
		Index string   `json:"index"`
	}{
		Error: fmt.Sprintf("no mock instance for host %q in profile %q", fakeHost, s.Profile),
		Forms: s.urlForms(r), Hosts: hosts, Index: baseURL(r) + "/",
	}))
}

// marshal encodes v as indented JSON without escaping <, > and &.
func marshal(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return buf.String()
}
