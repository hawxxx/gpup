package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"gpup/internal/analysis"
	"gpup/internal/api"
	"gpup/internal/app"
	"gpup/internal/bench"
	"gpup/internal/engine"
	"gpup/web"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cmd := newRoot()
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type cliOptions struct {
	db, dcgm string
	json     bool
}

func newRoot() *cobra.Command {
	home, err := os.UserConfigDir()
	if err != nil {
		home = "."
	}
	opt := &cliOptions{db: filepath.Join(home, "gpup", "gpup.db")}
	root := &cobra.Command{Use: "gpup", Short: "Inference performance, connected to your GPUs", Version: app.Version, SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringVar(&opt.db, "db", opt.db, "SQLite database path")
	root.PersistentFlags().StringVar(&opt.dcgm, "dcgm-url", os.Getenv("GPUP_DCGM_URL"), "Optional DCGM exporter metrics URL")
	root.PersistentFlags().BoolVar(&opt.json, "json", false, "Print JSON")
	serve := serveCommand(opt)
	root.AddCommand(serve)
	ui := serveCommand(opt)
	ui.Use = "ui"
	ui.Short = "Start the embedded web UI"
	root.AddCommand(ui)
	root.RunE = serve.RunE
	// Default invocation shares the serve options and starts the local UI.
	root.Flags().AddFlagSet(serve.Flags())
	target := &cobra.Command{Use: "target", Short: "Manage inference targets"}
	var endpoint, engineName, model, metrics, keyEnv string
	add := &cobra.Command{Use: "add NAME", Args: cobra.ExactArgs(1), Short: "Register an OpenAI-compatible endpoint", RunE: func(cmd *cobra.Command, args []string) error {
		return withApp(opt, func(a *app.App) error {
			t, err := a.AddTarget(cmd.Context(), engine.Target{Name: args[0], Engine: engineName, URL: endpoint, Model: model, MetricsURL: metrics, APIKeyEnv: keyEnv})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), t)
		})
	}}
	add.Flags().StringVar(&endpoint, "url", "", "Endpoint base URL")
	_ = add.MarkFlagRequired("url")
	add.Flags().StringVar(&engineName, "engine", "openai", "Engine: openai, vllm, sglang, tensorrt-llm, triton, llama.cpp, ollama, tgi")
	add.Flags().StringVar(&model, "model", "", "Default model")
	add.Flags().StringVar(&metrics, "metrics-url", "", "Engine exporter URL")
	add.Flags().StringVar(&keyEnv, "api-key-env", "", "Environment variable containing API key (never saved)")
	target.AddCommand(add, &cobra.Command{Use: "list", Short: "List targets", RunE: func(cmd *cobra.Command, args []string) error {
		return withApp(opt, func(a *app.App) error { return printJSON(cmd.OutOrStdout(), a.Snapshot().Targets) })
	}}, &cobra.Command{Use: "remove NAME_OR_ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return withApp(opt, func(a *app.App) error {
			t, err := a.Target(args[0])
			if err != nil {
				return err
			}
			return a.DeleteTarget(t.ID)
		})
	}})
	root.AddCommand(target)
	for _, name := range []string{"status", "models", "requests", "gpu", "doctor"} {
		name := name
		watch := false
		c := &cobra.Command{Use: name, Short: "Inspect " + name, RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(opt, func(a *app.App) error {
				if watch {
					a.Start()
					return runTop(cmd, a, "gpu")
				}
				a.Poll(cmd.Context())
				s := a.Snapshot()
				if opt.json {
					switch name {
					case "gpu":
						return printJSON(cmd.OutOrStdout(), s.GPUs)
					case "models":
						return printJSON(cmd.OutOrStdout(), s.Models)
					case "requests":
						return printJSON(cmd.OutOrStdout(), s.Requests)
					default:
						return printJSON(cmd.OutOrStdout(), s)
					}
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), render(s, name))
				return err
			})
		}}
		if name == "gpu" {
			c.Flags().BoolVar(&watch, "watch", false, "Interactive live GPU dashboard")
		}
		root.AddCommand(c)
	}
	root.AddCommand(&cobra.Command{Use: "top", Short: "Interactive inference/GPU dashboard", RunE: func(cmd *cobra.Command, args []string) error {
		return withApp(opt, func(a *app.App) error { a.Start(); return runTop(cmd, a, "status") })
	}})
	root.AddCommand(&cobra.Command{Use: "discover", Short: "Discover and register local compatible endpoints", RunE: func(cmd *cobra.Command, args []string) error {
		return withApp(opt, func(a *app.App) error {
			targets, err := a.Discover(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), targets)
		})
	}})
	root.AddCommand(benchCommand(opt, "bench"), benchCommand(opt, "sweep"))
	var drop, increase float64
	compare := &cobra.Command{Use: "compare BASELINE CANDIDATE", Args: cobra.ExactArgs(2), Short: "Compare saved compatible runs", RunE: func(cmd *cobra.Command, args []string) error {
		return withApp(opt, func(a *app.App) error {
			b, err := a.Run(args[0])
			if err != nil {
				return err
			}
			c, err := a.Run(args[1])
			if err != nil {
				return err
			}
			diff, err := analysis.Compare(b, c)
			if err != nil {
				return err
			}
			if err = printJSON(cmd.OutOrStdout(), diff); err != nil {
				return err
			}
			return analysis.CheckThresholds(diff, drop, increase)
		})
	}}
	compare.Flags().Float64Var(&drop, "max-throughput-drop", 5, "Allowed throughput decrease percent")
	compare.Flags().Float64Var(&increase, "max-latency-increase", 10, "Allowed E2E p95 latency increase percent")
	root.AddCommand(compare)
	var duration time.Duration
	profile := &cobra.Command{Use: "profile [TARGET]", Args: cobra.MaximumNArgs(1), Short: "Capture passive telemetry (no heavy profiler)", RunE: func(cmd *cobra.Command, args []string) error {
		if duration <= 0 || duration > time.Hour {
			return errors.New("duration must be positive and at most 1h")
		}
		return withApp(opt, func(a *app.App) error {
			if len(args) > 0 {
				if _, err := a.Target(args[0]); err != nil {
					return err
				}
			}
			a.Start()
			timer := time.NewTimer(duration)
			defer timer.Stop()
			select {
			case <-cmd.Context().Done():
				return cmd.Context().Err()
			case <-timer.C:
				return printJSON(cmd.OutOrStdout(), a.Snapshot())
			}
		})
	}}
	profile.Flags().DurationVar(&duration, "duration", 30*time.Second, "Capture window")
	root.AddCommand(profile)
	return root
}
func withApp(opt *cliOptions, fn func(*app.App) error) error {
	a, err := app.New(opt.db, opt.dcgm)
	if err != nil {
		return err
	}
	defer a.Close()
	return fn(a)
}
func serveCommand(opt *cliOptions) *cobra.Command {
	var listen, token, cert, key string
	var readOnly bool
	cmd := &cobra.Command{Use: "serve", Short: "Run API and embedded web UI", RunE: func(cmd *cobra.Command, args []string) error {
		if err := api.ValidateBind(listen, token); err != nil {
			return err
		}
		if (cert == "") != (key == "") {
			return errors.New("both --tls-cert and --tls-key required")
		}
		return withApp(opt, func(a *app.App) error {
			if len(a.Snapshot().Targets) == 0 && !readOnly {
				_, _ = a.Discover(cmd.Context())
			}
			a.Start()
			server := &http.Server{Addr: listen, Handler: api.Handler(a, web.FS(), api.Options{Token: token, ReadOnly: readOnly}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
			done := make(chan error, 1)
			go func() {
				if cert != "" {
					done <- server.ListenAndServeTLS(cert, key)
				} else {
					done <- server.ListenAndServe()
				}
			}()
			scheme := "http"
			if cert != "" {
				scheme = "https"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "GPUP %s · %s://%s\nSQLite: %s\n", app.Version, scheme, listen, opt.db)
			select {
			case <-cmd.Context().Done():
				a.Stop()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				err := server.Shutdown(ctx)
				if err != nil {
					_ = server.Close()
				}
				return err
			case err := <-done:
				if errors.Is(err, http.ErrServerClosed) {
					return nil
				}
				return err
			}
		})
	}}
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:7331", "Bind address (public binds require token)")
	cmd.Flags().StringVar(&token, "token", os.Getenv("GPUP_API_TOKEN"), "API token; prefer GPUP_API_TOKEN")
	cmd.Flags().BoolVar(&readOnly, "read-only", false, "Disable API mutations")
	cmd.Flags().StringVar(&cert, "tls-cert", "", "TLS certificate file")
	cmd.Flags().StringVar(&key, "tls-key", "", "TLS private key file")
	return cmd
}
func benchCommand(opt *cliOptions, name string) *cobra.Command {
	var model, concurrency, prompt, save, baseline string
	var duration time.Duration
	var maxTokens int
	var drop, increase float64
	var gates []string
	cmd := &cobra.Command{Use: name + " TARGET_OR_URL", Args: cobra.ExactArgs(1), Short: "Benchmark a closed-loop concurrency sweep", RunE: func(cmd *cobra.Command, args []string) error {
		gateDrop, gateTTFT, gateErr := parseGateRules(gates)
		if gateErr != nil {
			return gateErr
		}
		if len(gates) > 0 && baseline == "" {
			return errors.New("--fail-if requires --compare")
		}
		if gateDrop != nil {
			drop = *gateDrop
		}
		points := []int{}
		for _, raw := range strings.Split(concurrency, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil {
				return fmt.Errorf("invalid concurrency %q", raw)
			}
			points = append(points, n)
		}
		config := bench.Config{Model: model, Prompt: prompt, Concurrency: points, DurationSeconds: duration.Seconds(), MaxTokens: maxTokens, Name: save}
		// Validate workload bounds before issuing traffic or registering an endpoint.
		if model != "" {
			if err := config.Validate(); err != nil {
				return err
			}
		} else {
			probe := config
			probe.Model = "discover"
			if err := probe.Validate(); err != nil {
				return err
			}
		}
		return withApp(opt, func(a *app.App) error {
			t, err := a.Target(args[0])
			if err != nil {
				if !strings.HasPrefix(args[0], "http://") && !strings.HasPrefix(args[0], "https://") {
					return errors.New("target not found; pass a configured name or HTTP(S) URL")
				}
				t, err = a.AddTarget(cmd.Context(), engine.Target{Name: app.ID("cli"), Engine: "openai", URL: args[0]})
				if err != nil {
					return err
				}
			}
			config.TargetID = t.ID
			if config.Model == "" {
				config.Model = t.Model
				if config.Model == "" {
					c, err := engine.New(t)
					if err != nil {
						return err
					}
					models, err := c.Discover(cmd.Context())
					if err != nil {
						return err
					}
					if len(models) != 1 {
						return errors.New("specify --model when endpoint has zero or multiple models")
					}
					config.Model = models[0].ID
				}
			}
			if baseline != "" {
				if _, err = a.Run(baseline); err != nil {
					return fmt.Errorf("baseline: %w", err)
				}
			}
			a.Start()
			id, err := a.StartBenchmark(cmd.Context(), config)
			if err != nil {
				return err
			}
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-cmd.Context().Done():
					_ = a.CancelJob(id)
					return cmd.Context().Err()
				case <-ticker.C:
					j, err := a.Job(id)
					if err != nil {
						return err
					}
					if j.Status == "running" {
						continue
					}
					if j.Run == nil {
						return errors.New("benchmark has no result")
					}
					if opt.json {
						err = printJSON(cmd.OutOrStdout(), j.Run)
					} else {
						printRun(cmd.OutOrStdout(), *j.Run)
					}
					if err != nil {
						return err
					}
					if j.Error != "" {
						return errors.New(j.Error)
					}
					for _, p := range j.Run.Points {
						if p.Requests == 0 || p.Errors > 0 {
							return errors.New("benchmark contains errors or no completed requests")
						}
					}
					if baseline != "" {
						b, _ := a.Run(baseline)
						diff, err := analysis.Compare(b, *j.Run)
						if err != nil {
							return err
						}
						_ = printJSON(cmd.OutOrStdout(), diff)
						if err := analysis.CheckThresholds(diff, drop, increase); err != nil {
							return err
						}
						if gateTTFT != nil {
							return analysis.CheckTTFTP99Threshold(diff, *gateTTFT)
						}
						return nil
					}
					return nil
				}
			}
		})
	}}
	cmd.Flags().StringVar(&model, "model", "", "Model ID (discover only if unambiguous)")
	cmd.Flags().StringVar(&concurrency, "concurrency", "1,2,4,8", "Comma-separated concurrency points, 1..256")
	cmd.Flags().DurationVar(&duration, "duration", 10*time.Second, "Duration per point, max 1h")
	cmd.Flags().IntVar(&maxTokens, "max-tokens", 128, "Maximum generated tokens")
	cmd.Flags().StringVar(&prompt, "prompt", "Explain how GPUs accelerate inference.", "Fixed prompt (not persisted)")
	cmd.Flags().StringVar(&save, "save", "", "Saved run name")
	cmd.Flags().StringVar(&baseline, "compare", "", "Baseline run ID/name for CI comparison")
	cmd.Flags().Float64Var(&drop, "max-throughput-drop", 5, "Maximum throughput drop percent")
	cmd.Flags().Float64Var(&increase, "max-latency-increase", 10, "Maximum E2E p95 latency increase percent")
	cmd.Flags().StringArrayVar(&gates, "fail-if", nil, "CI rule: throughput<-5% or ttft-p99>+10% (requires --compare)")
	return cmd
}

