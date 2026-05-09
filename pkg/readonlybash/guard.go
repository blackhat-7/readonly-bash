package readonlybash

import (
	"fmt"
	"strings"
	"time"
)

type GuardConstraints struct {
	DangerousEnv       map[string]string `json:"dangerousEnv,omitempty"`
	ShellCommandPrefix string            `json:"shellCommandPrefix,omitempty"`
	ShellPath          string            `json:"shellPath,omitempty"`
	ExpectedShellPath  string            `json:"expectedShellPath,omitempty"`
	Host               string            `json:"host,omitempty"`
}

type PrepareRequest struct {
	RequestID          string           `json:"requestID"`
	Cwd                string           `json:"cwd"`
	Command            string           `json:"command"`
	RunnerPath         string           `json:"runnerPath"`
	TrustedShell       string           `json:"trustedShell"`
	TrustedPath        string           `json:"trustedPath"`
	ApprovalDir        string           `json:"approvalDir"`
	ApprovalTTLSeconds int              `json:"approvalTTLSeconds,omitempty"`
	Guard              GuardConstraints `json:"guard,omitempty"`
}

type PrepareResponse struct {
	Action  string `json:"action"`
	Command string `json:"command,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func Prepare(req PrepareRequest) PrepareResponse {
	if req.RequestID == "" || req.Cwd == "" || req.Command == "" || req.RunnerPath == "" || req.ApprovalDir == "" || req.TrustedShell == "" || req.TrustedPath == "" {
		return prepareAsk("missing required prepare field")
	}
	if err := CheckGuard(req.Guard, req.TrustedShell); err != nil {
		return prepareAsk(err.Error())
	}

	classification := Classify(req.Command)
	if classification.Decision != DecisionReadOnly {
		return prepareAsk(classification.Reason)
	}
	ttl := defaultApprovalTTL
	if req.ApprovalTTLSeconds > 0 {
		ttl = time.Duration(req.ApprovalTTLSeconds) * time.Second
	}
	if _, err := CreateApproval(CreateApprovalOptions{
		ApprovalDir:     req.ApprovalDir,
		RequestID:       req.RequestID,
		Cwd:             req.Cwd,
		OriginalCommand: req.Command,
		CommandToRun:    classification.CommandToRun,
		TTL:             ttl,
	}); err != nil {
		return prepareAsk(err.Error())
	}
	return PrepareResponse{Action: "rewrite", Command: req.RunnerPath}
}

func CheckGuard(guard GuardConstraints, trustedShell string) error {
	if guard.ShellCommandPrefix != "" {
		return fmt.Errorf("shell prefix is not empty")
	}
	expectedShell := guard.ExpectedShellPath
	if expectedShell == "" {
		expectedShell = trustedShell
	}
	if expectedShell != "" && guard.ShellPath != expectedShell {
		return fmt.Errorf("untrusted shell path")
	}
	for key, value := range guard.DangerousEnv {
		if value == "" {
			continue
		}
		if key == "BASH_ENV" || key == "ENV" || strings.HasPrefix(key, "BASH_FUNC_") || key == "SHELLOPTS" || key == "BASHOPTS" {
			return fmt.Errorf("dangerous environment variable %s", key)
		}
	}
	return nil
}

func prepareAsk(reason string) PrepareResponse {
	if reason == "" {
		reason = "not auto-approved"
	}
	return PrepareResponse{Action: "ask", Reason: reason}
}
