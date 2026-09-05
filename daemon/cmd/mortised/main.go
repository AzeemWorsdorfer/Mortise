// Command mortised is the Mortise daemon entry point.
//
// mortised reads and validates Mortise configuration files (project
// + global), initializes the SQLite session store at
// ~/.mortise/mortise.db, loads (or creates) the session row for the
// current workspace, binds a Unix domain socket at
// ~/.mortise/mortise.sock, and serves the Connect-RPC
// AgentService.Connect bidi stream.
//
// In ticket 04 the daemon loads the persistent session on startup so
// the TUI sees real session identity in SystemStatus. On the first
// client connect it starts the agent loop in demo mode with a mock
// provider (ticket 06), streaming live phase transitions and
// tool-call events to the TUI. Tool execution is still future work.
//
// See: docs/specs/01-core-agent-harness.md §4.4, §5
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

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

// daemonInputs groups the per-process values that buildDaemon needs
// from run(): the loaded session, the persistence store, and the
// model/provider identifiers. Grouped to keep buildDaemon's
// signature under the 5-parameter rule.
type daemonInputs struct {
	store         *session.Store
	sess          *session.Session
	model         string
	provider      string
	undoStackSize int
	toolApproval  map[string]string
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

// gitBranch returns the current git branch for workspace, or an error
// when workspace is not inside a working tree or git is unavailable.
// A 5-second deadline prevents hanging on a corrupted repo or NFS stall.
func gitBranch(workspace string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = workspace
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// main parses CLI flags, builds a logger, and runs the daemon until
// it shuts down gracefully on SIGINT/SIGTERM. Exit codes: 2 for flag
// errors, 1 for runtime errors, 0 for a clean shutdown.
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
	if err := os.MkdirAll(opts.mortiseDir, session.DefaultDirPerm); err != nil {
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

	store, closeStore, err := openStore(opts, logger)
	if err != nil {
		return err
	}
	defer closeStore()

	workspace := workspacePath()
	sess, err := loadOrCreateSession(parent, store, workspace, cfg, logger)
	if err != nil {
		return err
	}

	listener, err := newListener(opts.socketPath)
	if err != nil {
		return fmt.Errorf("bind socket: %w", err)
	}
	logger.Info("socket bound", "path", opts.socketPath)

	d := buildDaemon(listener, opts, daemonInputs{
		store:         store,
		sess:          sess,
		model:         model,
		provider:      provider,
		undoStackSize: cfg.Tools.UndoStackSize,
		toolApproval:  cfg.Tools.Approval,
	}, logger)
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

// openStore creates the SQLite session store and returns it along
// with a close function the caller must defer.
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

// newListener binds the requested Unix socket. Delegates to the
// daemon package so socket-binding policy (stale-socket cleanup,
// mode locking, parent-dir creation) lives in one place.
func newListener(socketPath string) (net.Listener, error) {
	if socketPath == "" {
		socketPath = defaultDir() + "/mortise.sock"
	}
	return mortisedaemon.NewUnixListener(socketPath)
}

// loadOrCreateSession resolves the session for the current workspace
// from the SQLite store. If no row exists for the workspace-derived
// ID, a new session is created in StatusRunning and persisted. If a
// row exists, the session is transitioned to StatusRunning (when it
// is not already running, e.g., crash recovery) and updated. A
// session in the terminal StatusCompleted state is returned as-is —
// the user has explicitly marked the session as done and re-running
// the daemon should not silently revive it.
//
// This function is the bridge between the on-disk row and the
// in-memory value the daemon hands to the handler.
func loadOrCreateSession(
	ctx context.Context,
	store *session.Store,
	workspace string,
	cfg *config.Config,
	logger *slog.Logger,
) (*session.Session, error) {
	id := session.NewSessionID(workspace)
	sess, err := store.GetSession(ctx, id)
	switch {
	case err == nil:
		// Already Running: nothing to do, just hand it back.
		if sess.Status == session.StatusRunning {
			logger.Info("session resumed",
				"session_id", id,
				"workspace", workspace,
			)
			return sess, nil
		}
		// Terminal: the user marked this session done. Do not try
		// to revive it; the future handoff / restart-session flow
		// (later ticket) will own that decision.
		if sess.Status == session.StatusCompleted {
			logger.Warn("session is completed; leaving as-is",
				"session_id", id,
				"workspace", workspace,
			)
			return sess, nil
		}
		// Paused / Errored / Crashed: bring it back to Running.
		if terr := sess.Transition(session.StatusRunning); terr != nil {
			return nil, fmt.Errorf("reopen session %q: %w", id, terr)
		}
		if uerr := store.UpdateSession(ctx, sess); uerr != nil {
			return nil, fmt.Errorf("reopen session %q: persist: %w", id, uerr)
		}
		logger.Info("session reopened",
			"session_id", id,
			"workspace", workspace,
			"prior_status", sess.Status.String(),
		)
		return sess, nil
	case errors.Is(err, session.ErrSessionNotFound):
		return createSession(ctx, store, id, workspace, cfg, logger)
	default:
		return nil, fmt.Errorf("load session %q: %w", id, err)
	}
}

// createSession constructs a fresh Session for workspace, transitions
// it to Running, and persists the row.
func createSession(
	ctx context.Context,
	store *session.Store,
	id, workspace string,
	cfg *config.Config,
	logger *slog.Logger,
) (*session.Session, error) {
	now := time.Now().UTC().Truncate(time.Second)
	sess := &session.Session{
		ID:            id,
		Name:          session.DefaultName(workspace),
		Status:        session.StatusCreated,
		WorkspacePath: workspace,
		Branch:        branchFor(workspace),
		TurnCount:     0,
		CreatedAt:     now,
		LastActiveAt:  now,
		ConfigJSON:    serializeConfig(cfg),
	}
	if err := sess.Transition(session.StatusRunning); err != nil {
		return nil, fmt.Errorf("activate new session: %w", err)
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		return nil, fmt.Errorf("persist new session: %w", err)
	}
	logger.Info("session created",
		"session_id", id,
		"workspace", workspace,
		"name", sess.Name,
	)
	return sess, nil
}

// serializeConfig renders cfg as opaque JSON for storage in the
// session row. The session package does not parse this value; the
// daemon reads it back verbatim in a later ticket.
func serializeConfig(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	return string(b)
}

// buildDaemon constructs a daemon.Daemon from the listener, the
// loaded session, the persistence store, and the resolved config.
func buildDaemon(listener net.Listener, opts runOptions, in daemonInputs, logger *slog.Logger) *mortisedaemon.Daemon {
	d := &mortisedaemon.Daemon{
		Listener:      listener,
		SocketPath:    opts.socketPath,
		ModelID:       in.model,
		ProviderID:    in.provider,
		Workspace:     workspacePath(),
		Branch:        branchFor(workspacePath()),
		UndoStackSize: in.undoStackSize,
		ToolApproval:  in.toolApproval,
		Logger:        logger.With("component", "daemon"),
		Store:         in.store,
	}
	d.SetSession(in.sess)
	return d
}

// setupContext returns a context that cancels when the caller's
// parent context is canceled, or (for top-level production
// invocation) on SIGINT/SIGTERM.
func setupContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent != nil && parent != context.Background() {
		return context.WithCancel(parent)
	}
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
