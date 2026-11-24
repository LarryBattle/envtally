# envtally

[![ci](https://github.com/LarryBattle/envtally/actions/workflows/ci.yml/badge.svg)](https://github.com/LarryBattle/envtally/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/LarryBattle/envtally.svg)](https://pkg.go.dev/github.com/LarryBattle/envtally)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**envtally tells you whether every running copy of your app is on the same version, and points at the ones that are not.**

Most apps run as several copies: a few servers per environment, often in more than one region. After a deploy they should all run the same build. Sometimes one is missed, a rollout stops halfway, or a server quietly restarts on an old version, and you get bugs that only some users see. envtally is a command-line tool that asks every copy "are you healthy, and what version are you?", then gives you one answer per app and environment: **in sync**, **drift** or **down**.

```
$ envtally check
PROJECT  APP       ENV   HOST                       STATUS  GROUP KEY      TIME
billing  payments  dev   payments.dev.na1.host.com  UP      1.4.2|a1b2c3d  110ms
billing  payments  dev   payments.dev.na2.host.com  UP      1.4.1|9f8e7d6  95ms
billing  payments  prod  payments.na1.host.com      UP      1.4.1|9f8e7d6  98ms
billing  payments  prod  payments.na2.host.com      UP      1.4.1|9f8e7d6  1.2s (slow)

SUMMARY
billing/payments/dev   ✗ DRIFT    [1.4.1|9f8e7d6 ×1] [1.4.2|a1b2c3d ×1]  [110ms, 95ms] min 95ms · avg 103ms · max 110ms
billing/payments/prod  ✓ IN SYNC  [1.4.1|9f8e7d6 ×2]                     [98ms, 1.2s] min 98ms · avg 649ms · max 1.2s

2 app/envs: 1 in sync, 1 drift
$ echo $?
1
```

Here `dev` has two instances on different builds (drift), and `prod` is consistent. The exit code is 1 because something needs attention, so a script or pipeline can act on it.

## Why use it

- **One command instead of many tabs.** No more opening `/actuator/info` on each host and comparing commit hashes by eye.
- **Nothing to install on your servers.** It only makes HTTP GET requests to health and version endpoints your apps already have. There is no agent, no database and no dashboard to run.
- **Built for scripts.** Exit codes separate "all good" (0), "drift or down" (1) and "misconfigured" (2). Output comes as a table, tree, Markdown, CSV or JSON.
- **Works with your stack.** Spring Boot, Rails and Flask are built in. Any other app type is a few lines of config, and apps with no version endpoint (Jenkins, Grafana) can be checked for "up" only.
- **One static binary** for Linux, macOS and Windows.

## What you can do with it

**See what is running everywhere, right now.**

```sh
envtally check
envtally check -i env=prod                    # only prod
envtally check -i app=payments                # only one app
envtally check -i app=payments -i env=prod    # that app, in prod
```

**Wait for a rollout to finish.** Watch mode re-checks until every instance reports the same version, then exits 0. If that does not happen in time, it exits 1.

```sh
envtally check -i app=payments --watch 30s --watch-timeout 10m
```

**Gate a pipeline on it.** Run the same command as a step after your deploy; the step fails if the rollout never converges. See [CI usage](#ci-usage).

**Find the one stale or broken instance.** List each instance by its own URL (not the load balancer's) and the table shows which host is on the old build, down or slow.

**Share the state in a pull request, ticket or chat.**

```sh
envtally check --format markdown
```

**Feed it to other tools.**

```sh
envtally check --format json | jq -r '.envs[] | select(.status != "IN SYNC") | "\(.project)/\(.app)/\(.env) \(.status)"'
```

**Look up an app's URLs and links** (Swagger, Jenkins, anything you template) without calling anything:

```sh
envtally urls -i app=payments
```

## Quick start

**1. Install.** Download the archive for your OS from the [Releases page](https://github.com/LarryBattle/envtally/releases), unpack it and put `envtally` on your `PATH`. Or, with Go installed:

```sh
go install github.com/LarryBattle/envtally/cmd/envtally@latest
```

**2. Create a config.**

```sh
envtally init          # writes a commented starter config and remembers where it is
envtally config path   # prints that location
```

**3. Describe your apps.** Open the config and replace the example with your own project, app, environments and instance URLs. A complete config can be this small:

```json
{
  "defaults": { "profile": "springboot" },
  "envOrder": ["dev", "test", "prod"],
  "projects": {
    "billing": {
      "apps": {
        "payments": {
          "envs": {
            "dev": ["https://payments.dev.na1.host.com", "https://payments.dev.na2.host.com"],
            "prod": ["https://payments.na1.host.com", "https://payments.na2.host.com"]
          }
        }
      }
    }
  }
}
```

Give base URLs only. The **profile** is the app type: it decides which paths are called and how the version is read. Use `springboot`, `rails` or `flask`, or [define your own](#profiles).

**4. Check.**

```sh
envtally config validate   # catches typos, with the JSON path of each problem
envtally check             # calls every instance and reports
```

From here: narrow the run with [filters](#filters), wait for a rollout with [watch mode](#watch-mode), or pick an [output style](#output-styles). `envtally --help` lists the common tasks.

For a starter config that also shows auth headers, a status-only app and a custom app type, run `envtally init --example full --print`.

### Try it without real services

The repository includes a mock server with fake Spring Boot, Rails and Flask apps, some drifting and some down. With Go installed:

```sh
git clone https://github.com/LarryBattle/envtally.git
cd envtally
go run ./cmd/mock-proxy-server                          # terminal 1: serves on 127.0.0.1:8099

go run ./cmd/mock-proxy-server envtally > mock.json     # terminal 2: a config that points at it
go run ./cmd/envtally check --config mock.json
```

`--config` remembers the path for later runs, so pass your own config with `--config` when you switch to it. More in [Mock server](#mock-server).

## How it works

```
config.json                 envtally check                        report
projects                    for every instance URL:
  apps            ──────▶     1. GET health  → up or down  ──────▶  per instance: UP / DOWN / NOINFO
    envs                      2. GET info    → version              per app/env:  IN SYNC / DRIFT / DOWN
      instance URLs           3. build the group key                exit code:    0 / 1 / 2
```

1. You list your instances in a JSON config, grouped as project → app → env → instance URLs.
2. For each instance, envtally calls the **health** endpoint. If it is healthy, it calls the **info** endpoint. Requests run in parallel.
3. From the info response it builds a **group key**: a short string such as `1.4.2|a1b2c3d` (version and commit). The profile says which JSON fields go into it.
4. Within each app/env it compares the keys. One key everywhere is **IN SYNC**. More than one is **DRIFT**. If any instance is unreachable or unhealthy, the app/env is **DOWN**.
5. It prints the report and sets the exit code.

envtally only reads. It never deploys, restarts or changes anything. The exact rules are in [How `check` decides](#how-check-decides).

## FAQ

**My app is not Spring Boot, Rails or Flask. Can I use it?**
Yes. Any app with an HTTP health endpoint works, and a JSON endpoint that reports a version lets envtally compare builds. Add a profile with your paths and fields: see [Profiles](#profiles).

**My app has no version endpoint.**
Use a [status-only profile](#status-only-profiles). envtally then reports `UP` or `DOWN` and skips the version comparison.

**My endpoints need a token or login.**
Add `headers` to the profile and read the secret from an environment variable with `${env:NAME}`, so it never sits in the config file. See [Request headers](#request-headers).

**Does it compare dev against prod?**
No. Environments are expected to differ, so the comparison is between the instances of one app in one environment. The table still shows every environment's version side by side.

**An environment has only one instance. Is that useful?**
It cannot drift, so it is always in sync when it is up. You still get its health, version and response time.

**All my instances sit behind one load balancer URL.**
Then envtally sees whichever instance answers. To compare instances, list URLs that reach each one directly.

**What do `NOINFO` and `UNKNOWN` mean?**
`NOINFO`: the instance is healthy but the version endpoint failed or did not return JSON. `UNKNOWN`: every instance answered, but a field named in the group key was missing. Both are explained in [How `check` decides](#how-check-decides); run with `-v` or `-vv` to see the requests ([Verbose mode](#verbose-mode)).

**We use self-signed or internal certificates.**
Pass `-k` (`--insecure`), or set `defaults.insecure` in the config.

**Which config file is it using?**
Every run prints `Using config: <path>` first, and `envtally config path` prints it on demand. To use another file, pass `--config <path>` (remembered) or set `ENVTALLY_CONFIG` (not remembered, good for CI). See [Config location](#config-location).

**How do I tell "drift" from "my setup is broken" in a script?**
By exit code: 1 is drift or down, 2 is a config or usage error. See [Exit codes](#exit-codes).

**Can I keep notes in the config?**
Yes. Any key that starts with `.comment` is ignored. See [Config reference](#config-reference).

## Contents

Reference for everything above.

- [Config reference](#config-reference)
- [Templates and variables](#templates-and-variables)
- [Profiles](#profiles)
- [Config location](#config-location)
- [Commands](#commands)
- [Filters](#filters)
- [How `check` decides](#how-check-decides)
- [Output styles](#output-styles)
- [Watch mode](#watch-mode)
- [Verbose mode](#verbose-mode)
- [Exit codes](#exit-codes)
- [CI usage](#ci-usage)
- [Development](#development)
- [Contributing](#contributing)
- [License](#license)

## Config reference

The config is JSON. `envtally init` writes `envtally.schema.json` next to the config, so editors that understand `"$schema"` autocomplete and validate it. `envtally config schema` prints the schema.

Unknown fields are rejected, so a typo such as `"projcts"` fails loudly instead of being ignored.

**Comments.** JSON has no comments, so envtally ignores every key that starts with `.comment`, in any object. The value is a string, or an array of strings for several lines. To put several notes in one object, give them different names:

```json
"defaults": {
  ".comment": "Settings for every app.",
  ".comment-timeout": ["Our gateway is slow on cold starts,", "so wait longer than the default 5s."],
  "timeout": "15s"
}
```

Comments are for people reading the file; `envtally config show` prints the config without them. The `"description"` field on projects, apps and profiles is still there for a one-line label.

```json
{
  "$schema": "./envtally.schema.json",
  "defaults": {
    "output": "table",
    "profile": "springboot",
    "concurrency": 10,
    "timeout": "5s",
    "retries": 1,
    "insecure": false,
    "slowThreshold": "1s"
  },
  "envOrder": ["dev", "test", "prod"],
  "vars": {
    "jenkins_host": "https://jenkins.example.com"
  },
  "templates": {
    "swagger": "${base_url}/swagger-ui/index.html",
    "jenkins": "${jenkins_host}/job/${project}/job/${app}/"
  },
  "profiles": {
    "springboot": {
      "description": "Spring Boot Actuator",
      "healthPath": "/actuator/health",
      "infoPath": "/actuator/info",
      "healthyWhen": { "field": "status", "equals": "UP" },
      "groupKey": "${build.version}|${git.commit.id}",
      "headers": {}
    }
  },
  "projects": {
    "billing": {
      "description": "Billing team services",
      "templates": {
        "jenkins": "${jenkins_host}/job/billing-team/job/${app}/"
      },
      "apps": {
        "payments": {
          "profile": "springboot",
          "envs": {
            "dev": ["https://payments.dev.na1.host.com", "https://payments.dev.na2.host.com"],
            "test": ["https://payments.test.na1.host.com"],
            "prod": "https://payments.host.com"
          }
        }
      }
    }
  }
}
```

| Field | Default | Meaning |
|---|---|---|
| `defaults.output` | `table` | Output style: `table`, `tree`, `compact`, `markdown`, `csv`, `json`. |
| `defaults.profile` | none | Profile for apps that do not set one. |
| `defaults.concurrency` | `10` | Parallel requests. |
| `defaults.timeout` | `5s` | Per-request timeout (Go duration: `500ms`, `5s`, `1m`). |
| `defaults.retries` | `1` | Retries when **no HTTP response** arrives (refused, reset, timeout). An HTTP response, 5xx included, is never retried. |
| `defaults.insecure` | `false` | Skip TLS certificate verification. |
| `defaults.slowThreshold` | `1s` | An instance whose total time (health + info) exceeds this is marked `(slow)`. |
| `envOrder` | alphabetical | Display order of envs. Envs not listed follow alphabetically. |
| `vars` | none | Literal values for `${name}` in link templates and header values. |
| `templates` | none | Named links (any names; `swagger` and `jenkins` are just examples). Allowed at top level, per project and per app. |
| `profiles.<name>` | built-ins | App types. See [Profiles](#profiles). |
| `projects.<name>.description` | none | Free text. |
| `projects.<name>.templates` | none | Overrides top-level templates for this project. |
| `projects.<name>.apps.<name>.profile` | `defaults.profile` | Profile for this app. |
| `projects.<name>.apps.<name>.templates` | none | Overrides project templates for this app. |
| `projects.<name>.apps.<name>.envs.<env>` | required | One URL string, or an array of URLs. A string is treated as a one-item array. |

Instance URLs must be `http` or `https`, with no query or fragment. A trailing slash is removed. A URL may contain a path (`https://gw.example.com/payments`); the health and info paths are appended to it.

Projects and apps are shown in alphabetical order (JSON objects have no reliable order). Use `envOrder` for envs.

## Templates and variables

There is one syntax, `${...}`, used in three places. Each place has its own set of names.

| Where | What `${name}` can be | If the name is unknown |
|---|---|---|
| Link templates (`templates`) | Built-ins `${project}` `${app}` `${env}` `${host}` `${base_url}`, plus anything in `vars` | Error at `config validate` |
| Header values (`profiles.*.headers`) | Anything in `vars`, plus `${env:NAME}` (an OS environment variable, meant for secrets) | Unknown var: error at `config validate`. Unset `${env:NAME}`: error when `check` runs |
| Group keys (`profiles.*.groupKey`) | Dot-paths into the response JSON, e.g. `${build.version}` | Renders as `?` and the app/env becomes `UNKNOWN` |

- `${base_url}` is the instance URL. `${host}` is its `host[:port]`.
- A link template may be a pattern or a full literal URL.
- Override order for link templates: top-level `templates` → project `templates` → app `templates`. The most specific wins, per link name.
- `vars` are literal. A `${...}` inside a var is not expanded, and a var cannot reuse a built-in name.
- `${env:NAME}` is not allowed in link templates: links get printed and pasted, so secrets must not flow into them.

## Profiles

A profile describes one app type.

| Field | Required | Meaning |
|---|---|---|
| `healthPath` | yes | Appended to the instance URL. Healthy = HTTP 2xx. |
| `healthyWhen` | no | Also require a field of the health JSON to equal a value: `{ "field": "status", "equals": "UP" }`. |
| `infoPath` | no | A second endpoint, called when health passed; it must answer HTTP 2xx. With a `groupKey`, the key is built from its JSON (without `infoPath`, from the health JSON). |
| `groupKey` | no | Template of dot-paths into that JSON. Leave it out for a [status-only](#status-only-profiles) profile. |
| `headers` | no | Request headers sent to both endpoints. See [Request headers](#request-headers). |

Three profiles are built in. You can use them without declaring them, and a profile of the same name in your config replaces the built-in one.

| Name | healthPath | healthyWhen | infoPath | groupKey |
|---|---|---|---|---|
| `springboot` | `/actuator/health` | `status` = `UP` | `/actuator/info` | `${build.version}\|${git.commit.id}` |
| `rails` | `/up` | — | `/version` | `${version}\|${revision}` |
| `flask` | `/health` | `status` = `ok` | `/info` | `${version}` |

Only Spring Boot provides these endpoints itself (Actuator, with the `build-info` and `git-commit-id` plugins feeding `/actuator/info`). For Rails, `/up` is built in but `/version` is a small JSON endpoint you add; for Flask you add both. See `examples/`.

**Add your own app type:**

```json
"profiles": {
  "node": {
    "description": "Express services with /healthz and /meta",
    "healthPath": "/healthz",
    "infoPath": "/meta",
    "healthyWhen": { "field": "ok", "equals": "true" },
    "groupKey": "${version}|${build.sha}",
    "headers": { "Authorization": "Bearer ${env:NODE_API_TOKEN}" }
  }
}
```

### Status-only profiles

Some apps have no JSON version endpoint: Jenkins, Grafana, a static site. Leave `groupKey` out and envtally only checks the HTTP status:

```json
"profiles": {
  "jenkins": {
    "description": "Jenkins: up when the login page loads",
    "healthPath": "/login"
  }
}
```

- An instance is `UP` when `healthPath` answers HTTP 2xx. The body can be anything, HTML included.
- Add an `infoPath` to require a second path to answer 2xx as well.
- No versions are compared, so the app/env is reported as `✓ UP` or `✗ DOWN`, never `IN SYNC` or `DRIFT`.
- Redirects are followed, so the final response counts. A page that answers 401 or 403 without a login is `DOWN`; point `healthPath` at a path that is open (Jenkins: `/login`), or send credentials with `headers`.

### Request headers

`headers` is a map of HTTP header name → value. envtally sends them with every health and info request for apps that use the profile. The usual reason is authentication.

```json
"profiles": {
  "internal-api": {
    "healthPath": "/healthz",
    "headers": {
      "Authorization": "Bearer ${env:INTERNAL_API_TOKEN}",
      "X-Team": "${team}"
    }
  }
}
```

`${env:NAME}` is replaced by the OS environment variable `NAME` when envtally runs, so the secret never sits in the config file. Set it in your shell before running `check`:

```sh
export INTERNAL_API_TOKEN=abc123        # bash, zsh
$env:INTERNAL_API_TOKEN = "abc123"      # PowerShell
```

With that, the request carries `Authorization: Bearer abc123`. If the variable is not set, `check` stops and names it. `${team}` is a plain value from `vars`.

| You need | Write |
|---|---|
| Bearer token | `"Authorization": "Bearer ${env:API_TOKEN}"` |
| API key header | `"X-Api-Key": "${env:API_KEY}"` |
| Basic auth | `"Authorization": "Basic ${env:BASIC_AUTH}"`, where the variable holds the base64 of `user:password` |
| A fixed, non-secret value | `"X-Client": "envtally"` |

Headers belong to a profile, not to an app. If two apps of the same type need different headers, give them two profiles. `envtally check -vv` shows the headers that were sent, with the values replaced by `[REDACTED]`.

Dot-paths work with nested objects (`{"git":{"commit":{"id":"a1b2"}}}`) and with flat keys that contain dots (`{"git.commit.id":"a1b2"}`). Numbers and booleans are compared as text (`true`, `8080`).

## Config location

Commands that read the config first print one line to stderr: `Using config: <path> (from <source>)`.

The path is picked in this order:

1. `--config <path>`. Once the file loads and validates, its absolute path is **remembered** for later runs.
2. The `ENVTALLY_CONFIG` environment variable. Not remembered; good for CI.
3. The remembered path.

With none of these, envtally exits 2 with: ``No config file. Run `envtally init` or pass `--config <path>`.``

The remembered path lives in `state.json` inside the envtally directory: `os.UserConfigDir()/envtally` (`~/.config/envtally` on Linux, `~/Library/Application Support/envtally` on macOS, `%AppData%\envtally` on Windows). Set `ENVTALLY_HOME` to use another directory.

## Commands

| Command | Purpose |
|---|---|
| `envtally check` | Probe instances, group by key, report sync/drift. The main command. |
| `envtally urls` | Print instance URLs and links. No network calls. |
| `envtally config validate` | Check the config and print every problem with its JSON path. |
| `envtally config show` | Print the normalized config (defaults and built-in profiles filled in). |
| `envtally config path` | Print the active config path (stdout) and where it came from (stderr). |
| `envtally config schema` | Print the JSON schema. |
| `envtally init [--example basic\|full] [--force] [--print]` | Create a commented sample config and schema in the envtally directory and remember it. `--example full` adds auth headers, a status-only app and a custom app type. `--print` only prints the sample. |
| `envtally version` | Print version, commit and build date. |

Global flags (they override `defaults` in the config):

| Flag | Meaning |
|---|---|
| `-c`, `--config <path>` | Config file; remembered for later runs. |
| `-f`, `--format <format>` | `table`, `tree`, `compact`, `markdown`, `csv` or `json`. |
| `-v`, `-vv` | Verbose diagnostics on stderr. |
| `--timeout <duration>` | Per-request timeout. |
| `--concurrency <n>` | Parallel requests. |
| `-k`, `--insecure` | Skip TLS verification. |
| `--no-color` | Disable colors. `NO_COLOR` is also respected, and colors are only used on a terminal. |

`check` flags: `-i`/`--include <pattern>`, `-x`/`--exclude <pattern>` (see [Filters](#filters)), `--only health|info`, `-w`/`--watch <interval>`, `--watch-timeout <duration>`.
`urls` flags: `-i`/`--include <pattern>`, `-x`/`--exclude <pattern>`.

Every command's `--help` shows short examples, and `envtally --help` ends with the common tasks.

### `urls`

```
$ envtally urls -i env=prod
Using config: /home/me/.config/envtally/config.json (from remembered)
PROJECT  APP       ENV   URL                        JENKINS                                                     SWAGGER
billing  payments  prod  https://payments.host.com  https://jenkins.example.com/job/billing-team/job/payments/  https://payments.host.com/swagger-ui/index.html
```

`urls` supports `--format table` (also used for `tree` and `compact`), `markdown`, `csv` and `json`.

### `config validate`

```
$ envtally config validate --config broken.json
Using config: /work/broken.json (from --config)
Config has 4 problem(s):
  $.defaults.timeout: invalid duration "soon" (examples: 500ms, 5s, 1m)
  $.projects.p.apps.a.envs.dev[0]: invalid URL "not a url": scheme must be http or https
  $.projects.p.apps.a.profile: unknown profile "cobol"
  $.templates.jenkins: undefined variable ${jenkins_host}
$ echo $?
2
```

Validation is done by envtally itself (no network, no schema engine): JSON syntax, unknown fields, output style, durations, URLs, duplicate URLs, unknown profiles, template syntax and undefined variables.

### `version`

```
$ envtally version
envtally 1.0.0 (commit a1b2c3d, built 2026-01-15T10:00:00Z, go1.24.7 linux/amd64)
```

Release archives carry all three values. Other builds read what Go embeds in the binary: `go install ...@v1.0.0` knows the version but not the commit or date, and `go build` in a git checkout reports the commit (with `-dirty` if files were modified) and the commit's time as the date.

## Filters

`--include` (`-i`) and `--exclude` (`-x`) choose which instances `check` and `urls` work on. Both are repeatable. `urls` makes no network calls, so it is a safe way to preview a filter before using it with `check`.

A pattern has one of two forms:

| Form | Matches | Example |
|---|---|---|
| `FIELD=REGEX` | That field only. `FIELD` is `project`, `app`, `env`, `host` or `url`. | `-i env=prod` |
| `REGEX` | Anywhere in the project, app, env, host or URL. | `-i payments` |

`project`, `app` and `env` are the names in your config. `host` is the host (and port) of an instance URL, and `url` is the whole instance URL as written in the config, path included.

### Everyday filters

```sh
envtally check -i env=prod          # one env
envtally check -i app=payments      # one app, in every env
envtally check -i project=billing   # one project
envtally check -x env=dev           # everything except dev
envtally urls -i app=payments       # the same filters work on urls
```

`project=`, `app=` and `env=` match the **whole name**: `env=prod` selects `prod` and leaves `preprod` alone. No quotes or anchors are needed, which also keeps these safe to type in `cmd.exe` and PowerShell.

### Combining filters

Includes on **different** fields must all match. Includes on the **same** field are alternatives. Then any matching exclude removes the instance.

```sh
envtally check -i app=payments -i env=prod                 # payments in prod
envtally check -i app=payments -i app=ledger -i env=prod   # payments or ledger, in prod
envtally check -i 'app=payments|ledger' -i env=prod        # the same, as one regex
envtally check -i project=billing -x env=dev               # a project, minus dev
envtally check -i env=dev -i env=preprod                   # everything before prod
envtally check -i env=prod -x app=reports -x app=ledger    # prod, minus two apps
```

### Regex, hosts and URLs

Patterns are Go [RE2](https://github.com/google/re2/wiki/Syntax) regular expressions. `host=` and `url=` match **anywhere** in the value, because nobody wants to type a whole hostname. Quote a pattern that contains `|`, `*`, `$`, `^` or `\`.

```sh
envtally check -i 'app=pay.*'                    # apps whose name starts with pay
envtally check -i 'env=.*prod'                   # prod and preprod
envtally check -i env=prod -i 'host=\.na2\.'     # prod, one region only
envtally check -i env=prod -x 'host=\.na2\.'     # prod, skipping a region under maintenance
envtally check -i 'url=/reports/'                # instances behind a gateway path
envtally urls -i 'url=^http://'                  # instances still on plain HTTP
envtally check -i 'app=(?i)PAYMENTS'             # ignore case
```

### Plain patterns

A pattern without `FIELD=` is searched for anywhere in all five fields. It is the quickest thing to type and good for a look around, but it can select more than you meant:

```sh
envtally check -i payments          # the payments app, and anything with payments in its host or URL
envtally check -i prod              # the prod env, but also preprod and an app named products
envtally check -i '^prod$'          # anchored: only a name that is exactly prod
envtally check -i payments -i ledger           # plain patterns are alternatives too
envtally check -i payments -i env=prod         # a plain pattern combines with fields like any other
```

Prefer `FIELD=REGEX` in scripts and pipelines.

### Rules

- With no `--include`, everything is included. `--exclude` always wins over `--include`.
- An invalid regex, an unknown field (`evn=prod`), an empty pattern (often an unset variable in a script) or filters that match nothing exit with code 2. Filters never silently check the wrong set.
- A plain pattern that really starts with a word and `=` is written with `[=]`, as in `mode[=]fast`.
- `-v` prints how many instances the filters kept.

### One endpoint type

`--only` is separate from the filters: it chooses which endpoint is called on the selected instances. `--only health` calls just the health endpoint: group keys are not compared and a healthy app/env is reported as `UP`. `--only info` calls just the info endpoint: an instance counts as up if info answers 2xx with JSON.

```sh
envtally check -i env=prod --only health   # is prod up? (no version comparison)
```

## How `check` decides

1. The config line and the group-key template of each profile in use go to stderr.
2. All filtered instances are probed in parallel. Info is only requested when health succeeded.
3. Each instance gets a state:

   | Instance state | Meaning |
   |---|---|
   | `UP` | Health passed and the group key was read (status-only profile: every endpoint answered 2xx). |
   | `DOWN` | No response, a non-2xx status, or `healthyWhen` did not match. |
   | `NOINFO` | Healthy, but the group key source failed (info not 2xx, or not JSON). |

4. Each app/env gets a state:

   | App/env state | Meaning | Exit code |
   |---|---|---|
   | `✓ IN SYNC` | Every instance is `UP` and all group keys are identical and complete. | 0 |
   | `✗ DRIFT` | Every instance is `UP`, but there is more than one distinct key. Each key is listed with its instance count. | 1 |
   | `✗ DOWN` | At least one instance is `DOWN` or `NOINFO`. Never "in sync", whatever the others report. | 1 |
   | `? UNKNOWN` | Every instance is `UP`, but a key has a missing field (`?`). Two instances that both report `?\|?` are not known to match. | 1 |
   | `✓ UP` | Every instance is healthy and no keys were compared: `--only health`, or a [status-only](#status-only-profiles) profile. | 0 |

   An app/env with a single instance is in sync by definition; the check then only tells you it is up and which version it runs.

5. Times are the total per instance (health + info), shown as `98ms` or `1.2s`. The summary lists the times of `UP` instances with min/avg/max.

## Output styles

Set the default in `defaults.output`; override with `--format`. Diagnostics always go to stderr, so stdout can be piped.

**table** (default): shown at the top of this page.

**tree**

```
billing
  payments
    dev  ✗ DRIFT  [1.4.1|9f8e ×1] [1.4.2|a1b2 ×1]  [110ms, 95ms] min 95ms · avg 103ms · max 110ms
      payments.dev.na1.host.com  UP  1.4.2|a1b2  110ms
      payments.dev.na2.host.com  UP  1.4.1|9f8e  95ms
    prod  ✓ IN SYNC  [1.4.1|9f8e ×2]  [98ms, 1.2s] min 98ms · avg 649ms · max 1.2s
      payments.na1.host.com  UP  1.4.1|9f8e  98ms
      payments.na2.host.com  UP  1.4.1|9f8e  1.2s (slow)
```

**compact**: one line per app/env.

```
billing/payments/dev ✗ DRIFT [1.4.1|9f8e ×1] [1.4.2|a1b2 ×1]
billing/payments/prod ✓ IN SYNC [1.4.1|9f8e ×2]
billing/ledger/dev ✗ DOWN [ledger.dev.na2.host.com DOWN: HTTP 503] [2.0.0|77aa ×1]
billing/ledger/prod ? UNKNOWN [2.0.0|? ×1]
```

**markdown**: a summary table and an instance table for Jira, Slack or pull requests. `|` inside group keys is escaped.

**csv**: one row per instance. Columns: `project, app, env, url, host, instance_status, group_key, env_status, health_ms, info_ms, total_ms, slow, detail`.

**json**: the full result. Times are raw milliseconds.

```json
{
  "ok": false,
  "groupKeyTemplates": { "springboot": "${build.version}|${git.commit.id}" },
  "envs": [
    {
      "project": "billing", "app": "payments", "env": "dev",
      "status": "DRIFT",
      "keys": [ { "key": "1.4.1|9f8e", "count": 1 }, { "key": "1.4.2|a1b2", "count": 1 } ],
      "minMs": 95, "avgMs": 102, "maxMs": 110,
      "instances": [
        {
          "url": "https://payments.dev.na1.host.com", "host": "payments.dev.na1.host.com",
          "profile": "springboot", "status": "UP", "groupKey": "1.4.2|a1b2", "keyComplete": true,
          "healthMs": 60, "infoMs": 50, "totalMs": 110, "slow": false
        }
      ]
    }
  ]
}
```

```sh
envtally check --format json | jq -r '.envs[] | select(.status != "IN SYNC") | "\(.project)/\(.app)/\(.env) \(.status)"'
```

## Watch mode

```sh
envtally check -i app=payments --watch 30s --watch-timeout 10m
```

- Re-runs the check every interval (Go durations: `1s`, `30s`, `1m`). The default `--watch-timeout` is `15m`.
- Prints a full snapshot on the first check, then one timestamped line per **change** (instance state flips, group-key changes, app/env state changes). A check with no changes prints nothing (use `-v` to see a line for each).
- Stops when every filtered app/env is in sync, rings the terminal bell and prints a summary. Exit code 0.
- On timeout it prints the final state and exits 1. Ctrl-C does the same.
- If everything is already in sync, it prints the snapshot and exits 0 after one check.

```
billing/payments/dev ✗ DRIFT [1.4.1|9f8e ×1] [1.4.2|a1b2 ×1]
billing/payments/prod ✓ IN SYNC [1.4.1|9f8e ×2]

[14:02:10] watching every 30s, timeout 10m0s
[14:06:10] billing/payments/dev payments.dev.na2.host.com: key 1.4.1|9f8e -> 1.4.2|a1b2
[14:06:10] billing/payments/dev: DRIFT -> IN SYNC
✓ in sync after 4m12s (9 checks)
```

With `--format json` or `csv`, progress lines go to stderr and stdout receives exactly one final document, so piping still works.

`--timeout` is always the per-request timeout. The watch limit is `--watch-timeout`.

## Verbose mode

Verbose output goes to stderr.

- `-v`: how many instances the filters kept, then one line per request: URL, HTTP status or error, time, retries used.
- `-vv`: also request and response headers, and response bodies truncated to 4 KB.

Secrets are masked at `-vv`: `Authorization`, `Proxy-Authorization`, `Cookie`, `Set-Cookie` and every header configured in the profile print as `[REDACTED]`. If a server echoes a configured header value in its body, that is masked too.

```
$ envtally check -i app=payments -v
Using config: /home/me/.config/envtally/config.json (from remembered)
Filters kept 4 of 6 instances
Group key [springboot]: ${build.version}|${git.commit.id}
GET https://payments.dev.na1.host.com/actuator/health -> HTTP 200 in 61ms (retries: 0)
GET https://payments.dev.na1.host.com/actuator/info -> HTTP 200 in 49ms (retries: 0)
...
```

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Every filtered app/env is in sync (or up, with `--only health`). |
| `1` | Any drift, down or unknown app/env; or a watch that timed out or was interrupted. |
| `2` | Config or usage error: no config, invalid config, bad flag, invalid regex, filters that match nothing. |

## CI usage

Gate a pipeline on the exit code. Use `ENVTALLY_CONFIG` so nothing is remembered on the build agent.

**GitHub Actions:** wait up to 10 minutes for a rollout to converge, then fail.

```yaml
- name: Wait for prod to converge
  env:
    ENVTALLY_CONFIG: deploy/envtally.json
  run: envtally check --include env=prod --format compact --watch 30s --watch-timeout 10m
```

**Jenkins:**

```groovy
stage('Verify rollout') {
  environment { ENVTALLY_CONFIG = 'deploy/envtally.json' }
  steps {
    sh "envtally check --include 'env=${params.ENV}' --format markdown --watch 30s --watch-timeout 10m"
  }
}
```

To tell drift (1) from a broken setup (2):

```sh
envtally check --format json > result.json; code=$?
if [ "$code" -eq 2 ]; then echo "envtally is misconfigured"; exit 2; fi
```

## Development

Requirements: Go (latest stable). [Task](https://taskfile.dev) is optional: every task is a short `go` command you can run directly.

| Command | What it does |
|---|---|
| `task` | fmt, lint, test, build |
| `task build` | Build `bin/envtally` and `bin/mock-proxy-server` |
| `task test` | Run all tests |
| `task test-update` | Rewrite the golden files (`internal/report`, `internal/cli`) after an intended output change |
| `task lint` | `go vet` |
| `task mock` | Run the mock server on `127.0.0.1:8099` (see [Mock server](#mock-server)) |
| `task mock-check` | Run `envtally check` against the running mock server |
| `task fmt` | gofmt |
| `task cross-build` | Build linux/darwin/windows × amd64/arm64 into `dist/` |
| `task release-build` | Release archives and checksums in `dist/`, without publishing (`sh .github/release-build.sh`) |

Tasks declare `sources`, so Task skips them when nothing changed (`task --force <name>` runs anyway).

Layout: one responsibility per package.

```
cmd/envtally/        entry point
cmd/mock-proxy-server/  mock server binary (development only, not released)
embed.go             embeds the schema and the sample config
internal/cli/        one file per command
internal/config/     load, normalize, validate, resolve instances/links/headers
internal/state/      remembered config path
internal/filter/     include/exclude patterns
internal/tmpl/       ${name} expansion
internal/probe/      HTTP worker pool, retries, timing, verbose log
internal/group/      group keys, instance and app/env states
internal/report/     one file per output style, golden files in testdata/
internal/watch/      watch loop and change detection
internal/helptext/   shared --help layout (examples, closing tips)
internal/mockserver/ mock server: default.json (profiles), one file per built-in app type
examples/            springboot.json, rails.json, flask.json
```

### Mock server

`mock-proxy-server` serves fake apps behind fake host names, so you can see real output without running real services. It runs on its own and does not need envtally: open it in a browser or call it with curl.

```sh
task build
bin/mock-proxy-server                   # default profile on 127.0.0.1:8099
curl -i http://127.0.0.1:8099/flask-catalog.dev.na1.example.com/health
```

`http://127.0.0.1:8099/` lists every instance and URL: a page of links in a browser, JSON for curl.

**Fake hosts.** Every instance has a fake host, `<app>.<env>.<instance>.<domain>`, for example `flask-catalog.dev.na1.example.com`. Give the server a URL that names the host and it answers as that instance; anything else is a 404. Three forms work, and they return the same response:

| Form | Example |
|---|---|
| Host in the path | `http://127.0.0.1:8099/flask-catalog.dev.na1.example.com/health` |
| Full URL in the path | `http://127.0.0.1:8099/https://flask-catalog.dev.na1.example.com/health` |
| Host header (subdomain) | `http://flask-catalog.dev.na1.localhost:8099/health` |

Only the first three labels (`<app>.<env>.<instance>`) are looked up, so any domain after them works: `.example.com`, `.localhost`, `.127.0.0.1`, `.127.0.0.1.nip.io`.

The two path forms work everywhere. The Host-header form needs the name to reach your machine:

- Browsers and recent curl resolve `*.localhost` to 127.0.0.1 themselves.
- `*.127.0.0.1.nip.io` and `*.127.0.0.1.sslip.io` resolve through public DNS.
- Windows itself does not resolve `*.localhost`, and `*.127.0.0.1` is not a real name anywhere. For those, send the header yourself: `curl -H "Host: flask-catalog.dev.na1.127.0.0.1:8099" http://127.0.0.1:8099/health`, or use `curl --resolve`, or add the names to your hosts file.

An app's name starts with its app type (`springboot-payments`, `rails-storefront`, `flask-catalog`), and that prefix decides how it answers. Every app is deployed to every instance of every env.

| App type | Endpoints | Typical answers |
|---|---|---|
| `springboot` | `/actuator/health`, `/actuator/info` | Actuator JSON with components, build and git details; Spring's JSON error body |
| `rails` | `/up`, `/version` | The green/red `/up` page, a JSON version; the `public/404.html` and `500.html` pages |
| `flask` | `/health`, `/info` | Small JSON documents; Werkzeug's HTML error pages |

**Commands and options.**

| Command | Purpose |
|---|---|
| `mock-proxy-server` | Serve a profile. |
| `mock-proxy-server profiles` | List the config's profiles. |
| `mock-proxy-server urls` | List every URL the server answers. |
| `mock-proxy-server config` | Print the built-in mock config, to copy and edit. |
| `mock-proxy-server envtally` | Print an envtally config that checks this server. |

| Option | Meaning |
|---|---|
| `-c`, `--config <file>` | Mock config file. Default: the built-in config. |
| `--profile <name>` | Profile to use. Default: the config's `defaultProfile`. |
| `-p`, `--port <n>` | Port to listen on. Default `8099`. |
| `-b`, `--bind <address>` | Address to listen on. Default `127.0.0.1`. |
| `-q`, `--quiet` | Do not log requests. |

`mock-proxy-server --help` shows examples for each.

**Profiles.** The server reads a JSON config that holds named profiles and serves one of them. The built-in config is `internal/mockserver/default.json`:

| Profile | What it shows |
|---|---|
| `healthy` | Every app up and in sync in every env |
| `drift` | A rollout halfway through prod |
| `outage` | A failing health check, a missing info endpoint, incomplete info |
| `statuses` | Apps answering 2xx, 3xx, 4xx and 5xx |
| `slow` | One targeted instance answering slowly |
| `custom` | A config-defined app type and custom responses |
| `mixed` (default) | A bit of everything |

```sh
bin/mock-proxy-server profiles
bin/mock-proxy-server --profile drift
bin/mock-proxy-server config > my-mock.json    # copy the built-in config, then edit it
bin/mock-proxy-server -c my-mock.json --profile mine
```

**Config.** Top-level `domain` ends the fake hosts (default `example.com`). A profile lists `envs` (or uses the top-level ones) and `apps`. An env's `instances` is how many instances (clusters, regions, nodes) it has: a count (`2` means `na1` and `na2`) or a list of names (`["east", "west"]`). Without it there is one, `na1`.

Settings decide how an instance answers, and the most specific one wins: env, then app, then the app's `overrides` for an env (`"prod"`) or one instance (`"prod/na2"`).

| Setting | Meaning |
|---|---|
| `version`, `revision` | What the info endpoint reports. |
| `state` | `healthy` (default), `down` (health check fails), `noinfo` (info endpoint is a 404) or `partial` (info lacks a group-key field). Each app type answers the way that kind of app does. |
| `status` | Force this HTTP status (200–599) on every endpoint, with the app type's usual body for it. A 3xx redirects to the instance's `/login` page. |
| `delay` | Wait this long before every response, e.g. `"600ms"`. |
| `responses` | Replace single endpoints, keyed by path: `status`, `contentType`, `headers`, `body`, `delay`. |

```json
{
  "defaultProfile": "mine",
  "domain": "corp.example",
  "profiles": {
    "mine": {
      "envs": [
        { "name": "dev", "version": "2.1.0", "revision": "4d5e6f7", "instances": 2 },
        { "name": "prod", "version": "2.0.0", "revision": "1a2b3c4", "instances": ["east", "west", "eu"] }
      ],
      "apps": {
        "springboot-payments": {
          "overrides": {
            "prod/eu": { "delay": "2s" },
            "dev": { "responses": { "/actuator/health": { "status": 503, "body": { "status": "OUT_OF_SERVICE" } } } }
          }
        },
        "rails-storefront": { "overrides": { "prod": { "status": 502 } } },
        "flask-catalog": {}
      }
    }
  }
}
```

That config serves `springboot-payments.prod.eu.corp.example`, `flask-catalog.dev.na2.corp.example` and so on.

A response `body` that is a JSON string is sent as-is (`text/plain` unless `contentType` says otherwise); any other JSON value is sent as JSON. Without a `body`, the app type's usual response is kept and only the status, headers and delay change. `${app}`, `${name}`, `${type}`, `${env}`, `${instance}`, `${host}`, `${version}` and `${revision}` are replaced in the body.

**Your own app type.** Define it under top-level `types`, then name apps with its prefix (`node-gateway`). `profile` is the envtally profile that reads it, `endpoints` are its normal responses, and `states` replaces endpoints while an instance is in that state:

```json
"types": {
  "node": {
    "profile": {
      "healthPath": "/healthz",
      "infoPath": "/meta",
      "healthyWhen": { "field": "ok", "equals": "true" },
      "groupKey": "${version}|${build.sha}"
    },
    "endpoints": {
      "/healthz": { "body": { "ok": true } },
      "/meta": { "body": { "version": "${version}", "build": { "sha": "${revision}" } } }
    },
    "states": {
      "down": { "/healthz": { "status": 503, "body": { "ok": false } } }
    }
  }
}
```

**With envtally.** `mock-proxy-server envtally` prints an envtally config for the chosen profile and port. Its instance URLs use the host-in-path form (`http://127.0.0.1:8099/flask-catalog.dev.na1.example.com`), which needs no DNS setup. The tasks wire it up:

```sh
task mock          # terminal 1: serve on 127.0.0.1:8099
task mock-check    # terminal 2: check every app in every env
task mock-check -- -f tree -i env=prod

task mock MOCK_ARGS="--profile drift"         # another profile: pass the same MOCK_ARGS to both
task mock-check MOCK_ARGS="--profile drift"
```

`task mock-check` writes the config to `bin/mock.json` and passes it with `ENVTALLY_CONFIG`, so your remembered config is left alone. Use another port with `MOCK_ARGS="-p 9000"` on both tasks.

Built-in app types live in `internal/mockserver/`, one file each (`springboot.go`, `rails.go`, `flask.go`). To build one in, add its file and list it in `builtinTypes`.

Tests that use it:

- `internal/mockserver`: every URL form; status, content type, headers and body per app type, state and status class; delays; custom configs; config errors; and that every built-in envtally profile has a mock app type serving its health and info paths.
- `internal/cli/mock_test.go`: runs `check` against every profile and compares with the golden files in `internal/cli/testdata/` (the `mixed` profile in every output format). Ports and response times are normalized first.

**Cross-OS testing notes**

- Tests use `net/http/httptest` only; they never touch a real network.
- Tests use `t.TempDir()` and `path/filepath`, and set `ENVTALLY_HOME` so they never read or write your real state.
- `.gitattributes` forces LF line endings, because golden files are compared byte for byte.
- CI runs `go vet` and the tests on Linux, macOS and Windows, then builds the binaries for Linux, macOS and Windows (amd64 and arm64) and attaches them to the run as the `envtally-binaries` artifact.
- Pushing a tag such as `v1.2.3` publishes those archives, with `checksums.txt`, as a GitHub release.
- CI uses only Go and the official `checkout`, `setup-go` and `upload-artifact` actions.
- The binary is pure Go (`CGO_ENABLED=0`).

## Contributing

Issues and pull requests are welcome at [github.com/LarryBattle/envtally](https://github.com/LarryBattle/envtally). See [CONTRIBUTING.md](CONTRIBUTING.md) for how to build, test and submit a change.

## License

[MIT](LICENSE) © 2026 Larry Battle
