package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/ivysterling59-cyber/compatprobe/internal/config"
	"github.com/ivysterling59-cyber/compatprobe/internal/core"
	"github.com/ivysterling59-cyber/compatprobe/internal/diagnosis"
	"github.com/ivysterling59-cyber/compatprobe/internal/probe"
	"github.com/ivysterling59-cyber/compatprobe/internal/redact"
	"github.com/ivysterling59-cyber/compatprobe/internal/report"
)

type App struct {
	Version        string
	Stdout, Stderr io.Writer
	Context        context.Context
}

func (a App) Run(args []string) int {
	if a.Stdout == nil {
		a.Stdout = os.Stdout
	}
	if a.Stderr == nil {
		a.Stderr = os.Stderr
	}
	if len(args) == 0 {
		a.help()
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(a.Stdout, "compatprobe %s\n", a.Version)
		return 0
	case "help", "-h", "--help":
		a.help()
		return 0
	case "check":
		return a.check(args[1:])
	default:
		fmt.Fprintf(a.Stderr, "error: unknown command %q\n", redact.String(args[0]))
		a.help()
		return 2
	}
}

func (a App) check(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var target config.Target
	var jsonOut, verbose bool
	var output, headers string
	fs.StringVar(&target.BaseURL, "base-url", "", "OpenAI-compatible base URL")
	fs.StringVar(&target.APIKey, "api-key", "", "API key (prefer COMPATPROBE_API_KEY)")
	fs.StringVar(&target.Model, "model", "", "model name")
	fs.DurationVar(&target.Timeout, "timeout", 60*time.Second, "overall request timeout")
	fs.IntVar(&target.StreamTokens, "stream-tokens", config.DefaultStreamTokens, fmt.Sprintf("Chat Completions max_completion_tokens budget (%d-%d; not a visible-token count or duration)", config.MinStreamTokens, config.MaxStreamTokens))
	fs.BoolVar(&jsonOut, "json", false, "write JSON to stdout")
	fs.StringVar(&output, "output", "", "write Markdown report to file")
	fs.BoolVar(&verbose, "verbose", false, "include verbose progress on stderr")
	fs.StringVar(&headers, "header", "", "custom headers as comma-separated Name:Value pairs")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(a.Stderr, "error: %s\n", redact.Secrets(err.Error(), target.APIKey))
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Stderr, "error: unexpected positional arguments")
		return 2
	}
	if err := config.ValidateStreamTokens(target.StreamTokens); err != nil {
		fmt.Fprintf(a.Stderr, "error: %s\n", err)
		return 2
	}
	if err := parseHeaders(headers, &target); err != nil {
		fmt.Fprintln(a.Stderr, "error: invalid --header value")
		return 2
	}
	if err := target.Normalize(); err != nil {
		fmt.Fprintf(a.Stderr, "error: %s\n", err)
		return 2
	}
	if verbose {
		fmt.Fprintln(a.Stderr, "Running compatibility probes...")
	}
	parent := a.Context
	if parent == nil {
		parent = context.Background()
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt)
	defer stop()
	run := probe.New(target).Run(ctx)
	diags := diagnosis.Evaluate(run.Probes)
	secrets := []string{target.APIKey}
	for name, values := range target.Headers {
		for _, value := range values {
			if redact.Header(name, value) == redact.Replacement {
				secrets = append(secrets, value)
			}
		}
	}
	rep := report.New(a.Version, target.BaseURL, target.Model, run.Timeline, run.Probes, diags, secrets...)
	rep.CheckTimeout = target.Timeout
	if output != "" {
		if err := os.WriteFile(output, report.Markdown(rep), 0600); err != nil {
			fmt.Fprintf(a.Stderr, "error: could not write report: %s\n", redact.Secrets(err.Error(), secrets...))
			return 2
		}
	}
	if jsonOut {
		b, err := report.JSON(rep)
		if err != nil {
			fmt.Fprintf(a.Stderr, "error: could not encode report: %s\n", redact.String(err.Error()))
			return 2
		}
		a.Stdout.Write(b)
	} else {
		a.Stdout.Write(report.Terminal(rep))
	}
	return exitCode(run.Probes)
}

func parseHeaders(s string, t *config.Target) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	t.Headers = make(http.Header)
	for _, part := range strings.Split(s, ",") {
		name, value, ok := strings.Cut(part, ":")
		if !ok || strings.TrimSpace(name) == "" {
			return fmt.Errorf("invalid header")
		}
		t.Headers.Add(strings.TrimSpace(name), strings.TrimSpace(value))
	}
	return nil
}
func exitCode(ps []core.ProbeResult) int {
	meaningful := false
	failed := false
	for _, p := range ps {
		if p.Status == core.Skip {
			continue
		}
		if p.Error == nil || !probe.IsNetworkKind(p.Error.Kind) {
			meaningful = true
		}
		if p.Status == core.Pass || p.Status == core.Warn {
			meaningful = true
		}
		if p.Status == core.Fail {
			failed = true
		}
	}
	if !meaningful {
		return 3
	}
	if failed {
		return 1
	}
	return 0
}
func (a App) help() {
	fmt.Fprintf(a.Stdout, "CompatProbe\n\nUsage:\n  compatprobe check --base-url URL --model MODEL [options]\n  compatprobe version\n  compatprobe help\n\n--stream-tokens N maps to Chat Completions max_completion_tokens, a completion-token budget (%d-%d, default %d); it does not guarantee a visible-token count or stream duration.\nAPI keys may be supplied with COMPATPROBE_API_KEY (recommended).\n", config.MinStreamTokens, config.MaxStreamTokens, config.DefaultStreamTokens)
}
