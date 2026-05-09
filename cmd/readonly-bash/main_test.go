package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	readonlybash "github.com/blackhat-7/readonly-bash/pkg/readonlybash"
)

func TestCLIClassifyJSON(t *testing.T) {
	stdout, stderr, code := runCLI(t, []string{"classify"}, `{"command":"git diff --stat"}`)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr)
	}
	var resp map[string]string
	if err := json.Unmarshal(stdout, &resp); err != nil {
		t.Fatal(err)
	}
	if resp["decision"] != "readonly" || resp["commandToRun"] != "git diff --no-ext-diff --no-textconv --stat" {
		t.Fatalf("response=%s", stdout)
	}
}

func TestCLIPrepareJSON(t *testing.T) {
	root := t.TempDir()
	body := `{"requestID":"req","cwd":` + quoteJSON(root) + `,"command":"pwd","runnerPath":"/runner","trustedShell":"/bin/bash","trustedPath":"/bin:/usr/bin","approvalDir":` + quoteJSON(filepath.Join(root, "approvals")) + `,"guard":{"shellPath":"/bin/bash","expectedShellPath":"/bin/bash"}}`
	stdout, stderr, code := runCLI(t, []string{"prepare"}, body)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr)
	}
	var resp map[string]string
	if err := json.Unmarshal(stdout, &resp); err != nil {
		t.Fatal(err)
	}
	if resp["action"] != "rewrite" || resp["command"] != "/runner" {
		t.Fatalf("response=%s", stdout)
	}
}

func TestCLIInvalidJSONExitsNonzero(t *testing.T) {
	_, _, code := runCLI(t, []string{"classify"}, `{bad`)
	if code == 0 {
		t.Fatal("invalid JSON exited successfully")
	}
}

func TestCLIRunnerModeFailsClosedWithoutBakedConfig(t *testing.T) {
	_, _, code := runCLI(t, []string{"__runner__"}, "")
	if code == 0 {
		t.Fatal("runner mode without baked config exited successfully")
	}
	_, _, code = runCLI(t, []string{"__runner__", "--config", "/tmp/x"}, "")
	if code == 0 {
		t.Fatal("runner mode accepted arguments")
	}
}

func TestCLIRunnerModeRealConfigFailures(t *testing.T) {
	root := t.TempDir()
	configPath := writeRunnerConfig(t, root, filepath.Join(root, "approvals"))
	_, _, code := runCLIWith(t, []string{"__runner__"}, "", root, []string{"READONLY_BASH_DEFAULT_CONFIG=" + configPath})
	if code == 0 {
		t.Fatal("runner without approval exited successfully")
	}
	_, _, code = runCLIWith(t, []string{"__runner__", "--config", configPath}, "", root, []string{"READONLY_BASH_DEFAULT_CONFIG=" + configPath})
	if code == 0 {
		t.Fatal("runner accepted excess args")
	}

	other := filepath.Join(root, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := readonlybash.CreateApproval(readonlybash.CreateApprovalOptions{ApprovalDir: filepath.Join(root, "approvals"), RequestID: "wrong-cwd", Cwd: other, OriginalCommand: "pwd", CommandToRun: "pwd"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, code = runCLIWith(t, []string{"__runner__"}, "", root, []string{"READONLY_BASH_DEFAULT_CONFIG=" + configPath})
	if code == 0 {
		t.Fatal("runner accepted approval for another cwd")
	}

	if _, err := readonlybash.ClaimApproval(readonlybash.ClaimApprovalOptions{ApprovalDir: filepath.Join(root, "approvals"), Cwd: other}); err != nil {
		t.Fatal(err)
	}
	_, err = readonlybash.CreateApproval(readonlybash.CreateApprovalOptions{ApprovalDir: filepath.Join(root, "approvals"), RequestID: "expired", Cwd: root, OriginalCommand: "pwd", CommandToRun: "pwd", TTL: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	_, _, code = runCLIWith(t, []string{"__runner__"}, "", root, []string{"READONLY_BASH_DEFAULT_CONFIG=" + configPath})
	if code == 0 {
		t.Fatal("runner accepted expired approval")
	}
}

func TestCLIRunnerModeIgnoresEnvConfigOverride(t *testing.T) {
	root := t.TempDir()
	defaultConfig := writeRunnerConfig(t, root, filepath.Join(root, "default-approvals"))
	altConfig := writeRunnerConfig(t, root, filepath.Join(root, "alt-approvals"))
	_, err := readonlybash.CreateApproval(readonlybash.CreateApprovalOptions{ApprovalDir: filepath.Join(root, "alt-approvals"), RequestID: "alt", Cwd: root, OriginalCommand: "pwd", CommandToRun: "pwd"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, code := runCLIWith(t, []string{"__runner__"}, "", root, []string{
		"READONLY_BASH_DEFAULT_CONFIG=" + defaultConfig,
		"READONLY_BASH_CONFIG=" + altConfig,
	})
	if code == 0 {
		t.Fatal("runner used env config override")
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("READONLY_BASH_HELPER") != "1" {
		return
	}
	if p := os.Getenv("READONLY_BASH_DEFAULT_CONFIG"); p != "" {
		defaultConfigPath = p
	}
	for i, arg := range os.Args {
		if arg == "--" {
			args := os.Args[i+1:]
			if len(args) > 0 && args[0] == "__runner__" {
				os.Args = append([]string{"readonly-bash-runner"}, args[1:]...)
			} else {
				os.Args = append([]string{"readonly-bash"}, args...)
			}
			main()
			os.Exit(0)
		}
	}
	os.Exit(2)
}

func runCLI(t *testing.T, args []string, stdin string) ([]byte, string, int) {
	return runCLIWith(t, args, stdin, "", nil)
}

func runCLIWith(t *testing.T, args []string, stdin string, cwd string, env []string) ([]byte, string, int) {
	t.Helper()
	cmdArgs := append([]string{"-test.run=TestHelperProcess", "--"}, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Env = append(os.Environ(), append([]string{"READONLY_BASH_HELPER=1"}, env...)...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), stderr.String(), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return stdout.Bytes(), stderr.String(), exitErr.ExitCode()
	}
	t.Fatalf("run helper: %v", err)
	return nil, "", 1
}

func writeRunnerConfig(t *testing.T, root, approvalDir string) string {
	t.Helper()
	configPath := filepath.Join(root, filepath.Base(approvalDir)+".json")
	body := map[string]string{
		"approvalDir":  approvalDir,
		"trustedShell": "/bin/sh",
		"trustedPath":  "/bin:/usr/bin",
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func quoteJSON(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}
