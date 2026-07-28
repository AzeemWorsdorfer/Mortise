// Command mortised is the Mortise daemon entry point.
//
// mortised reads and validates Mortise configuration files (project
// + global), initializes the SQLite session store at ~/.mortise/mortise.db,
// binds a Unix domain socket at ~/.mortise/mortise.sock, and serves the
// Connect-RPC AgentService.Connect bidi stream.
//
// In ticket 02 the daemon is intentionally narrow: no agent loop, no
// tool execution. Its job is to prove the wire format, config loading,
// SQLite bootstrap, and graceful shutdown all work end-to-end.
//
// See: docs/specs/01-core-agent-harness.md §4.4, §5
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/AzeemWorsdorfer/Mortise/daemon/config"
	mortisedaemon "github.com/AzeemWorsdorfer/Mortise/daemon/daemon"
	"github.com/AzeemWorsdorfer/Mortise/daemon/session"
)

// defaultMortiseDir is the conventional per-user Mortise state
// directory. Mirrors the directory used by the TUI client so the two
// always agree on socket + DB paths.
const defaultMortiseDir = ".mortise"

// runOptions collects CLI configuration in one place so tests /
// future subcommands can construct it without re-reading flags.
type runOptions struct {
	configPath string
	socketPath string
	mortiseDir string
	logLevel   slog.Level
}

// parseFlags binds CLI flags onto a fresh runOptions. Defined as a
// helper so main() stays short and the OptionSet pattern stays
// visible.
//
// The --socket flag has no default: it is derived from --mortise-dir
// at run() time so a user who points mortised at a per-project state
// directory gets the matching socket path automatically.
func parseFlags(args []string) (runOptions, error) {
	fs := flag.NewFlagSet("mortised", flag.ContinueOnError)
	var opts runOptions
	defaultDir := defaultDir()
	fs.StringVar(&opts.configPath, "config", "", "path to project .mortise.json (defaults to <workspace>/.mortise.json)")
	fs.StringVar(&opts.socketPath, "socket", "", "Unix socket path to bind (default: <mortise-dir>/mortise.sock)")
	fs.StringVar(&opts.mortiseDir, "mortise-dir", defaultDir, "Mortise per-user state directory (DB + socket live here)")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	opts.logLevel = slog.LevelInfo
	return opts, nil
}

// defaultDir returns ~/.mortise, honoring $HOME explicitly so the
// daemon does not depend on os.UserHomeDir's behavior under snap or
// unusual filesystems.
func defaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return defaultMortiseDir
	}
	return filepath.Join(home, defaultMortiseDir)
}

// resolveProjectConfig finds the project config: explicit --config
// wins, otherwise <mortise-dir/..>/.mortise.json if running inside a
// project, otherwise empty (project-less mode is allowed).
func resolveProjectConfig(explicit string, mortiseDir string) string {
	if explicit != "" {
		return explicit
	}
	// Heuristic: if --mortise-dir is NOT the user's default, treat
	// its parent as the workspace root and look for .mortise.json
	// there. This lets `mortised --mortise-dir /path/to/proj/.mortise`
	// pick up /path/to/proj/.mortise.json without a separate --config
	// flag.
	if mortiseDir != defaultDir() {
		candidate := filepath.Join(filepath.Dir(mortiseDir), ".mortise.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// workspacePath returns the directory mortised considers the user's
// working directory. Defaults to the current working directory.
func workspacePath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

// branchFor returns the current git branch by shelling out to
// `git rev-parse --abbrev-ref HEAD`. Returns "" if not in a git repo
// or git is unavailable; the SystemStatus event tolerates an empty
// branch.
func branchFor(workspace string) string {
	if workspace == "" {
		return ""
	}
	branch, err := gitBranch(workspace)
	if err != nil {
		return ""
	}
	return branch
}

func main() {
	opts, err := parseFlags(os.Args[1:])
	if err != nil {
		// flag already printed usage.
		os.Exit(2)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: opts.logLevel}))

	if err := run(context.Background(), opts, logger); err != nil {
		logger.Error("mortised exited with error", "err", err)
		os.Exit(1)
	}
}

// run is the testable seam. The optional parent ctx lets tests cancel
// the daemon without poking signals at the test process. When parent
// is nil, run installs signal.NotifyContext for SIGINT/SIGTERM.
func run(parent context.Context, opts runOptions, logger *slog.Logger) error {
	if err := os.MkdirAll(opts.mortiseDir, 0o755); err != nil {
		return fmt.Errorf("create mortise dir %q: %w", opts.mortiseDir, err)
	}
	if opts.socketPath == "" {
		opts.socketPath = filepath.Join(opts.mortiseDir, "mortise.sock")
	}

	cfg, err := loadAndValidateConfig(opts, logger)
	if err != nil {
		return err
	}
	model, provider := resolveModel(cfg)

	_, closeStore, err := openStore(opts, logger)
	if err != nil {
		return err
	}
	defer closeStore()

	listener, err := newListener(opts.socketPath)
	if err != nil {
		return fmt.Errorf("bind socket: %w", err)
	}
	logger.Info("socket bound", "path", opts.socketPath)

	d := buildDaemon(listener, opts, model, provider, logger)
	ctx, stop := setupContext(parent)
	defer stop()

	if err := d.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("daemon serve: %w", err)
	}
	logger.Info("mortised shut down cleanly")
	return nil
}

// loadAndValidateConfig loads the merged project+global config and
// returns nil error only when the config is valid per config.Validate.
func loadAndValidateConfig(opts runOptions, logger *slog.Logger) (*config.Config, error) {
	projectPath := resolveProjectConfig(opts.configPath, opts.mortiseDir)
	globalPath := filepath.Join(opts.mortiseDir, "config.json")
	cfg, err := config.Load(projectPath, globalPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if err := config.Validate(cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	logger.Info("config loaded",
		"project_config", projectPath,
		"global_config", globalPath,
		"version", cfg.Version,
		"provider_default", cfg.Providers.Default,
		"persona", cfg.Persona,
	)
	return cfg, nil
}

// resolveModel extracts the model and provider identifiers from the
// merged config. If the default provider has no model in the models
// map, model is returned as "".
func resolveModel(cfg *config.Config) (model, provider string) {
	provider = cfg.Providers.Default
	if pm, ok := cfg.Providers.Models[provider]; ok {
		model = pm.Model
	}
	return model, provider
}

// openStore creates the SQLite session store and returns it along with
// a close function the caller must defer.
func openStore(opts runOptions, logger *slog.Logger) (*session.Store, func(), error) {
	dbPath := filepath.Join(opts.mortiseDir, "mortise.db")
	store, err := session.Open(context.Background(), dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open session store: %w", err)
	}
	closeFn := func() {
		if cerr := store.Close(); cerr != nil {
			logger.Warn("close session store", "err", cerr)
		}
	}
	return store, closeFn, nil
}

// buildDaemon constructs a daemon.Daemon from the listener and
// resolved configuration.
func buildDaemon(listener net.Listener, opts runOptions, model, provider string, logger *slog.Logger) *mortisedaemon.Daemon {
	return &mortisedaemon.Daemon{
		Listener:   listener,
		SocketPath: opts.socketPath,
		ModelID:    model,
		ProviderID: provider,
		Workspace:  workspacePath(),
		Branch:     branchFor(workspacePath()),
		Logger:     logger.With("component", "daemon"),
	}
}

// setupContext returns a context that cancels when the caller's parent
// context is canceled, or (for top-level production invocation) on
// SIGINT/SIGTERM.
func setupContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent != nil && parent != context.Background() {
		return context.WithCancel(parent)
	}
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
