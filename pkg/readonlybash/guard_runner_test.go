package readonlybash

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckGuard(t *testing.T) {
	trustedShell := "/bin/bash"
	good := GuardConstraints{ShellPath: trustedShell, ExpectedShellPath: trustedShell}
	if err := CheckGuard(good, trustedShell); err != nil {
		t.Fatalf("good guard failed: %v", err)
	}
	bad := []GuardConstraints{
		{ShellPath: trustedShell, ExpectedShellPath: trustedShell, ShellCommandPrefix: "source ~/.bashrc"},
		{ShellPath: "/bin/sh", ExpectedShellPath: trustedShell},
		{ShellPath: trustedShell, ExpectedShellPath: trustedShell, DangerousEnv: map[string]string{"BASH_ENV": "/tmp/x"}},
		{ShellPath: trustedShell, ExpectedShellPath: trustedShell, DangerousEnv: map[string]string{"BASH_FUNC_x%%": "() { :; }"}},
		{ShellPath: trustedShell, ExpectedShellPath: trustedShell, DangerousEnv: map[string]string{"SHELLOPTS": "extglob"}},
	}
	for _, guard := range bad {
		if err := CheckGuard(guard, trustedShell); err == nil {
			t.Fatalf("bad guard passed: %+v", guard)
		}
	}
}

func TestPrepareCreatesApproval(t *testing.T) {
	root := t.TempDir()
	approvalDir := filepath.Join(root, "approvals")
	resp := Prepare(PrepareRequest{
		RequestID:    "req",
		Cwd:          root,
		Command:      "pwd",
		RunnerPath:   "/runner",
		TrustedShell: "/bin/bash",
		TrustedPath:  "/bin:/usr/bin",
		ApprovalDir:  approvalDir,
		Guard:        GuardConstraints{ShellPath: "/bin/bash", ExpectedShellPath: "/bin/bash"},
	})
	if resp.Action != "rewrite" || resp.Command != "/runner" {
		t.Fatalf("prepare response=%+v", resp)
	}
	resp = Prepare(PrepareRequest{
		RequestID:    "req2",
		Cwd:          root,
		Command:      "pwd",
		RunnerPath:   "/runner",
		TrustedShell: "/bin/bash",
		TrustedPath:  "/bin:/usr/bin",
		ApprovalDir:  approvalDir,
		Guard:        GuardConstraints{ShellPath: "/bin/bash", ExpectedShellPath: "/bin/bash"},
	})
	if resp.Action != "ask" || !strings.Contains(resp.Reason, ErrApprovalPending.Error()) {
		t.Fatalf("second prepare response=%+v", resp)
	}
}

func TestSanitizeEnv(t *testing.T) {
	env := SanitizeEnv([]string{
		"PATH=/bad",
		"BASH_ENV=/tmp/x",
		"ENV=/tmp/y",
		"BASH_FUNC_x%%=() { :; }",
		"SHELLOPTS=extglob",
		"BASHOPTS=extglob",
		"CDPATH=/tmp",
		"GLOBIGNORE=*",
		"RIPGREP_CONFIG_PATH=/tmp/rg",
		"GIT_EXTERNAL_DIFF=evil",
		"GIT_TRACE=1",
		"GIT_TRACE2_EVENT=/tmp/trace",
		"GIT_CONFIG_PARAMETERS=x",
		"GIT_CONFIG_COUNT=99",
		"GIT_CONFIG_KEY_0=diff.external",
		"GIT_CONFIG_VALUE_0=evil",
		"GIT_SSH=ssh-wrapper",
		"GIT_SSH_COMMAND=ssh-wrapper",
		"KEEP=1",
	}, "/trusted/bin")
	m := envMap(env)
	for _, key := range []string{"BASH_ENV", "ENV", "SHELLOPTS", "BASHOPTS", "CDPATH", "GLOBIGNORE", "RIPGREP_CONFIG_PATH", "GIT_EXTERNAL_DIFF", "GIT_TRACE", "GIT_TRACE2_EVENT", "GIT_CONFIG_PARAMETERS", "GIT_SSH", "GIT_SSH_COMMAND"} {
		if _, ok := m[key]; ok {
			t.Fatalf("%s was not removed", key)
		}
	}
	if m["PATH"] != "/trusted/bin" || m["LC_ALL"] != "C" || m["LANG"] != "C" || m["KEEP"] != "1" {
		t.Fatalf("unexpected env: %+v", m)
	}
	if m["GIT_OPTIONAL_LOCKS"] != "0" || m["GIT_NO_LAZY_FETCH"] != "1" || m["GIT_CONFIG_COUNT"] != "5" {
		t.Fatalf("git hardening missing: %+v", m)
	}
	for _, v := range m {
		if strings.Contains(v, "diff.external") {
			t.Fatalf("diff.external override should not be set: %+v", m)
		}
	}
}

