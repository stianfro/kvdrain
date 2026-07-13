package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/stianfro/kvdrain/internal/coordinator"
	"github.com/stianfro/kvdrain/internal/kube"
	"github.com/stianfro/kvdrain/internal/render"
	"github.com/stianfro/kvdrain/internal/state"
)

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

type globalOptions struct {
	context, kubeconfig, output string
	noTTY, json                 bool
}
type app struct {
	opts        globalOptions
	out, errOut io.Writer
	runID       string
}

func Execute() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() { <-sig; cancel(); <-sig; os.Exit(130) }()
	cmd := NewRootCommand(os.Stdout, os.Stderr)
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, render.SanitizeHuman(err.Error()))
		return coordinator.ExitCode(err)
	}
	return 0
}

func NewRootCommand(out, errOut io.Writer) *cobra.Command {
	a := &app{out: out, errOut: errOut, runID: newRunID()}
	root := &cobra.Command{Use: "kvdrain", Short: "Safely drain KubeVirt nodes", SilenceErrors: true, SilenceUsage: true}
	root.SetOut(out)
	root.SetErr(errOut)
	f := root.PersistentFlags()
	f.StringVar(&a.opts.context, "context", "", "kubeconfig context")
	f.StringVar(&a.opts.kubeconfig, "kubeconfig", "", "path to kubeconfig")
	f.BoolVar(&a.opts.noTTY, "no-tty", false, "disable interactive terminal output")
	f.BoolVar(&a.opts.json, "json", false, "emit NDJSON events")
	f.StringVarP(&a.opts.output, "output", "o", "", "output format (wide)")
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if a.opts.output != "" && a.opts.output != "wide" {
			return coordinator.Usage("unsupported output format %q, only wide is supported", a.opts.output)
		}
		if a.opts.json && a.opts.output != "" {
			return coordinator.Usage("--json and --output cannot be used together")
		}
		return nil
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return coordinator.Usage("%v", err) })
	root.AddCommand(a.drainCommand(), a.statusCommand(), a.watchCommand(), a.uncordonCommand(), versionCommand(out), completionCommand(root))
	return root
}

func (a *app) clients() (kube.Clients, error) {
	cfg, err := kube.NewConfig(kube.ConfigOptions{Kubeconfig: a.opts.kubeconfig, Context: a.opts.context})
	if err != nil {
		return kube.Clients{}, coordinator.Usage("%v", err)
	}
	c, err := kube.NewClients(cfg)
	if err != nil {
		return kube.Clients{}, coordinator.Usage("%v", err)
	}
	return c, nil
}
func (a *app) coord(node string) (*coordinator.Coordinator, error) {
	clients, err := a.clients()
	if err != nil {
		return nil, err
	}
	renderer := render.New(a.out, render.Options{JSON: a.opts.json, NoTTY: a.opts.noTTY, Wide: a.opts.output == "wide"})
	return &coordinator.Coordinator{Clients: clients, Emit: renderer.Event, Node: node, RunID: a.runID}, nil
}
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return coordinator.Usage("%s requires exactly %d argument(s)", cmd.CommandPath(), n)
		}
		return nil
	}
}

func (a *app) statusCommand() *cobra.Command {
	return &cobra.Command{Use: "status <node>", Short: "Inspect drain readiness without mutation", Args: exactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, e := a.coord(args[0])
		if e != nil {
			return e
		}
		return c.Status(cmd.Context())
	}}
}
func (a *app) watchCommand() *cobra.Command {
	return &cobra.Command{Use: "watch [node...]", Short: "Watch active KubeVirt migrations", Args: func(_ *cobra.Command, args []string) error { return nil }, RunE: func(cmd *cobra.Command, args []string) error {
		c, e := a.coord("")
		if e != nil {
			return e
		}
		nodes := map[string]bool{}
		for _, n := range args {
			nodes[n] = true
		}
		return c.Watch(cmd.Context(), nodes)
	}}
}
func (a *app) uncordonCommand() *cobra.Command {
	return &cobra.Command{Use: "uncordon <node>", Short: "Mark a node schedulable", Args: exactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		clients, e := a.clients()
		if e != nil {
			return e
		}
		if e = clients.CheckUncordonPermissions(cmd.Context()); e != nil {
			return coordinator.Operational("uncordon permissions: %v", e)
		}
		if e = clients.SetUnschedulable(cmd.Context(), args[0], false); e != nil {
			return coordinator.Operational("uncordon: %v", e)
		}
		r := render.New(a.out, render.Options{JSON: a.opts.json, NoTTY: a.opts.noTTY, Wide: a.opts.output == "wide"})
		return r.Event(state.Event{APIVersion: state.APIVersion, Kind: "Event", Time: time.Now().UTC(), RunID: a.runID, Type: "node", Node: args[0], Object: &state.ObjectRef{Kind: "Node", Name: args[0]}, State: "uncordoned", Message: "node marked schedulable"})
	}}
}
func (a *app) drainCommand() *cobra.Command {
	o := coordinator.DrainOptions{}
	cmd := &cobra.Command{Use: "drain <node>", Short: "Cordon and safely evacuate a KubeVirt node", Args: exactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, e := a.coord(args[0])
		if e != nil {
			return e
		}
		return c.Drain(cmd.Context(), o)
	}}
	f := cmd.Flags()
	f.DurationVar(&o.Timeout, "timeout", 45*time.Minute, "overall drain timeout")
	f.IntVar(&o.Retries, "retries", 1, "failed migration retry budget per VMI")
	f.IntVar(&o.ParallelOutbound, "parallel-outbound", 0, "maximum outbound migrations (only stricter than KubeVirt)")
	f.BoolVar(&o.Force, "force", false, "allow eviction of unmanaged pods")
	f.BoolVar(&o.DeleteEmptyDirData, "delete-emptydir-data", false, "allow eviction of pods using emptyDir")
	f.Int64Var(&o.GracePeriod, "grace-period", -1, "pod termination grace period")
	f.BoolVar(&o.AbortUncordons, "abort-uncordons", false, "uncordon a node cordoned by this run after an abort")
	return cmd
}

func versionCommand(out io.Writer) *cobra.Command {
	return &cobra.Command{Use: "version", Short: "Print version information", Args: exactArgs(0), RunE: func(_ *cobra.Command, _ []string) error {
		_, e := fmt.Fprintf(out, "kvdrain %s (commit %s, built %s)\n", Version, Commit, Date)
		return e
	}}
}
func completionCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{Use: "completion [bash|zsh|fish|powershell]", Short: "Generate shell completion", Args: exactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		switch strings.ToLower(args[0]) {
		case "bash":
			return root.GenBashCompletion(cmd.OutOrStdout())
		case "zsh":
			return root.GenZshCompletion(cmd.OutOrStdout())
		case "fish":
			return root.GenFishCompletion(cmd.OutOrStdout(), true)
		case "powershell":
			return root.GenPowerShellCompletion(cmd.OutOrStdout())
		default:
			return coordinator.Usage("unsupported shell %q", args[0])
		}
	}}
}
func newRunID() string {
	b := make([]byte, 8)
	if _, e := rand.Read(b); e != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