func parseGateRules(rules []string) (drop, ttft *float64, err error) {
	for _, rule := range rules {
		var raw string
		var destination **float64
		if strings.HasPrefix(rule, "throughput<-") && strings.HasSuffix(rule, "%") {
			raw = strings.TrimSuffix(strings.TrimPrefix(rule, "throughput<-"), "%")
			destination = &drop
		} else if strings.HasPrefix(rule, "ttft-p99>+") && strings.HasSuffix(rule, "%") {
			raw = strings.TrimSuffix(strings.TrimPrefix(rule, "ttft-p99>+"), "%")
			destination = &ttft
		} else {
			return nil, nil, fmt.Errorf("unsupported CI rule %q; use throughput<-5%% or ttft-p99>+10%%", rule)
		}
		value, e := strconv.ParseFloat(raw, 64)
		if e != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return nil, nil, fmt.Errorf("invalid CI rule %q", rule)
		}
		*destination = &value
	}
	return
}
func printJSON(w io.Writer, v any) error {
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e.Encode(v)
}
func value(v *float64, suffix string) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.1f%s", *v, suffix)
}
func printRun(w io.Writer, r bench.Run) {
	fmt.Fprintf(w, "Run %s %s · %s\n", r.ID, r.Name, r.Status)
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "CONC\tREQ/S\tTOK/S\tTTFT P50\tTTFT P99\tE2E P95\tERRORS")
	for _, p := range r.Points {
		p50, p99 := "—", "—"
		if p.TTFT != nil {
			p50 = fmt.Sprintf("%.1fms", p.TTFT.P50)
			p99 = fmt.Sprintf("%.1fms", p.TTFT.P99)
		}
		fmt.Fprintf(table, "%d\t%.2f\t%s\t%s\t%s\t%.1fms\t%d\n", p.Concurrency, p.RPS, value(p.OutputTokensPerSecond, ""), p50, p99, p.E2E.P95, p.Errors)
	}
	_ = table.Flush()
	if r.Knee != nil {
		fmt.Fprintf(w, "Observed concurrency knee: %d\n", *r.Knee)
	}
}
func render(s app.Snapshot, page string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "GPUP %s · %s · LIVE\n\n", app.Version, s.Time.Format("15:04:05"))
	table := tabwriter.NewWriter(&out, 0, 4, 2, ' ', 0)
	switch page {
	case "gpu":
		fmt.Fprintln(table, "GPU\tNAME\tUTIL\tVRAM MiB\tTEMP\tPOWER\tSOURCE")
		for _, g := range s.GPUs {
			fmt.Fprintf(table, "%d\t%s\t%s\t%s / %s\t%s\t%s\t%s\n", g.Index, g.Name, value(g.Utilization, "%"), value(g.MemoryUsedMB, ""), value(g.MemoryTotalMB, ""), value(g.TemperatureC, "°C"), value(g.PowerW, "W"), g.Source)
		}
		if len(s.GPUs) == 0 {
			fmt.Fprintln(table, "No GPU measurements available.")
		}
	case "models":
		fmt.Fprintln(table, "MODEL\tENGINE\tTARGET")
		for _, m := range s.Models {
			fmt.Fprintf(table, "%s\t%s\t%s\n", m.ID, m.Engine, m.TargetID)
		}
	case "requests":
		fmt.Fprintln(table, "REQUEST\tMODEL\tSTATUS\tTTFT\tTPOT\tE2E")
		start := len(s.Requests) - 25
		if start < 0 {
			start = 0
		}
		for _, r := range s.Requests[start:] {
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%.1fms\n", r.ID, r.Model, r.Status, value(r.TTFTMs, "ms"), value(r.TPOTMs, "ms"), r.DurationMs)
		}
	case "benchmarks":
		_ = table.Flush()
		for _, r := range s.Runs {
			printRun(&out, r)
		}
	default:
		fmt.Fprintln(table, "TARGET\tENGINE\tMODEL\tSTATUS\tENDPOINT")
		for _, t := range s.Targets {
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", t.Name, t.Engine, t.Model, t.Status, t.URL)
		}
		if len(s.Targets) == 0 {
			fmt.Fprintln(table, "No targets. Run gpup discover or gpup target add.")
		}
	}
	_ = table.Flush()
	if page == "doctor" || page == "status" {
		fmt.Fprintf(&out, "\nGPUs: %d · Models: %d · Saved runs: %d\nRequest scope: GPUP benchmark traffic only\n", len(s.GPUs), len(s.Models), len(s.Runs))
	}
	if len(s.Findings) > 0 {
		out.WriteString("\nFindings\n")
		for _, f := range s.Findings {
			fmt.Fprintf(&out, "  %s: %s\n  %s\n", f.Kind, f.Summary, f.Recommendation)
		}
	}
	if len(s.CollectorErrors) > 0 {
		out.WriteString("\nCollector diagnostics\n")
		for _, e := range s.CollectorErrors {
			fmt.Fprintf(&out, "  %s\n", e)
		}
	}
	return out.String()
}

type tickMsg time.Time
type topModel struct {
	a     *app.App
	page  string
	width int
}

func tick() tea.Cmd              { return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }) }
func (m topModel) Init() tea.Cmd { return tick() }
func (m topModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "g":
			m.page = "gpu"
		case "r":
			m.page = "requests"
		case "m":
			m.page = "models"
		case "b":
			m.page = "benchmarks"
		case "o":
			m.page = "status"
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tickMsg:
		return m, tick()
	}
	return m, nil
}
func (m topModel) View() string {
	title := lipgloss.NewStyle().Foreground(lipgloss.Color("#7de2c7")).Bold(true).Render("GPUP / " + m.page)
	return title + "\n\n" + render(m.a.Snapshot(), m.page) + "\n[g] GPUs  [r] requests  [m] models  [b] benchmarks  [o] overview  [q] quit\n"
}
func runTop(cmd *cobra.Command, a *app.App, page string) error {
	p := tea.NewProgram(topModel{a: a, page: page}, tea.WithAltScreen(), tea.WithContext(cmd.Context()), tea.WithInput(cmd.InOrStdin()), tea.WithOutput(cmd.OutOrStdout()))
	_, err := p.Run()
	return err
}
