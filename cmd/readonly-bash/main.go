package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	readonlybash "github.com/blackhat-7/readonly-bash/pkg/readonlybash"
)

var defaultConfigPath string

type classifyRequest struct {
	Command string `json:"command"`
	Cwd     string `json:"cwd,omitempty"`
}

func main() {
	if filepath.Base(os.Args[0]) == "readonly-bash-runner" {
		os.Exit(runDefaultConfig())
	}
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "classify":
		runClassify()
	case "prepare":
		runPrepare()
	case "run":
		runManual()
	default:
		usage()
	}
}

func runClassify() {
	var req classifyRequest
	decodeStdin(&req)
	writeJSON(readonlybash.Classify(req.Command))
}

func runPrepare() {
	var req readonlybash.PrepareRequest
	decodeStdin(&req)
	writeJSON(readonlybash.Prepare(req))
}

func runManual() {
	if len(os.Args) != 4 || os.Args[2] != "--config" {
		usage()
	}
	cfg, err := readonlybash.LoadRunnerConfig(os.Args[3])
	if err != nil {
		fatal(err)
	}
	code, err := runWithConfig(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(code)
}

func runDefaultConfig() int {
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "readonly-bash-runner accepts no arguments")
		return 2
	}
	if defaultConfigPath == "" {
		fmt.Fprintln(os.Stderr, "readonly-bash-runner default config path is not set")
		return 2
	}
	cfg, err := readonlybash.LoadRunnerConfig(defaultConfigPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	code, err := runWithConfig(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	return code
}

func runWithConfig(cfg readonlybash.RunnerConfig) (int, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return 2, err
	}
	result, err := readonlybash.RunApproved(context.Background(), readonlybash.RunOptions{
		Config: cfg,
		Cwd:    cwd,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	return result.ExitCode, err
}

func decodeStdin(dst any) {
	dec := json.NewDecoder(os.Stdin)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		fatal(err)
	}
}

func writeJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(v); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: readonly-bash classify|prepare|run --config <path>")
	os.Exit(2)
}
