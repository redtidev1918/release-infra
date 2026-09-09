package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	rgdomain "github.com/redtidev1918/release-infra/internal/domain"
	"github.com/redtidev1918/release-infra/internal/graph"
	rgplan "github.com/redtidev1918/release-infra/internal/plan"
	"github.com/redtidev1918/release-infra/internal/policy"
)

const version = "0.1.0-go-readonly"

type Envelope struct {
	SchemaVersion       int        `json:"schemaVersion"`
	ReleaseGraphVersion string     `json:"releasegraphVersion"`
	GeneratedAt         string     `json:"generatedAt"`
	Data                any        `json:"data,omitempty"`
	Error               *ErrorBody `json:"error,omitempty"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	var err error
	switch args[0] {
	case "doctor":
		err = doctor(stdout, args[1:])
	case "graph":
		err = graphCommand(stdout, args[1:])
	case "inspect":
		err = inspect(stdout, args[1:])
	case "plan":
		err = planCommand(stdout, args[1:])
	case "version":
		fmt.Fprintln(stdout, version)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "ERROR", err)
		return 1
	}
	return 0
}

func flags(outputFormat *string) *flag.FlagSet {
	fs := flag.NewFlagSet("", flag.ContinueOnError)
	fs.StringVar(outputFormat, "output", "human", "human, json, or mermaid")
	fs.StringVar(outputFormat, "format", "human", "alias for --output")
	return fs
}

func doctor(w io.Writer, args []string) error {
	var format string
	fs := flags(&format)
	if err := fs.Parse(args); err != nil {
		return err
	}
	info := map[string]any{"status": "ok", "mode": "read-only", "serverRequired": false, "databaseRequired": false, "commands": []string{"doctor", "graph", "inspect", "plan", "version"}}
	return write(w, format, info, humanDoctor)
}

func graphCommand(w io.Writer, args []string) error {
	var format, path string
	fs := flags(&format)
	fs.StringVar(&path, "file", "release-graph.yml", "release graph path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	g, err := graph.Load(path)
	if err != nil {
		return err
	}
	view, err := graph.BuildView(g)
	if err != nil {
		return err
	}
	if format == "mermaid" {
		text, err := graph.Mermaid(g)
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(w, text)
		return err
	}
	return write(w, format, view, func(w io.Writer, _ any) {
		fmt.Fprintln(w, "READY")
		for _, id := range view.Ready {
			fmt.Fprintln(w, "  "+id)
		}
		if len(view.Blocked) > 0 {
			fmt.Fprintln(w, "BLOCKED")
			ids := make([]string, 0)
			for id := range view.Blocked {
				ids = append(ids, id)
			}
			for _, id := range ids {
				fmt.Fprintf(w, "  %s: %v\n", id, view.Blocked[id])
			}
		}
		fmt.Fprintln(w, "ORDER", view.Order)
	})
}

func inspect(w io.Writer, args []string) error {
	var format, path string
	fs := flags(&format)
	fs.StringVar(&path, "path", ".release-policy.yml", "policy path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := policy.Load(path)
	if err != nil {
		return err
	}
	return write(w, format, p, func(w io.Writer, _ any) {
		fmt.Fprintf(w, "kind=%s versioning=%s requiredAssets=%d registries=%d policyHash=%s\n", p.Kind, effectiveMode(p), len(p.Assets.Required), len(p.Registries), p.Hash)
	})
}

func planCommand(w io.Writer, args []string) error {
	var format, policyPath, version, root, graphPath, statePath string
	fs := flags(&format)
	fs.StringVar(&policyPath, "path", ".release-policy.yml", "policy path")
	fs.StringVar(&version, "version", "", "explicit version")
	fs.StringVar(&root, "root", ".", "repository root")
	fs.StringVar(&graphPath, "graph", "", "release graph path")
	fs.StringVar(&statePath, "state", "", "actual project health JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if graphPath != "" {
		g, err := graph.Load(graphPath)
		if err != nil {
			return err
		}
		health, err := rgplan.LoadHealth(statePath)
		if err != nil {
			return err
		}
		out, err := rgplan.Evaluate(g, health)
		if err != nil {
			return err
		}
		return write(w, format, out, func(w io.Writer, _ any) {
			fmt.Fprintln(w, "READY")
			for _, id := range out.Ready {
				fmt.Fprintln(w, "  "+id)
			}
			if len(out.Blocked) > 0 {
				fmt.Fprintln(w, "BLOCKED")
				ids := make([]string, 0)
				for id := range out.Blocked {
					ids = append(ids, id)
				}
				sort.Strings(ids)
				for _, id := range ids {
					fmt.Fprintf(w, "  %s: %v\n", id, out.Blocked[id])
				}
			}
			if len(out.Noop) > 0 {
				fmt.Fprintln(w, "NOOP")
				for _, id := range out.Noop {
					fmt.Fprintln(w, "  "+id)
				}
			}
		})
	}
	p, err := policy.Load(policyPath)
	if err != nil {
		return err
	}
	desired, err := policy.DesiredVersion(p, version, root)
	if err != nil {
		return err
	}
	node := rgdomain.NodePlan{ID: "local", Kind: rgdomain.NodeKindRelease, Health: rgdomain.HealthReady, Desired: &rgdomain.DesiredState{Version: rgdomain.Version(desired)}}
	out := rgdomain.Plan{Ready: []string{"local"}, Blocked: []string{}, Noop: []string{}, Nodes: []rgdomain.NodePlan{node}}
	return write(w, format, out, func(w io.Writer, _ any) {
		fmt.Fprintf(w, "READY\n  local desired=%s tag=%s requiredAssets=%d\n", desired, policy.Tag(p, desired), len(p.Assets.Required))
	})
}

func write(w io.Writer, format string, data any, human func(io.Writer, any)) error {
	if format == "json" || format == "" {
		return json.NewEncoder(w).Encode(Envelope{SchemaVersion: 1, ReleaseGraphVersion: version, GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano), Data: data})
	}
	if format == "human" {
		human(w, data)
		return nil
	}
	return fmt.Errorf("unsupported output format %q", format)
}
func humanDoctor(w io.Writer, _ any) { fmt.Fprintln(w, "ReleaseGraph read-only core: ok") }
func effectiveMode(p *policy.Policy) string {
	if p.Versioning.Mode != "" {
		return p.Versioning.Mode
	}
	return p.Versioning.Provider
}
func usage(w io.Writer) { fmt.Fprintln(w, "usage: releasegraph [doctor|graph|inspect|plan|version]") }
func Main()             { os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr)) }
