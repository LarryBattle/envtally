package mockserver

import (
	"fmt"
	"net/http"
)

// Spring Boot Actuator sends its own media type.
const contentActuator = "application/vnd.spring-boot.actuator.v3+json"

// springboot mocks Spring Boot Actuator with show-details enabled, the
// build-info goal and the git-commit-id plugin.
func springboot() *appType {
	return &appType{
		name:        "springboot",
		description: "Spring Boot Actuator",
		endpoints: []endpoint{
			{path: "/actuator/health", description: "Actuator health with components", respond: springHealth},
			{path: "/actuator/info", description: "Actuator info with build and git details", respond: springInfo},
		},
		errorPage: springError,
	}
}

const springDisk = `"diskSpace": {
      "status": "UP",
      "details": { "total": 499963174912, "free": 336420491264, "threshold": 10485760, "path": "/app/.", "exists": true }
    },
    "ping": { "status": "UP" }`

func springHealth(inst Instance) Response {
	if inst.State == Down {
		return Response{Status: http.StatusServiceUnavailable, ContentType: contentActuator, Body: `{
  "status": "DOWN",
  "components": {
    "db": {
      "status": "DOWN",
      "details": {
        "error": "org.springframework.jdbc.CannotGetJdbcConnectionException: Failed to obtain JDBC Connection"
      }
    },
    ` + springDisk + `
  }
}
`}
	}
	return Response{Status: http.StatusOK, ContentType: contentActuator, Body: `{
  "status": "UP",
  "components": {
    "db": {
      "status": "UP",
      "details": { "database": "PostgreSQL", "validationQuery": "isValid()" }
    },
    ` + springDisk + `
  }
}
`}
}

func springInfo(inst Instance) Response {
	build := fmt.Sprintf(`"build": {
    "artifact": %q,
    "name": %q,
    "time": %q,
    "version": %q,
    "group": "com.example"
  }`, inst.Service(), inst.Service(), buildTime, inst.Version)
	switch inst.State {
	case NoInfo:
		// The info endpoint is not in management.endpoints.web.exposure.include.
		return springError(inst, http.StatusNotFound, "/actuator/info")
	case Partial:
		// The git-commit-id plugin is missing, so there is no "git" section.
		return Response{Status: http.StatusOK, ContentType: contentActuator, Body: "{\n  " + build + "\n}\n"}
	}
	git := fmt.Sprintf(`"git": {
    "branch": "main",
    "commit": {
      "id": %q,
      "time": %q
    }
  }`, inst.Revision, buildTime)
	return Response{Status: http.StatusOK, ContentType: contentActuator, Body: "{\n  " + git + ",\n  " + build + "\n}\n"}
}

// springError is Spring Boot's default JSON error body. A 3xx is Spring
// Security sending the browser to the login page (no body); a 401 carries the
// Basic challenge.
func springError(inst Instance, status int, path string) Response {
	if status/100 == 3 {
		return redirectTo(Response{Status: status}, inst)
	}
	resp := jsonResponse(status, fmt.Sprintf(`{
  "timestamp": %q,
  "status": %d,
  "error": %q,
  "path": %q
}
`, now, status, http.StatusText(status), path))
	if status == http.StatusUnauthorized {
		resp.Headers = map[string]string{"WWW-Authenticate": `Basic realm="Realm"`}
	}
	return resp
}
