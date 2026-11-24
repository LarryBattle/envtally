package mockserver

import (
	"fmt"
	"net/http"
)

// rails mocks a Rails 7.1+ app: the built-in /up health check (an HTML page,
// healthy on HTTP 200) plus a small JSON /version endpoint the app adds.
func rails() *appType {
	return &appType{
		name:        "rails",
		description: "Rails /up plus a JSON /version endpoint",
		endpoints: []endpoint{
			{path: "/up", description: "Rails::HealthController: green page, or red with HTTP 500", respond: railsUp},
			{path: "/version", description: "JSON version and git revision", respond: railsVersion},
		},
		errorPage: railsError,
	}
}

func railsUp(inst Instance) Response {
	status, color := http.StatusOK, "green"
	if inst.State == Down {
		status, color = http.StatusInternalServerError, "red"
	}
	body := fmt.Sprintf("<!DOCTYPE html><html><body style=\"background-color: %s\"></body></html>", color)
	return Response{Status: status, ContentType: contentHTML, Body: body}
}

func railsVersion(inst Instance) Response {
	switch inst.State {
	case NoInfo:
		// The /version route was never added.
		return railsError(inst, http.StatusNotFound, "/version")
	case Partial:
		// REVISION is only written by the deploy tooling; without it the field is absent.
		return jsonResponse(http.StatusOK, fmt.Sprintf(`{"version":%q}`, inst.Version))
	}
	return jsonResponse(http.StatusOK, fmt.Sprintf(`{"version":%q,"revision":%q}`, inst.Version, inst.Revision))
}

// railsError is what Rails sends by default: the static pages from public/
// for errors, the "You are being redirected" body for redirect_to, and the
// plain-text denial of HTTP Basic auth.
func railsError(inst Instance, status int, _ string) Response {
	switch {
	case status/100 == 3:
		location := inst.loginPath()
		body := fmt.Sprintf(`<html><body>You are being <a href="%s">redirected</a>.</body></html>`, location)
		return redirectTo(Response{Status: status, ContentType: contentHTML, Body: body}, inst)
	case status == http.StatusUnauthorized:
		return Response{
			Status: status, ContentType: contentHTML, Body: "HTTP Basic: Access denied.\n",
			Headers: map[string]string{"WWW-Authenticate": `Basic realm="Application"`},
		}
	}
	title, text := "We're sorry, but something went wrong", "If you are the application owner check the logs for more information."
	switch status {
	case http.StatusNotFound:
		title, text = "The page you were looking for doesn't exist", "You may have mistyped the address or the page may have moved."
	case http.StatusUnprocessableEntity:
		title, text = "The change you wanted was rejected", "Maybe you tried to change something you didn't have access to."
	}
	return Response{Status: status, ContentType: contentHTML, Body: fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <title>%s (%d)</title>
</head>
<body>
  <div class="dialog">
    <h1>%s.</h1>
    <p>%s</p>
  </div>
</body>
</html>
`, title, status, title, text)}
}
