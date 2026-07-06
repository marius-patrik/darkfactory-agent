// Command rommie is the Agents Harness CLI entrypoint.
//
// Grammar: object-first (rommie <noun> <verb> [args] [flags]) per D3 §C2.
// Global conventions: --json envelope, exit codes, --rommie-home/ROMMIE_HOME, per D3 §C3.
//
// S2.4 scope (stub-level but real behavior):
//   - rommie --version
//   - rommie node init --role client|gateway|cpu|gpu [--rommie-home <path>]
//   - rommie audit source    (delegates to os/agents-manager)
//   - rommie audit secrets   (delegates to os/agents-manager)
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/marius-patrik/agents-harness/services/cli/internal/agent"
	"github.com/marius-patrik/agents-harness/services/cli/internal/cliwrap"
	"github.com/marius-patrik/agents-harness/services/cli/internal/identity"
	"github.com/marius-patrik/agents-harness/services/cli/internal/jsonenv"
	"github.com/marius-patrik/agents-harness/services/cli/internal/orchestrator"
	"github.com/marius-patrik/agents-harness/services/cli/internal/setup"
	"github.com/marius-patrik/agents-harness/services/cli/internal/switcher"
	"github.com/spf13/cobra"
)

// version is injected at build time via:
//
//	go build -ldflags="-X main.version=<ver>" ./cmd/rommie
//
// Falls back to the identity package's BuildVersion, then to reading the
// VERSION file, then to "dev".
var version string

// globalJSON is set by the persistent --json flag (D3 §C3).
var globalJSON bool

// defaultRoot returns the default rommie home, honoring the ROMMIE_HOME env var.
func defaultRoot() string {
	if v := os.Getenv("ROMMIE_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".rommie"
	}
	return filepath.Join(home, ".rommie")
}

