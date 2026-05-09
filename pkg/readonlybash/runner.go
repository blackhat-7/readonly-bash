package readonlybash

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

type RunnerConfig struct {
	RunnerPath   string `json:"runnerPath"`
	ApprovalDir  string `json:"approvalDir"`
	TrustedShell string `json:"trustedShell"`
	TrustedPath  string `json:"trustedPath"`
}

type RunOptions struct {
	Config RunnerConfig
	Cwd    string
	Stdout io.Writer
	Stderr io.Writer
}

type RunResult struct {
	ExitCode int
}

func LoadRunnerConfig(path string) (RunnerConfig, error) {
	if path == "" {
		return RunnerConfig{}, errors.New("config path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return RunnerConfig{}, err
	}
	var cfg RunnerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return RunnerConfig{}, err
	}
	if cfg.ApprovalDir == "" || cfg.TrustedShell == "" || cfg.TrustedPath == "" {
		return RunnerConfig{}, errors.New("runner config missing required field")
	}
	return cfg, nil
}

func RunApproved(ctx context.Context, opts RunOptions) (RunResult, error) {
	if opts.Cwd == "" {
		return RunResult{ExitCode: 2}, errors.New("cwd is required")
	}
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	approval, err := ClaimApproval(ClaimApprovalOptions{ApprovalDir: opts.Config.ApprovalDir, Cwd: opts.Cwd})
	if err != nil {
		return RunResult{ExitCode: 2}, err
	}
	canonicalCwd, err := CanonicalCwd(opts.Cwd)
	if err != nil {
		return RunResult{ExitCode: 2}, err
	}

	cmd := exec.CommandContext(ctx, opts.Config.TrustedShell, "-c", "set -f; set +B; "+approval.CommandToRun)
	cmd.Dir = canonicalCwd
	cmd.Env = SanitizeEnv(os.Environ(), opts.Config.TrustedPath)
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return RunResult{ExitCode: exitCode(exitErr.ProcessState)}, nil
		}
		return RunResult{ExitCode: 127}, err
	}
	return RunResult{ExitCode: 0}, nil
}

func SanitizeEnv(env []string, trustedPath string) []string {
	out := make([]string, 0, len(env)+16)
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || dropEnvKey(key) || strings.HasPrefix(key, "GIT_TRACE") || strings.HasPrefix(key, "GIT_TRACE2") || strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_") {
			continue
		}
		out = append(out, entry)
	}
	out = setEnv(out, "PATH", trustedPath)
	out = setEnv(out, "LC_ALL", "C")
	out = setEnv(out, "LANG", "C")
	out = setEnv(out, "GIT_OPTIONAL_LOCKS", "0")
	out = setEnv(out, "GIT_NO_LAZY_FETCH", "1")
	out = setEnv(out, "GIT_PAGER", "cat")
	out = setEnv(out, "PAGER", "cat")
	out = setEnv(out, "GIT_TERMINAL_PROMPT", "0")
	out = setEnv(out, "GIT_CONFIG_COUNT", "5")
	out = setEnv(out, "GIT_CONFIG_KEY_0", "core.pager")
	out = setEnv(out, "GIT_CONFIG_VALUE_0", "cat")
	out = setEnv(out, "GIT_CONFIG_KEY_1", "core.fsmonitor")
	out = setEnv(out, "GIT_CONFIG_VALUE_1", "false")
	out = setEnv(out, "GIT_CONFIG_KEY_2", "core.untrackedCache")
	out = setEnv(out, "GIT_CONFIG_VALUE_2", "false")
	out = setEnv(out, "GIT_CONFIG_KEY_3", "log.showSignature")
	out = setEnv(out, "GIT_CONFIG_VALUE_3", "false")
	out = setEnv(out, "GIT_CONFIG_KEY_4", "interactive.diffFilter")
	out = setEnv(out, "GIT_CONFIG_VALUE_4", "")
	return out
}

func dropEnvKey(key string) bool {
	if strings.HasPrefix(key, "BASH_FUNC_") {
		return true
	}
	switch key {
	case "BASH_ENV", "ENV", "SHELLOPTS", "BASHOPTS", "CDPATH", "GLOBIGNORE", "RIPGREP_CONFIG_PATH",
		"GIT_EXTERNAL_DIFF", "GIT_CONFIG_PARAMETERS", "GIT_SSH", "GIT_SSH_COMMAND", "GIT_CONFIG_COUNT":
		return true
	default:
		return false
	}
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	filtered := env[:0]
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, prefix+value)
}

func exitCode(state *os.ProcessState) int {
	if state == nil {
		return 127
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok {
		if status.Signaled() {
			return 128 + int(status.Signal())
		}
		return status.ExitStatus()
	}
	code := state.ExitCode()
	if code >= 0 {
		return code
	}
	return 127
}