func TestLoadRunnerConfig(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, []byte(`{"approvalDir":"/a","trustedShell":"/bin/bash","trustedPath":"/bin"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadRunnerConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ApprovalDir != "/a" || cfg.TrustedShell != "/bin/bash" || cfg.TrustedPath != "/bin" {
		t.Fatalf("config=%+v", cfg)
	}
	if _, err := LoadRunnerConfig(filepath.Join(root, "missing.json")); err == nil {
		t.Fatal("missing config loaded successfully")
	}
}

func TestRunApprovedSanitizesEnvAndDisablesExpansion(t *testing.T) {
	shell, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	root := t.TempDir()
	approvalDir := filepath.Join(root, "approvals")
	for _, name := range []string{"x", "-delete", "--output=owned", "-f"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bashEnv := filepath.Join(root, "evil-bash-env")
	if err := os.WriteFile(bashEnv, []byte("echo sourced-bash-env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BASH_ENV", bashEnv)

	_, err = CreateApproval(CreateApprovalOptions{
		ApprovalDir:     approvalDir,
		RequestID:       "req",
		Cwd:             root,
		OriginalCommand: "test",
		CommandToRun:    `printf '%s|%s|%s' "${BASH_ENV-unset}" * {a,b}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	result, err := RunApproved(context.Background(), RunOptions{
		Config: RunnerConfig{ApprovalDir: approvalDir, TrustedShell: shell, TrustedPath: "/bin:/usr/bin"},
		Cwd:    root,
		Stdout: &stdout,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit=%d", result.ExitCode)
	}
	if got, want := stdout.String(), "unset|*|{a,b}"; got != want {
		t.Fatalf("stdout=%q want %q", got, want)
	}
	if _, err := ClaimApproval(ClaimApprovalOptions{ApprovalDir: approvalDir, Cwd: root}); !errors.Is(err, ErrNoApproval) {
		t.Fatalf("approval should be single-use: %v", err)
	}
}

func TestRunApprovedGitDiffMetadataDoesNotRunTextconv(t *testing.T) {
	shell, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.invalid")
	runGit(t, root, "config", "user.name", "readonly-bash test")
	marker := filepath.Join(root, "textconv-ran")
	script := filepath.Join(root, "textconv.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ran > "+ShellQuote(marker)+"\ncat \"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "config", "diff.evil.textconv", script)
	if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.dat diff=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.dat"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(root, "file.dat"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	classification := Classify("git diff --stat")
	if classification.Decision != DecisionReadOnly {
		t.Fatalf("classification=%+v", classification)
	}
	approvalDir := filepath.Join(root, "approvals")
	_, err = CreateApproval(CreateApprovalOptions{ApprovalDir: approvalDir, RequestID: "req", Cwd: root, OriginalCommand: "git diff --stat", CommandToRun: classification.CommandToRun})
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunApproved(context.Background(), RunOptions{
		Config: RunnerConfig{ApprovalDir: approvalDir, TrustedShell: shell, TrustedPath: filepath.Dir(git) + ":/bin:/usr/bin"},
		Cwd:    root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("git diff exit=%d", result.ExitCode)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("textconv marker stat=%v", err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}
