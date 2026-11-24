package mockserver

import (
	"fmt"
	"net/http"
)

// flask mocks a Flask app with two JSON endpoints the app adds itself.
func flask() *appType {
	return &appType{
		name:        "flask",
		description: "Flask with JSON /health and /info",
		endpoints: []endpoint{
			{path: "/health", description: `{"status":"ok"}, or "error" with HTTP 503`, respond: flaskHealth},
			{path: "/info", description: "JSON name and version", respond: flaskInfo},
		},
		errorPage: flaskError,
	}
}

func flaskHealth(inst Instance) Response {
	if inst.State == Down {
		return jsonResponse(http.StatusServiceUnavailable, `{"status":"error","checks":{"database":"connection refused"}}`+"\n")
	}
	return jsonResponse(http.StatusOK, `{"status":"ok","checks":{"database":"ok"}}`+"\n")
}

func flaskInfo(inst Instance) Response {
	switch inst.State {
	case NoInfo:
		// The /info route was never added.
		return flaskError(inst, http.StatusNotFound, "/info")
	case Partial:
		// APP_VERSION was not set at build time, so the field is absent.
		return jsonResponse(http.StatusOK, fmt.Sprintf(`{"name":%q}`+"\n", inst.Service()))
	}
	return jsonResponse(http.StatusOK, fmt.Sprintf(`{"name":%q,"version":%q}`+"\n", inst.Service(), inst.Version))
}

// werkzeugText is the description Werkzeug prints for an HTTP error.
var werkzeugText = map[int]string{
	http.StatusUnauthorized:        "The server could not verify that you are authorized to access the URL requested. You either supplied the wrong credentials (e.g. a bad password), or your browser doesn't understand how to supply the credentials required.",
	http.StatusForbidden:           "You don't have the permission to access the requested resource. It is either read-protected or not readable by the server.",
	http.StatusNotFound:            "The requested URL was not found on the server. If you entered the URL manually please check your spelling and try again.",
	http.StatusInternalServerError: "The server encountered an internal error and was unable to complete your request. Either the server is overloaded or there is an error in the application.",
	http.StatusBadGateway:          "The proxy server received an invalid response from an upstream server.",
	http.StatusServiceUnavailable:  "The server is temporarily unable to service your request due to maintenance downtime or capacity problems. Please try again later.",
}

// flaskError is Werkzeug's default page for abort(status) and redirect().
func flaskError(inst Instance, status int, _ string) Response {
	if status/100 == 3 {
		location := inst.loginPath()
		return redirectTo(Response{Status: status, ContentType: contentHTML, Body: fmt.Sprintf(`<!doctype html>
<html lang=en>
<title>Redirecting...</title>
<h1>Redirecting...</h1>
<p>You should be redirected automatically to the target URL: <a href="%s">%s</a>. If not, click the link.
`, location, location)}, inst)
	}
	text, ok := werkzeugText[status]
	if !ok {
		text = http.StatusText(status) + "."
	}
	return Response{Status: status, ContentType: contentHTML, Body: fmt.Sprintf(`<!doctype html>
<html lang=en>
<title>%d %s</title>
<h1>%s</h1>
<p>%s</p>
`, status, http.StatusText(status), http.StatusText(status), text)}
}
