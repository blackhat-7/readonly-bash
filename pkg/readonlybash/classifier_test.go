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
		"printf '%n' GIT_NO_LAZY_FETCH && git log --oneline -n 1",
		"printf '%n' GIT_OPTIONAL_LOCKS && git status --short",
		"printf '%n' GIT_TERMINAL_PROMPT && git ls-files",
		"printf '%n' GIT_CONFIG_COUNT && git status --short",
		"printf '%n' PAGER && git log --oneline -n 1",
		"printf '%n' GIT_PAGER && git log --oneline -n 1",
		"printf '%10n' GIT_NO_LAZY_FETCH",
		"printf '%.*n' 1 GIT_NO_LAZY_FETCH",
		"printf '%ln' GIT_NO_LAZY_FETCH && git log --oneline -n 1",
		"printf '%lln' PATH && ls",
		"printf '%hhn' PATH && ls",
		"printf '%Ln' GIT_CONFIG_COUNT && git status --short",
		"printf '%zn' PATH && ls",
		"printf '%jn' PATH && ls",
		"printf '%tn' PATH && ls",
		"printf '%*ln' 1 PATH && ls",
		"printf '%.*ln' 1 PATH && ls",
		"printf \"%'n\" PATH && ls",
		"printf \"x%'n\" PATH && ls",
		"printf \"xx%'n\" PATH && cat README.md",
		"printf \"%'n\" PATH && git status --short",
		"printf \"%'n\" PATH && node -v",
		"printf \"%'n\" PATH && python --version",
		"printf \"%'n\" PATH && python3 --version",
		"printf \"%'n\" PATH && rg readonly .",
		"printf \"%'n\" PATH && find . -maxdepth 1 -print",
		"printf \"%'n\" PATH && file README.md",
		"printf \"%'n\" PATH && du -sh .",
		"printf \"%'n\" PATH && df -h .",
		"printf \"%'n\" PATH && head -n 1 README.md",
		"printf \"%'n\" PATH && tail -n 1 README.md",
		"printf \"%'n\" PATH && wc -l README.md",
		"printf \"%'10n\" PATH && ls",
		"printf \"%+'n\" PATH && ls",
		"printf \"%0'n\" PATH && ls",
		"printf \"%-'n\" PATH && ls",
		"printf \"%#'n\" PATH && ls",
		"printf \"% 'n\" PATH && ls",
		"printf \"%'ln\" PATH && ls",
		"printf \"%'lln\" PATH && ls",
		"printf \"%'hhn\" PATH && ls",
		"printf \"%'zn\" PATH && ls",
		"printf \"%'jn\" PATH && ls",
		"printf \"%'tn\" PATH && ls",
		"printf \"%'Ln\" PATH && ls",
		"printf \"%'*n\" 1 PATH && ls",
		"printf \"%'.*n\" 1 PATH && ls",
		"printf \"%'n\" GIT_CONFIG_COUNT && git status --short",
		"printf \"%'n\" GIT_NO_LAZY_FETCH && git log --oneline --all -n 1",
		"printf \"%'n\" GIT_CONFIG_COUNT && printf \"%'n\" HOME && git status --short",
		"printf '%b' '\\e]52;c;SGVsbG8=\\a'",
		"printf '\\e]52;c;SGVsbG8=\\a'",
		"printf '\\033]52;c;SGVsbG8=\\007'",
		"printf '\\e]0;pwned\\a'",
		"printf '\\e[2J\\e[H'",
		"printf '\\e[?1049h'",
		"printf '\\e[5ihello\\e[4i'",
		"echo -e '\\e]52;c;SGVsbG8=\\a'",
		"echo -e '\\033]0;pwned\\007'",
		"echo -e '\\e[?1049h'",
		"cat '~/.ssh/id_rsa'",
		"head -n 5 '~/.ssh/id_rsa'",
		"tail -n 5 '~/.zsh_history'",
		"wc -l '~/.zsh_history'",
		"file '~/.ssh/id_rsa'",
		"du -sh '~/.cache'",
		"grep -R PRIVATE '~/.ssh'",
		"rg --files '~/.ssh'",
		"find '~/.ssh' -maxdepth 1 -print",
		"ls '~root/.ssh'",
		"cat /dev/tty",
		"cat /dev/random",
		"head -c 1 /dev/random",
		"wc -c /dev/tty",
		"grep x /dev/tty",
		"cat /dev/watchdog",
		"head -c 1 /dev/input/event0",
		"cat /proc/kmsg",
		"find /net -maxdepth 2 -print",
		"ls -R /net",
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