// resolvedVersion returns the best available version string, checking the
// ldflags variable, then the identity package's variable, then the VERSION
// file in the binary's parent directories, then "dev".
func resolvedVersion() string {
	if version != "" {
		return version
	}
	if identity.BuildVersion != "" {
		return identity.BuildVersion
	}
	// Walk up from cwd to find a VERSION file.
	dir, _ := os.Getwd()
	for i := 0; i < 10; i++ {
		candidate := filepath.Join(dir, "VERSION")
		if data, err := os.ReadFile(candidate); err == nil {
			v := strings.TrimSpace(string(data))
			if v != "" {
				return v
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "dev"
}

func main() {
	if code, handled := runRawCLI(os.Args[1:]); handled {
		os.Exit(code)
	}
	root := buildRoot()
	os.Exit(run(root))
}

func runRawCLI(args []string) (int, bool) {
	if len(args) > 0 && args[0] == "--json" {
		globalJSON = true
		args = args[1:]
	}
	if len(args) == 0 || args[0] != "cli" {
		return 0, false
	}
	provider := ""
	vendorArgs := args[1:]
	if len(vendorArgs) > 0 && vendorArgs[0] == "--" {
		vendorArgs = vendorArgs[1:]
	}
	agentName := ""
	model := ""
	if len(vendorArgs) > 0 {
		// Explicit provider argument: validate and forward the rest.
		provider = vendorArgs[0]
		vendorArgs = vendorArgs[1:]
		if !agent.ValidProvider(provider) {
			err := fmt.Errorf("unknown provider %q", provider)
			if globalJSON {
				_ = jsonenv.PrintError(err.Error(), 2)
			} else {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
			return 2, true
		}
		resolvedAgent, err := switcher.ResolveAgent(defaultRoot())
		if err != nil {
			if globalJSON {
				_ = jsonenv.PrintError(err.Error(), 2)
			} else {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
			return 2, true
		}
		agentName = resolvedAgent
	} else {
		// No explicit provider: resolve from the switcher so agent/provider/model
		// orthogonality is honored at the raw CLI boundary.
		resolvedAgent, resolvedProvider, resolvedModel, err := switcher.Resolve(defaultRoot())
		if err != nil {
			if globalJSON {
				_ = jsonenv.PrintError(err.Error(), 2)
			} else {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
			return 2, true
		}
		agentName = resolvedAgent
		provider = resolvedProvider
		model = resolvedModel
	}
	if err := cliwrap.ExecWithEnv(provider, vendorArgs, agentEnv(agentName, model)); err != nil {
		exitCode := 1
		if wrapErr, ok := err.(*cliwrap.Error); ok {
			exitCode = wrapErr.Code
		}
		if globalJSON {
			_ = jsonenv.PrintError(err.Error(), exitCode)
		} else {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		return exitCode, true
	}
	return 0, true
}

// buildRoot constructs and configures the cobra command tree.
func buildRoot() *cobra.Command {
	var rootCmd = &cobra.Command{
		Use:   "rommie",
		Short: "rommie — the Agents Harness runtime control surface",
		Long: `rommie is the CLI for the Rommie runtime platform.

Grammar: rommie <noun> <verb> [args] [flags]  (object-first, D3 §C2)

Managed by Agentos through the agents-harness package.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	rootCmd.PersistentFlags().BoolVar(&globalJSON, "json", false, "emit machine-readable JSON envelope (D3 §C3)")

	// --version / version sub-command (D3 §C13, LOCKED S0).
	rootCmd.Version = resolvedVersion()
	rootCmd.SetVersionTemplate("rommie version {{.Version}}\n")

	// `rommie version` as an explicit subcommand (D3 §C13; alias for --version).
	rootCmd.AddCommand(buildVersionCmd())
	// noun: node
	rootCmd.AddCommand(buildNodeCmd())
	// noun: audit
	rootCmd.AddCommand(buildAuditCmd())
	// noun: cli
	rootCmd.AddCommand(buildCliCmd())
	// noun: setup
	rootCmd.AddCommand(buildSetupCmd())
	// noun: orchestrator
	rootCmd.AddCommand(buildOrchestratorCmd())
	// noun: agent
	rootCmd.AddCommand(buildAgentCmd())
	// noun: provider
	rootCmd.AddCommand(buildProviderCmd())
	// noun: switcher
	rootCmd.AddCommand(buildSwitcherCmd())

	return rootCmd
}

func run(cmd *cobra.Command) int {
	if err := cmd.Execute(); err != nil {
		if globalJSON {
			_ = jsonenv.PrintError(err.Error(), 1)
		} else {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// version subcommand (D3 §C13 — alias for --version; LOCKED S0)
// ---------------------------------------------------------------------------

func buildVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the rommie version (D3 §C13)",
		Long: `Print the binary version, equivalent to rommie --version.

In future slices this will also report the cluster version and warn on skew
(D-019 coordinated cutover). For now it prints the binary version only.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ver := resolvedVersion()
			if globalJSON {
				return jsonenv.Print(map[string]any{
					"version": ver,
				})
			}
			fmt.Printf("rommie version %s\n", ver)
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// noun: cli
// ---------------------------------------------------------------------------

func buildCliCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [provider] [args...]",
		Short:              "Delegate vendor CLI execution to os/agents-manager",
		Long: `Delegate vendor CLI execution to os/agents-manager.

If <provider> is omitted, the current resolved provider (from the switcher) is
used. This is the runtime dispatch path for agent/provider orthogonality.`,
		DisableFlagParsing: true,
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
		RunE: func(cmd *cobra.Command, args []string) error {
			provider := ""
			agentName := ""
			model := ""
			if len(args) > 0 {
				// Explicit provider argument: validate and forward the rest.
				provider = args[0]
				args = args[1:]
				if !agent.ValidProvider(provider) {
					err := fmt.Errorf("unknown provider %q", provider)
					if globalJSON {
						_ = jsonenv.PrintError(err.Error(), 2)
					} else {
						fmt.Fprintln(os.Stderr, "error:", err)
					}
					os.Exit(2)
				}
				resolvedAgent, err := switcher.ResolveAgent(defaultRoot())
				if err != nil {
					if globalJSON {
						_ = jsonenv.PrintError(err.Error(), 2)
					} else {
						fmt.Fprintln(os.Stderr, "error:", err)
					}
					os.Exit(2)
				}
				agentName = resolvedAgent
			} else {
				// No explicit provider: resolve from the switcher so
				// agent/provider/model orthogonality is honored.
				resolvedAgent, resolvedProvider, resolvedModel, err := switcher.Resolve(defaultRoot())
				if err != nil {
					if globalJSON {
						_ = jsonenv.PrintError(err.Error(), 2)
					} else {
						fmt.Fprintln(os.Stderr, "error:", err)
					}
					os.Exit(2)
				}
				agentName = resolvedAgent
				provider = resolvedProvider
				model = resolvedModel
			}
			if err := cliwrap.ExecWithEnv(provider, args, agentEnv(agentName, model)); err != nil {
				exitCode := 1
				if wrapErr, ok := err.(*cliwrap.Error); ok {
					exitCode = wrapErr.Code
				}
				if globalJSON {
					_ = jsonenv.PrintError(err.Error(), exitCode)
				} else {
					fmt.Fprintln(os.Stderr, "error:", err)
				}
				os.Exit(exitCode)
			}
			return nil
		},
	}
	return cmd
}

// ---------------------------------------------------------------------------
// noun: setup
// ---------------------------------------------------------------------------

func buildSetupCmd() *cobra.Command {
	var rommieHome string
	var materializeCreds bool

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Initialise local harness structure and verify manager-owned CLI adapters",
		RunE: func(cmd *cobra.Command, args []string) error {
			if rommieHome == "" {
				rommieHome = defaultRoot()
			}

			report, err := setup.Run(setup.Options{Root: rommieHome, MaterializeCreds: materializeCreds})
			if err != nil {
				if globalJSON {
					_ = jsonenv.PrintError(err.Error(), 1)
				} else {
					fmt.Fprintln(os.Stderr, "error: setup:", err)
				}
				os.Exit(1)
			}

			exitCode := 0
			if setup.HasPresentBinFailure(report) {
				exitCode = setup.ExitCodeNoFalseGreen
			}

			if globalJSON {
				_ = jsonenv.Print(report)
				if exitCode != 0 {
					os.Exit(exitCode)
				}
				return nil
			}

			fmt.Printf("setup: root=%s role=%s hostname=%s\n", report.Root, report.Role, report.Hostname)
			if materializeCreds {
				fmt.Printf("%-8s %-9s %-12s %-7s %s\n", "cred", "src_found", "materialized", "bytes", "dst")
				for _, cred := range report.Creds {
					fmt.Printf("%-8s %-9t %-12t %-7d %s\n", cred.CLI, cred.SrcFound, cred.Materialized, cred.Bytes, cred.Dst)
				}
			}
			fmt.Printf("%-8s %-5s %-5s %s\n", "cli", "bin", "ok", "version/error")
			for _, check := range report.Checks {
				binFound := "no"
				if check.BinFound {
					binFound = "yes"
				}
				ok := "no"
				if check.OK {
					ok = "yes"
				}
				detail := check.Version
				if detail == "" {
					detail = check.Error
				}
				fmt.Printf("%-8s %-5s %-5s %s\n", check.CLI, binFound, ok, detail)
			}
			if exitCode != 0 {
				os.Exit(exitCode)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&rommieHome, "rommie-home", "", "rommie home directory (default $ROMMIE_HOME or ~/.rommie)")
	cmd.Flags().StringVar(&rommieHome, "root", "", "alias for --rommie-home")
	cmd.Flags().BoolVar(&materializeCreds, "materialize-creds", false, "ask os/agents-manager to materialize provider CLI credentials")
	return cmd
}

// ---------------------------------------------------------------------------
// noun: orchestrator
// ---------------------------------------------------------------------------

func buildOrchestratorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "orchestrator",
		Short: "Run harness-owned orchestration planning surfaces",
		Long: "Run harness-owned orchestration planning surfaces.\n\n" +
			"The JSON fields match docs/adapters/darkfactory.md so control-plane adapters " +
			"can hand scheduling decisions to the harness without adopting a second engine.",
	}
	cmd.AddCommand(buildOrchestratorScheduleCmd())
	cmd.AddCommand(buildOrchestratorObserveCmd())
	cmd.AddCommand(buildOrchestratorNonProgressCmd())
	return cmd
}

func buildOrchestratorScheduleCmd() *cobra.Command {
	var inputPath string

	cmd := &cobra.Command{
		Use:   "schedule",
		Short: "Select dispatchable work from a stream/work-unit snapshot",
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := readScheduleRequest(inputPath)
			if err != nil {
				return err
			}
			resp, err := orchestrator.Schedule(req)
			if err != nil {
				return err
			}
			if globalJSON {
				return jsonenv.Print(resp)
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetEscapeHTML(false)
			enc.SetIndent("", "  ")
			return enc.Encode(resp)
		},
	}
	cmd.Flags().StringVar(&inputPath, "input", "-", "schedule request JSON file, or - for stdin")
	return cmd
}

func readScheduleRequest(inputPath string) (orchestrator.ScheduleRequest, error) {
	var reader io.Reader = os.Stdin
	if inputPath != "" && inputPath != "-" {
		file, err := os.Open(inputPath)
		if err != nil {
			return orchestrator.ScheduleRequest{}, fmt.Errorf("orchestrator schedule: open input: %w", err)
		}
		defer file.Close()
		reader = file
	}
	var req orchestrator.ScheduleRequest
	dec := json.NewDecoder(reader)
	if err := dec.Decode(&req); err != nil {
		return orchestrator.ScheduleRequest{}, fmt.Errorf("orchestrator schedule: decode input: %w", err)
	}
	return req, nil
}

func buildOrchestratorObserveCmd() *cobra.Command {
	var inputPath string

	cmd := &cobra.Command{
		Use:   "observe",
		Short: "Normalize worker-run ledger state and heartbeat observations",
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := readObserveRunsRequest(inputPath)
			if err != nil {
				return err
			}
			resp, err := orchestrator.ObserveRuns(req)
			if err != nil {
				return err
			}
			if globalJSON {
				return jsonenv.Print(resp)
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetEscapeHTML(false)
			enc.SetIndent("", "  ")
			return enc.Encode(resp)
		},
	}
	cmd.Flags().StringVar(&inputPath, "input", "-", "observe request JSON file, or - for stdin")
	return cmd
}

func readObserveRunsRequest(inputPath string) (orchestrator.ObserveRunsRequest, error) {
	var reader io.Reader = os.Stdin
	if inputPath != "" && inputPath != "-" {
		file, err := os.Open(inputPath)
		if err != nil {
			return orchestrator.ObserveRunsRequest{}, fmt.Errorf("orchestrator observe: open input: %w", err)
		}
		defer file.Close()
		reader = file
	}
	var req orchestrator.ObserveRunsRequest
	dec := json.NewDecoder(reader)
	if err := dec.Decode(&req); err != nil {
		return orchestrator.ObserveRunsRequest{}, fmt.Errorf("orchestrator observe: decode input: %w", err)
	}
	return req, nil
}

func buildOrchestratorNonProgressCmd() *cobra.Command {
	var inputPath string

	cmd := &cobra.Command{
		Use:   "non-progress",
		Short: "Flag candidate stuck worker runs for review confirmation",
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := readNonProgressRequest(inputPath)
			if err != nil {
				return err
			}
			resp, err := orchestrator.DetectNonProgress(req)
			if err != nil {
				return err
			}
			if globalJSON {
				return jsonenv.Print(resp)
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetEscapeHTML(false)
			enc.SetIndent("", "  ")
			return enc.Encode(resp)
		},
	}
	cmd.Flags().StringVar(&inputPath, "input", "-", "non-progress request JSON file, or - for stdin")
	return cmd
}

func readNonProgressRequest(inputPath string) (orchestrator.NonProgressRequest, error) {
	var reader io.Reader = os.Stdin
	if inputPath != "" && inputPath != "-" {
		file, err := os.Open(inputPath)
		if err != nil {
			return orchestrator.NonProgressRequest{}, fmt.Errorf("orchestrator non-progress: open input: %w", err)
		}
		defer file.Close()
		reader = file
	}
	var req orchestrator.NonProgressRequest
	dec := json.NewDecoder(reader)
	if err := dec.Decode(&req); err != nil {
		return orchestrator.NonProgressRequest{}, fmt.Errorf("orchestrator non-progress: decode input: %w", err)
	}
	return req, nil
}

// ---------------------------------------------------------------------------
// noun: node
// ---------------------------------------------------------------------------

func buildNodeCmd() *cobra.Command {
	nodeCmd := &cobra.Command{
		Use:   "node",
		Short: "Node identity and cluster enrollment (D3 §C8)",
	}
	nodeCmd.AddCommand(buildNodeInitCmd())
	return nodeCmd
}

func buildNodeInitCmd() *cobra.Command {
	var role string
	var rommieHome string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Ask os/agents-manager to initialise this node's identity",
		Long: `Delegates node identity and runtime-root materialization to os/agents-manager.
The harness then reads the manager-materialized node.yaml projection.

Role must be one of: client, gateway, cpu, gpu  (§19 RS1).

Idempotency and schema ownership live in agents-manager / agents-core.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if rommieHome == "" {
				rommieHome = defaultRoot()
			}

			r := identity.Role(role)
			opts := identity.InitOptions{Root: rommieHome, Role: r}
			if err := identity.Init(opts); err != nil {
				exitCode := 2 // usage/validation (D3 §C3)
				if globalJSON {
					_ = jsonenv.PrintError(err.Error(), exitCode)
				} else {
					fmt.Fprintln(os.Stderr, "error: node init:", err)
				}
				os.Exit(exitCode)
			}

			if globalJSON {
				node, err := identity.ReadNodeYAML(rommieHome)
				if err != nil {
					_ = jsonenv.PrintError(err.Error(), 1)
					os.Exit(1)
				}
				return jsonenv.Print(map[string]any{
					"root":     rommieHome,
					"role":     node.Role,
					"hostname": node.Hostname,
				})
			}

			fmt.Printf("node init: OK\n  root:     %s\n  role:     %s\n", rommieHome, role)
			node, err := identity.ReadNodeYAML(rommieHome)
			if err == nil {
				fmt.Printf("  hostname: %s\n", node.Hostname)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&role, "role", "", "node role: client, gateway, cpu, or gpu (required; §19 RS1)")
	cmd.Flags().StringVar(&rommieHome, "rommie-home", "", "rommie home directory (default $ROMMIE_HOME or ~/.rommie)")
	// --root is an ergonomic alias for --rommie-home (used in one-liners / tests).
	cmd.Flags().StringVar(&rommieHome, "root", "", "alias for --rommie-home")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

// ---------------------------------------------------------------------------
// noun: audit
// ---------------------------------------------------------------------------

func buildAuditCmd() *cobra.Command {
	auditCmd := &cobra.Command{
		Use:   "audit",
		Short: "Delegate code/state hygiene gates to os/agents-manager",
	}
	auditCmd.AddCommand(buildAuditSourceCmd())
	auditCmd.AddCommand(buildAuditSecretsCmd())
	return auditCmd
}

func buildAuditSourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "source [manager args...]",
		Short:              "Run the manager-owned source hygiene audit",
		DisableFlagParsing: true,
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runManagedAudit("source", args)
		},
	}
	return cmd
}

func buildAuditSecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "secrets [manager args...]",
		Short:              "Run the manager-owned secrets hygiene audit",
		DisableFlagParsing: true,
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runManagedAudit("secrets", args)
		},
	}
	return cmd
}

func runManagedAudit(target string, args []string) error {
	agentsBin := os.Getenv("AGENTS_BIN")
	if agentsBin == "" || os.Getenv("AGENTS_HOME") == "" {
		return fmt.Errorf("audit %s is owned by os/agents-manager; set AGENTS_BIN and AGENTS_HOME or run through Agentos", target)
	}
	argv := []string{}
	if script := os.Getenv("AGENTS_BIN_SCRIPT"); script != "" {
		argv = append(argv, script)
	}
	argv = append(argv, "audit", target)
	if globalJSON {
		argv = append(argv, "--json")
	}
	argv = append(argv, args...)
	cmd := exec.Command(agentsBin, argv...)
	cmd.Env = os.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// noun: agent
// ---------------------------------------------------------------------------

func buildAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Agent persona selection and inspection (D-011 agent⊥provider)",
		Long: `Agent personas are independent of the provider/model that serves them.
The four provider agents default to their namesake provider, but any agent can be
switched to any provider with 'rommie provider use'.`,
	}
	cmd.AddCommand(buildAgentListCmd())
	cmd.AddCommand(buildAgentShowCmd())
	cmd.AddCommand(buildAgentUseCmd())
	return cmd
}

func buildAgentListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List seeded agents and their default providers",
		RunE: func(cmd *cobra.Command, args []string) error {
			if globalJSON {
				items := []map[string]any{}
				for _, name := range agent.Names() {
					a, _ := agent.Get(name)
					items = append(items, map[string]any{
						"name":             a.Name,
						"default_provider": a.DefaultProvider,
						"description":      a.Description,
					})
				}
				return jsonenv.Print(map[string]any{"agents": items})
			}
			fmt.Printf("%-10s %-10s %s\n", "AGENT", "DEFAULT", "DESCRIPTION")
			for _, name := range agent.Names() {
				a, _ := agent.Get(name)
				fmt.Printf("%-10s %-10s %s\n", a.Name, a.DefaultProvider, a.Description)
			}
			return nil
		},
	}
}

func buildAgentShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show an agent's persona, default provider, skills, memory, and history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, ok := agent.Get(args[0])
			if !ok {
				return fmt.Errorf("unknown agent: %s", args[0])
			}
			if globalJSON {
				return jsonenv.Print(map[string]any{
					"name":             a.Name,
					"description":      a.Description,
					"default_provider": a.DefaultProvider,
					"prompt":           a.Prompt,
					"skills":           a.Skills,
					"memory":           a.Memory,
					"history":          a.History,
				})
			}
			fmt.Printf("name:             %s\n", a.Name)
			fmt.Printf("description:      %s\n", a.Description)
			fmt.Printf("default_provider: %s\n", a.DefaultProvider)
			fmt.Printf("prompt:           %s\n", a.Prompt)
			fmt.Printf("skills:           %v\n", a.Skills)
			fmt.Printf("memory:           %v\n", a.Memory)
			fmt.Printf("history:          %v\n", a.History)
			return nil
		},
	}
}

func buildAgentUseCmd() *cobra.Command {
	var session, project, global bool
	var rommieHome string
	cmd := &cobra.Command{
		Use:   "use <name> [--session|--project|--global] (default: project)",
		Short: "Select the active agent for the requested scope",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := switcher.ParseScope(session, project, global)
			if err != nil {
				return err
			}
			root := rommieHome
			if root == "" {
				root = defaultRoot()
			}
			if err := switcher.SetAgent(root, scope, args[0]); err != nil {
				return err
			}
			if globalJSON {
				return jsonenv.Print(map[string]any{"agent": args[0], "scope": scope.String()})
			}
			fmt.Printf("agent set to %s (%s)\n", args[0], scope)
			return nil
		},
	}
	cmd.Flags().BoolVar(&session, "session", false, "apply to the current session")
	cmd.Flags().BoolVar(&project, "project", false, "apply to the current project")
	cmd.Flags().BoolVar(&global, "global", false, "apply globally")
	cmd.Flags().StringVar(&rommieHome, "rommie-home", "", "rommie home directory (default $ROMMIE_HOME or ~/.rommie)")
	cmd.Flags().StringVar(&rommieHome, "root", "", "alias for --rommie-home")
	return cmd
}

// ---------------------------------------------------------------------------
// noun: provider
// ---------------------------------------------------------------------------

func buildProviderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provider",
		Short: "Provider/model switcher (D-011 agent⊥provider)",
		Long: `The provider is orthogonal to the agent persona. Any agent can run on
any supported provider/model via the switcher.`,
	}
	cmd.AddCommand(buildProviderUseCmd())
	return cmd
}

func buildProviderUseCmd() *cobra.Command {
	var session, project, global bool
	var rommieHome, model string
	cmd := &cobra.Command{
		Use:   "use <provider> [--model <model>] [--session|--project|--global] (default: project)",
		Short: "Select the active provider and optional model for the requested scope",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := switcher.ParseScope(session, project, global)
			if err != nil {
				return err
			}
			root := rommieHome
			if root == "" {
				root = defaultRoot()
			}
			if err := switcher.SetProvider(root, scope, args[0]); err != nil {
				return err
			}
			if model != "" {
				if err := switcher.SetModel(root, scope, model); err != nil {
					return err
				}
			}
			if globalJSON {
				return jsonenv.Print(map[string]any{"provider": args[0], "model": model, "scope": scope.String()})
			}
			fmt.Printf("provider set to %s (%s)\n", args[0], scope)
			if model != "" {
				fmt.Printf("model set to %s (%s)\n", model, scope)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&session, "session", false, "apply to the current session")
	cmd.Flags().BoolVar(&project, "project", false, "apply to the current project")
	cmd.Flags().BoolVar(&global, "global", false, "apply globally")
	cmd.Flags().StringVar(&model, "model", "", "optional model override for the provider")
	cmd.Flags().StringVar(&rommieHome, "rommie-home", "", "rommie home directory (default $ROMMIE_HOME or ~/.rommie)")
	cmd.Flags().StringVar(&rommieHome, "root", "", "alias for --rommie-home")
	return cmd
}

// ---------------------------------------------------------------------------
// noun: switcher
// ---------------------------------------------------------------------------

func buildSwitcherCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "switcher",
		Short: "Show resolved agent/provider selection",
	}
	cmd.AddCommand(buildSwitcherShowCmd())
	return cmd
}

func buildSwitcherShowCmd() *cobra.Command {
	var rommieHome string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the resolved agent, provider, and model",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := rommieHome
			if root == "" {
				root = defaultRoot()
			}
			agentName, provider, model, err := switcher.Resolve(root)
			if err != nil {
				return err
			}
			if globalJSON {
				return jsonenv.Print(map[string]any{
					"agent":    agentName,
					"provider": provider,
					"model":    model,
				})
			}
			fmt.Printf("agent:    %s\nprovider: %s\n", agentName, provider)
			if model != "" {
				fmt.Printf("model:    %s\n", model)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&rommieHome, "rommie-home", "", "rommie home directory (default $ROMMIE_HOME or ~/.rommie)")
	cmd.Flags().StringVar(&rommieHome, "root", "", "alias for --rommie-home")
	return cmd
}

// agentEnv returns environment variables that inject the selected agent persona
// and model into a provider CLI invocation, making agent/provider/model
// orthogonality visible at the runtime boundary.
func agentEnv(agentName, model string) []string {
	if agentName == "" && model == "" {
		return nil
	}
	env := []string{}
	if agentName != "" {
		a, ok := agent.Get(agentName)
		if !ok {
			env = append(env, fmt.Sprintf("ROMMIE_AGENT=%s", agentName))
		} else {
			env = append(env,
				fmt.Sprintf("ROMMIE_AGENT=%s", a.Name),
				fmt.Sprintf("ROMMIE_AGENT_PROMPT=%s", a.Prompt),
				fmt.Sprintf("ROMMIE_AGENT_DEFAULT_PROVIDER=%s", a.DefaultProvider),
			)
			if len(a.Skills) > 0 {
				env = append(env, fmt.Sprintf("ROMMIE_AGENT_SKILLS=%s", strings.Join(a.Skills, ",")))
			}
			if len(a.Memory) > 0 {
				env = append(env, fmt.Sprintf("ROMMIE_AGENT_MEMORY=%s", strings.Join(a.Memory, "|")))
			}
			if len(a.History) > 0 {
				env = append(env, fmt.Sprintf("ROMMIE_AGENT_HISTORY=%s", strings.Join(a.History, "|")))
			}
		}
	}
	if model != "" {
		env = append(env, fmt.Sprintf("ROMMIE_AGENT_MODEL=%s", model))
	}
	return env
}

