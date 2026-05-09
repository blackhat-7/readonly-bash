package readonlybash

import (
	"strings"
	"testing"
)

func TestClassifyAllowsReadOnlyCommands(t *testing.T) {
	tests := map[string]string{
		"pwd":                                          "pwd",
		"ls -la docs":                                  "ls -la docs",
		"rg -n -g '*.go' Classify .":                   "rg -n -g '*.go' Classify .",
		"grep -R 'foo.*' file.txt":                     "grep -R 'foo.*' file.txt",
		"find . -name '*.go' -print":                   "find . -name '*.go' -print",
		"git status --short":                           "git status --short",
		"git log --oneline -n 5":                       "git log --oneline -n 5",
		"git ls-files -- -leading":                     "git ls-files -- -leading",
		"git tag --list 'v*'":                          "git tag --list 'v*'",
		"date -u +%s":                                  "date -u +%s",
		"node -v":                                      "node -v",
		"python --version":                             "python --version",
		"python3 --version":                            "python3 --version",
		"echo hello && pwd":                            "echo hello && pwd",
		"git diff --stat -- 'a file'":                  "git diff --no-ext-diff --no-textconv --stat -- 'a file'",
		"git diff --name-only -- '-leading'":           "git diff --no-ext-diff --no-textconv --name-only -- -leading",
		"git diff --stat -- \"it's.go\"":               "git diff --no-ext-diff --no-textconv --stat -- 'it'\\''s.go'",
		"git diff --numstat -- 'semi;colon'":           "git diff --no-ext-diff --no-textconv --numstat -- 'semi;colon'",
		"git diff --stat origin/main...HEAD":           "git diff --no-ext-diff --no-textconv --stat origin/main...HEAD",
		"git diff --name-status origin/main..HEAD":     "git diff --no-ext-diff --no-textconv --name-status origin/main..HEAD",
		"git diff --shortstat HEAD~1 HEAD":             "git diff --no-ext-diff --no-textconv --shortstat HEAD~1 HEAD",
		"git diff --name-only origin/main...HEAD -- .": "git diff --no-ext-diff --no-textconv --name-only origin/main...HEAD -- .",
	}
	for command, want := range tests {
		t.Run(command, func(t *testing.T) {
			got := Classify(command)
			if got.Decision != DecisionReadOnly {
				t.Fatalf("decision=%s reason=%s", got.Decision, got.Reason)
			}
			if got.CommandToRun != want {
				t.Fatalf("commandToRun=%q want %q", got.CommandToRun, want)
			}
		})
	}
}

func TestClassifyRejectsUnsafeCommands(t *testing.T) {
	tests := []string{
		"rm file",
		"touch file",
		"mkdir dir",
		"cp a b",
		"mv a b",
		"sed -i s/a/b/ file",
		"curl https://example.com",
		"pnpm install",
		"docker ps",
		"gh pr list",
		"wget https://example.com",
		"ssh host",
		"scp a b",
		"sftp host",
		"rsync a b",
		"nc host 80",
		"ncat host 80",
		"telnet host",
		"ftp host",
		"dig example.com",
		"nslookup example.com",
		"host example.com",
		"ping 127.0.0.1",
		"npm install",
		"yarn install",
		"bun install",
		"pip install x",
		"brew install x",
		"kubectl get pods",
		"aws s3 ls",
		"gcloud projects list",
		"az group list",
		"FOO=bar pwd",
		"/bin/ls",
		"echo $(whoami)",
		"echo ${HOME}",
		"echo `whoami`",
		"echo $HOME",
		"cat < file",
		"cat file > out",
		"pwd; whoami",
		"pwd # comment",
		"pwd &",
		"cat <(echo x)",
		"echo 'a\nb'",
		"echo \"a\nb\"",
		"echo 'unterminated",
		"ls *",
		"find * -print",
		"tail *",
		"echo {a,b}",
		"echo ?",
		"echo [abc]",
		"git diff *",
		"cat '-n'",
		"tail '-f' file",
		"find '-delete' -print",
		"find . -exec echo {} ;",
		"find . -delete",
		"git diff",
		"git diff -- file",
		"git diff --stat --ext-diff",
		"git diff --stat HEAD main other",
		"git diff --stat --color",
		"git diff --stat --output out",
		"git diff --stat --textconv",
		"git diff --stat --no-index a b",
		"git diff --stat --config-env=x=y",
		"git diff --stat -c",
		"git diff --stat --unknown",
		"git diff --stat -leading",
		"git diff --stat :/magic",
		"git diff --stat HEAD@{1}",
		"git diff --stat 'bad;rev'",
		"git log --stat",
		"git log --numstat",
		"git log --name-only",
		"git log --name-status",
		"git log --summary",
		"git log --raw",
		"git log -p",
		"git log --patch",
		"git log --ext-diff",
		"git log --textconv",
		"git log --unknown",
		"git tag",
		"git push",
		"git commit -m msg",
		"python -v",
		"python3 -v",
		"printf -v PATH . && ls",
		"pwd || whoami",
		"pwd | rm file",
	}
	for _, command := range tests {
		t.Run(command, func(t *testing.T) {
			got := Classify(command)
			if got.Decision != DecisionAsk {
				t.Fatalf("command was auto-approved as %q", got.CommandToRun)
			}
		})
	}
}

func TestClassifyAllowsSafePipeline(t *testing.T) {
	got := Classify("printf '%s\\n' hello | grep hello")
	if got.Decision != DecisionReadOnly {
		t.Fatalf("safe pipeline rejected: %+v", got)
	}
}

func TestShellQuote(t *testing.T) {
	tests := map[string]string{
		"simple":     "simple",
		"a file":     "'a file'",
		"it's":       "'it'\\''s'",
		"semi;colon": "'semi;colon'",
		"-leading":   "-leading",
		"":           "''",
	}
	for in, want := range tests {
		if got := ShellQuote(in); got != want {
			t.Fatalf("ShellQuote(%q)=%q want %q", in, got, want)
		}
	}
}

func TestExactRunnerPermissionShape(t *testing.T) {
	runner := "/nix/store/abc-readonly-bash/bin/readonly-bash-runner"
	allowed := func(command string) bool { return command == runner }
	bad := []string{
		runner + " ; rm -rf /",
		runner + " && rm file",
		runner + " | sh",
		runner + " > out",
		runner + " < in",
		runner + " $(rm file)",
		runner + " `rm file`",
		runner + " # comment",
		"FOO=bar " + runner,
		runner + " arg",
		runner + " ",
	}
	if !allowed(runner) {
		t.Fatal("exact runner command should match")
	}
	for _, command := range bad {
		if allowed(command) {
			t.Fatalf("suffix/prefix form matched exact runner: %q", command)
		}
		if !strings.Contains(command, runner) {
			t.Fatalf("test bug: %q", command)
		}
	}
}
