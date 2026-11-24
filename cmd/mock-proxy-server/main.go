// Command mock-proxy-server serves fake Flask, Rails and Spring Boot apps
// (and any app type you define) behind fake host names, for trying envtally
// without real services. It runs on its own: open it in a browser or call it
// with curl. It is a development tool and is not part of a release.
package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/LarryBattle/envtally/internal/helptext"
	"github.com/LarryBattle/envtally/internal/mockserver"
)

func main() {
	root := rootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\nRun `mock-proxy-server --help` for usage.\n", err)
		os.Exit(1)
	}
}

// options are the flags shared by every command.
type options struct {
	configPath string
	profile    string
	bind       string
	port       int
	quiet      bool
}

func (o *options) baseURL() string {
	return "http://" + net.JoinHostPort(o.bind, strconv.Itoa(o.port))
}

// config loads --config, or the built-in config.
func (o *options) config() (*mockserver.Config, error) {
	if o.configPath == "" {
		return mockserver.Default(), nil
	}
	return mockserver.Load(o.configPath)
}

// server builds the mock server for --profile.
func (o *options) server() (*mockserver.Server, error) {
	cfg, err := o.config()
	if err != nil {
		return nil, err
	}
	if o.profile == "" && cfg.DefaultProfile == "" {
		return nil, errors.New("the config has no defaultProfile; pass --profile (see `mock-proxy-server profiles`)")
	}
	return mockserver.New(cfg, o.profile)
}

func rootCmd() *cobra.Command {
	o := &options{}
	root := &cobra.Command{
		Use:   "mock-proxy-server",
		Short: "Serve fake apps behind fake host names",
		Long: "mock-proxy-server serves fake Flask, Rails and Spring Boot apps from a config.\n" +
			"Each instance has a fake host, <app>.<env>.<instance>.<domain>. Put that host\n" +
			"in the URL and the server answers as that instance; anything else is a 404.",
		Example: helptext.Block(`
			mock-proxy-server                       # serve the default profile on 127.0.0.1:8099
			mock-proxy-server -p 9000 --profile drift
			mock-proxy-server -c my-mock.json`),
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(*cobra.Command, []string) error { return serve(o) },
	}
	flags := root.PersistentFlags()
	flags.StringVarP(&o.configPath, "config", "c", "", "mock config file (default: the built-in config)")
	flags.StringVar(&o.profile, "profile", "", "profile to use (default: the config's defaultProfile)")
	flags.IntVarP(&o.port, "port", "p", 8099, "port to listen on")
	flags.StringVarP(&o.bind, "bind", "b", "127.0.0.1", "address to listen on")
	root.Flags().BoolVarP(&o.quiet, "quiet", "q", false, "do not log requests")

	root.AddCommand(
		&cobra.Command{
			Use:   "profiles",
			Short: "List the config's profiles",
			Example: helptext.Block(`
				mock-proxy-server profiles
				mock-proxy-server profiles -c my-mock.json`),
			Args: cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				cfg, err := o.config()
				if err != nil {
					return err
				}
				for _, name := range cfg.ProfileNames() {
					mark := " "
					if name == cfg.DefaultProfile {
						mark = "*"
					}
					fmt.Printf("%s %-10s %s\n", mark, name, cfg.Profiles[name].Description)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "urls",
			Short: "List every URL the server answers",
			Example: helptext.Block(`
				mock-proxy-server urls
				mock-proxy-server urls --profile outage -p 9000`),
			Args: cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				server, err := o.server()
				if err != nil {
					return err
				}
				for _, u := range server.URLs(o.baseURL()) {
					fmt.Println(u)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "config",
			Short: "Print the built-in mock config, to copy and edit",
			Example: helptext.Block(`
				mock-proxy-server config > my-mock.json
				mock-proxy-server -c my-mock.json`),
			Args: cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				_, err := os.Stdout.Write(mockserver.DefaultJSON())
				return err
			},
		},
		&cobra.Command{
			Use:   "envtally",
			Short: "Print an envtally config that checks this server",
			Example: helptext.Block(`
				mock-proxy-server envtally > mock-envtally.json
				envtally check -c mock-envtally.json
				mock-proxy-server envtally --profile drift -p 9000 > mock-envtally.json`),
			Args: cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				server, err := o.server()
				if err != nil {
					return err
				}
				_, err = os.Stdout.Write(server.EnvtallyConfig(o.baseURL()))
				return err
			},
		},
	)
	helptext.Apply(root)
	helptext.Tips(root, "Try it (with the server running):", `
		curl -i http://127.0.0.1:8099/flask-catalog.dev.na1.example.com/health
		curl -i http://127.0.0.1:8099/https://flask-catalog.dev.na1.example.com/health
		curl -i -H "Host: flask-catalog.dev.na1.example.com" http://127.0.0.1:8099/health
		curl http://127.0.0.1:8099/              # every instance and URL (a page of links in a browser)
		mock-proxy-server profiles               # what --profile accepts
		mock-proxy-server config > my-mock.json  # start your own config`)
	return root
}

func serve(o *options) error {
	server, err := o.server()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(o.bind, strconv.Itoa(o.port)))
	if err != nil {
		return err
	}
	base := o.baseURL()
	fmt.Fprintf(os.Stderr, "mock-proxy-server listening on %s\n", base)
	fmt.Fprintf(os.Stderr, "Profile %s: %s\n", server.Profile, server.Description)
	fmt.Fprintf(os.Stderr, "Fake hosts: <app>.<env>.<instance>.%s\n", server.Domain)
	if instances := server.Instances(); len(instances) > 0 {
		inst := instances[0]
		if endpoints := server.Endpoints(inst); len(endpoints) > 0 {
			fmt.Fprintf(os.Stderr, "Try: curl -i %s/%s%s\n", base, inst.Host(), endpoints[0])
			fmt.Fprintf(os.Stderr, "     curl -i -H \"Host: %s\" %s%s\n", inst.Host(), base, endpoints[0])
		}
	}
	fmt.Fprintf(os.Stderr, "Every URL: open %s/ in a browser\n", base)

	var handler http.Handler = server
	if !o.quiet {
		handler = logRequests(handler)
	}
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	return httpServer.Serve(listener)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		fmt.Fprintf(os.Stderr, "%s %s %s%s -> %d (%s)\n", start.Format("15:04:05"), r.Method, r.Host, r.URL.Path, rec.status,
			time.Since(start).Round(time.Millisecond))
	})
}
