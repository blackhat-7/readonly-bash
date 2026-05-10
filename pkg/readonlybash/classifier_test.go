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
		"git log --oneline -5":                         "git log --oneline -5",
		"git ls-files -- -leading":                     "git ls-files -- -leading",
		"git tag --list 'v*'":                          "git tag --list 'v*'",
		"date -u +%s":                                  "date -u +%s",
		"node -v":                                      "node -v",
		"python --version":                             "python --version",
		"python3 --version":                            "python3 --version",
		"echo hello && pwd":                            "echo hello && pwd",
		"pwd; whoami":                                  "pwd ; whoami",
		"pwd || whoami":                                "pwd || whoami",
		"pwd && ls | sort ; true":                      "pwd && ls | sort ; true",
		"find . -name '*.go' -o -name '*.md' -print":   "find . -name '*.go' -o -name '*.md' -print",
		"find . -type f -a -name '*.go'":               "find . -type f -a -name '*.go'",
		"find missing 2>/dev/null | head -20":          "find missing 2>/dev/null | head -n 20",
		"find missing 2>/dev/null\nhead -20 README.md": "find missing 2>/dev/null ; head -n 20 README.md",
		"command -v go":                                "command -v go",
		"sort -u README.md":                            "sort -u README.md",
		"head -20 README.md":                           "head -n 20 README.md",
		"tail -20 README.md":                           "tail -n 20 README.md",
		"nl -ba example.txt":                           "nl -ba example.txt",
		"sed -n '1,220p' README.md":                    "sed -n 1,220p README.md",
		"nl -ba src/index.ts | sed -n '1460,1730p'":    "nl -ba src/index.ts | sed -n 1460,1730p",
		"cd project && nl -ba ai-harnesses/readonly-bash-classifier.js | sed -n '1,220p'":                                           "cd project && nl -ba ai-harnesses/readonly-bash-classifier.js | sed -n 1,220p",
		"git status --short && git log --oneline -5":                                                                                "git status --short && git log --oneline -5",
		"git diff HEAD --stat && git diff HEAD":                                                                                     "git diff --no-ext-diff --no-textconv HEAD --stat && git diff --no-ext-diff --no-textconv HEAD",
		"cd project && git status --short":                                                                                          "cd project && git status --short",
		"cd project && git status --short && git diff --stat HEAD && git diff --numstat HEAD":                                       "cd project && git status --short && git diff --no-ext-diff --no-textconv --stat HEAD && git diff --no-ext-diff --no-textconv --numstat HEAD",
		"echo 'Repo: project'\ngit status --porcelain\ngit diff --stat HEAD":                                                        "echo 'Repo: project' ; git status --porcelain ; git diff --no-ext-diff --no-textconv --stat HEAD",
		"cd project && git diff HEAD --stat && git diff HEAD -- pkg/readonlybash/classifier.go pkg/readonlybash/classifier_test.go": "cd project && git diff --no-ext-diff --no-textconv HEAD --stat && git diff --no-ext-diff --no-textconv HEAD -- pkg/readonlybash/classifier.go pkg/readonlybash/classifier_test.go",
		"cd project && nl -ba pkg/readonlybash/classifier.go | sed -n '1100,1185p' && printf '\n--- tests 1-220 ---\n' && nl -ba pkg/readonlybash/classifier_test.go | sed -n '1,220p' && printf '\n--- tests 220-360 ---\n' && nl -ba pkg/readonlybash/classifier_test.go | sed -n '220,360p'": "cd project && nl -ba pkg/readonlybash/classifier.go | sed -n 1100,1185p && printf '\n--- tests 1-220 ---\n' && nl -ba pkg/readonlybash/classifier_test.go | sed -n 1,220p && printf '\n--- tests 220-360 ---\n' && nl -ba pkg/readonlybash/classifier_test.go | sed -n 220,360p",
		"git diff HEAD": "git diff --no-ext-diff --no-textconv HEAD",
		"git diff pkg/readonlybash/classifier.go": "git diff --no-ext-diff --no-textconv pkg/readonlybash/classifier.go",
		"git diff .": "git diff --no-ext-diff --no-textconv .",
		"git diff -- pkg/readonlybash/classifier.go":                                          "git diff --no-ext-diff --no-textconv -- pkg/readonlybash/classifier.go",
		"git diff HEAD -- pkg/readonlybash/classifier.go":                                     "git diff --no-ext-diff --no-textconv HEAD -- pkg/readonlybash/classifier.go",
		"git diff HEAD -- pkg/readonlybash/classifier.go pkg/readonlybash/classifier_test.go": "git diff --no-ext-diff --no-textconv HEAD -- pkg/readonlybash/classifier.go pkg/readonlybash/classifier_test.go",
		"git diff --stat -- 'a file'":                                                         "git diff --no-ext-diff --no-textconv --stat -- 'a file'",
		"git diff --name-only -- '-leading'":                                                  "git diff --no-ext-diff --no-textconv --name-only -- -leading",
		"git diff --stat -- \"it's.go\"":                                                      "git diff --no-ext-diff --no-textconv --stat -- 'it'\\''s.go'",
		"git diff --numstat -- 'semi;colon'":                                                  "git diff --no-ext-diff --no-textconv --numstat -- 'semi;colon'",
		"git diff --stat origin/main...HEAD":                                                  "git diff --no-ext-diff --no-textconv --stat origin/main...HEAD",
		"git diff --name-status origin/main..HEAD":                                            "git diff --no-ext-diff --no-textconv --name-status origin/main..HEAD",
		"git diff --shortstat HEAD~1 HEAD":                                                    "git diff --no-ext-diff --no-textconv --shortstat HEAD~1 HEAD",
		"git diff --name-only origin/main...HEAD -- .":                                        "git diff --no-ext-diff --no-textconv --name-only origin/main...HEAD -- .",
		"git diff":                     "git diff --no-ext-diff --no-textconv",
		"git diff --cached":            "git diff --no-ext-diff --no-textconv --cached",
		"git -C /repo diff":            "git -C /repo diff --no-ext-diff --no-textconv",
		"git -C a -C b status --short": "git -C a -C b status --short",
		"git -C /Users/illusion/dotfiles log --oneline --graph --all -15": "git -C /Users/illusion/dotfiles log --oneline --graph --all -15",
		"git show HEAD:README.md": "git show --no-ext-diff --no-textconv HEAD:README.md",
		"git show --no-ext-diff --no-textconv HEAD:README.md >/dev/null && echo ok": "git show --no-ext-diff --no-textconv HEAD:README.md >/dev/null && echo ok",
		"git grep -n 'foo|bar' HEAD -- src":                                         "git grep -n 'foo|bar' HEAD -- src",
		"git ls-tree -r --name-only HEAD -- src":                                    "git ls-tree -r --name-only HEAD -- src",
		"git merge-base HEAD MERGE_HEAD":                                            "git merge-base HEAD MERGE_HEAD",
		"pwd && find . -maxdepth 2 -type f | sed 's#^./##' | sort | head -200":      "pwd && find . -maxdepth 2 -type f | sed 's#^./##' | sort | head -n 200",
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

func TestClassifyAllowsRequiredRepoInspectionCommands(t *testing.T) {
	tests := map[string]string{
		"git branch -a":                       "git branch -a",
		"git log --oneline -20":               "git log --oneline -20",
		"git remote -v":                       "git remote -v",
		"git log --stat --oneline -5":         "git log --no-ext-diff --no-textconv --stat --oneline -5",
		"git shortlog -sne | head -10":        "git shortlog -sne | head -n 10",
		"git log --all --oneline --graph -25": "git log --all --oneline --graph -25",
		"find . -maxdepth 3 -type f -not -path './.git/*' | head -80": "find . -maxdepth 3 -type f -not -path './.git/*' | head -n 80",
		"git diff master origin/nix-rpi --stat | head -30":            "git diff --no-ext-diff --no-textconv master origin/nix-rpi --stat | head -n 30",
		"git log --all --oneline --decorate --graph -30":              "git log --all --oneline --decorate --graph -30",
		"git rev-list --count --all":                                  "git rev-list --count --all",
		"git tag -l":                                                  "git tag -l",
		"git log --format=\"%h %s\" --all | wc -l":                    "git log '--format=%h %s' --all | wc -l",
		"find . -maxdepth 4 -type f -not -path './.git/*' -not -path './.direnv/*' -not -path './result/*' | sort": "find . -maxdepth 4 -type f -not -path './.git/*' -not -path './.direnv/*' -not -path './result/*' | sort",
		"git log --all --format=\"%h %ad %s\" --date=short | head -30":                                             "git log --all '--format=%h %ad %s' --date=short | head -n 30",
		"git reflog | head -20":                        "git reflog | head -n 20",
		"git log --all --oneline --reverse | head -20": "git log --all --oneline --reverse | head -n 20",
		"git log --oneline --grep=\"feat\" | wc -l && git log --oneline --grep=\"fix\" | wc -l && git log --oneline --grep=\"refactor\" | wc -l": "git log --oneline --grep=feat | wc -l && git log --oneline --grep=fix | wc -l && git log --oneline --grep=refactor | wc -l",
		"git stash list": "git stash list",
		"git log --all --format=\"%an\" | sort | uniq -c | sort -rn | head -10": "git log --all --format=%an | sort | uniq -c | sort -rn | head -n 10",
		"pwd && ls -la && (git rev-parse --show-toplevel 2>/dev/null || true) && find . -maxdepth 2 -type d -name .git -prune -o -maxdepth 2 -type f \\( -name 'package.json' -o -name 'pyproject.toml' -o -name 'go.mod' -o -name 'Cargo.toml' -o -name 'flake.nix' -o -name '*.ts' -o -name '*.js' -o -name '*.py' \\) -print | head -80": "pwd && ls -la && ( git rev-parse --show-toplevel 2>/dev/null || true ) && find . -maxdepth 2 -type d -name .git -prune -o -maxdepth 2 -type f '(' -name package.json -o -name pyproject.toml -o -name go.mod -o -name Cargo.toml -o -name flake.nix -o -name '*.ts' -o -name '*.js' -o -name '*.py' ')' -print | head -n 80",
		"git -C \"$HOME/Documents/projects/readonly-bash\" status --short --branch && find \"$HOME/Documents/projects/readonly-bash\" -maxdepth 3 -type f -not -path '*/.git/*' | sort | head -120":                                                                                                                                         "git -C /Users/illusion/Documents/projects/readonly-bash status --short --branch && find /Users/illusion/Documents/projects/readonly-bash -maxdepth 3 -type f -not -path '*/.git/*' | sort | head -n 120",
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

func TestClassifyAllowsCommonExplorationCommands(t *testing.T) {
	tests := map[string]string{
		"false":                                              "false",
		"stat -f '%z' README.md":                             "stat -f %z README.md",
		"readlink -f path":                                   "readlink -f path",
		"realpath README.md":                                 "realpath README.md",
		"tree -L 2 -a .":                                     "tree -L 2 -a .",
		"cut -d ':' -f 1 /etc/passwd":                        "cut -d : -f 1 /etc/passwd",
		"tr a-z A-Z":                                         "tr a-z A-Z",
		"basename src/main.go":                               "basename src/main.go",
		"dirname src/main.go":                                "dirname src/main.go",
		"test -f README.md":                                  "test -f README.md",
		"jq -r '.name' package.json":                         "jq -r .name package.json",
		"git describe --tags --always":                       "git describe --tags --always",
		"git show-ref --heads":                               "git show-ref --heads",
		"git symbolic-ref --short HEAD":                      "git symbolic-ref --short HEAD",
		"git worktree list --porcelain":                      "git worktree list --porcelain",
		"git submodule status --recursive":                   "git submodule status --recursive",
		"git count-objects -vH":                              "git count-objects -vH",
		"git cat-file -t HEAD":                               "git cat-file -t HEAD",
		"git blame -L 1,10 README.md":                        "git blame --no-textconv -L 1,10 README.md",
		"git -C \"$HOME\" status --short":                    "git -C /Users/illusion status --short",
		"ls \"$HOME\"":                                       "ls /Users/illusion",
		"cd $HOME && pwd":                                    "cd /Users/illusion && pwd",
		"pwd > /dev/null && echo ok":                         "pwd >/dev/null && echo ok",
		"pwd 1> /dev/null && echo ok":                        "pwd >/dev/null && echo ok",
		"git rev-parse --show-toplevel 2> /dev/null || true": "git rev-parse --show-toplevel 2>/dev/null || true",
		"echo -- -n":                                         "echo -- -n",
		"nl":                                                 "nl",
		"sed 's/a/b/I' file.txt":                             "sed s/a/b/I file.txt",
		"sed 's/a/b/2g' file.txt":                            "sed s/a/b/2g file.txt",
		"head -n -1 README.md":                               "head -n -1 README.md",
		"sort -R --debug README.md":                          "sort -R --debug README.md",
		"sort --buffer-size=1M README.md":                    "sort --buffer-size=1M README.md",
		"find . -writable -o -executable -print":             "find . -writable -o -executable -print",
		"find . -newer README.md -samefile go.mod -print":    "find . -newer README.md -samefile go.mod -print",
		"find . -inum 1 -links +1 -user root -group staff -perm -u+w -printf '%p\\n'": "find . -inum 1 -links +1 -user root -group staff -perm -u+w -printf '%p\\n'",
		"git stash show":                                  "git stash show --no-ext-diff --no-textconv",
		"git archive --list":                              "git archive --list",
		"git verify-commit HEAD":                          "git verify-commit HEAD",
		"git verify-tag v1.0":                             "git verify-tag v1.0",
		"git var -l":                                      "git var -l",
		"git config --list":                               "git config --list",
		"git help status":                                 "git help status",
		"git bundle list-heads bundle.file":               "git bundle list-heads bundle.file",
		"git notes list":                                  "git notes list",
		"git rerere status":                               "git rerere status",
		"jq -n '{\"ok\":true}'":                           "jq -n '{\"ok\":true}'",
		"jq --arg name value '.name' file.json":           "jq --arg name value .name file.json",
		"jq --slurpfile docs docs.json '.docs' file.json": "jq --slurpfile docs docs.json .docs file.json",
		"jq --from-file filter.jq file.json":              "jq --from-file filter.jq file.json",
		"date -d '2 days ago' +%s":                        "date -d '2 days ago' +%s",
		"date -r README.md +%s":                           "date -r README.md +%s",
		"date -Iseconds":                                  "date -Iseconds",
		"test -w README.md":                               "test -w README.md",
		"tr -t a-z A-Z":                                   "tr -t a-z A-Z",
		"cut --output-delimiter=:: -f 1 file.tsv":         "cut --output-delimiter=:: -f 1 file.tsv",
		"du --apparent-size --exclude='*.git' .":          "du --apparent-size '--exclude=*.git' .",
		"tree --fromfile file-list.txt":                   "tree --fromfile file-list.txt",
		"wc --files0-from files.list":                     "wc --files0-from files.list",
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
		"sed -e '1,2p' file",
		"sed -n '1,2w out' file",
		"sed -n '1,2e sh' file",
		"sed -n '1,2p' '-leading'",
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
		"pwd; rm file",
		"pwd || rm file",
		"pwd\nrm file",
		"cd docs; git status --short",
		"cd docs || git status --short",
		"cd - && git status --short",
		"cd /dev && git status --short",
		"cd docs && git push",
		"cat missing 2>err",
		"sort -o out README.md",
		"command -v /bin/ls",
		"command -v FOO=bar",
		"nl -v 1 example.txt",
		"nl -ba /dev/tty",
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
		"find . -o -print",
		"find . -name '*.go' -o",
		"find . -maxdepth 1 -o -name '*.go'",
		"find . -name '*.go' -o README.md",
		"git diff --stat --",
		"git diff --name-only --",
		"git diff HEAD --",
		"git diff /dev/tty",
		"git diff ~/.ssh/id_rsa",
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
		"git log --oneline -abc",
		"git tag",
		"git push",
		"git commit -m msg",
		"git -C /repo push",
		"git -c core.pager=cat status --short",
		"git show --output out HEAD",
		"git ls-tree HEAD src",
		"git ls-tree HEAD --help",
		"git ls-tree HEAD --unknown",
		"git ls-tree HEAD --",
		"pwd >/tmp/out",
		"pwd > out",
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
		"find . -name '*.go' -o -delete",
		"find . -fprint out",
		"find . -printf '%p\n'",
		"find . -name '*.go' -o -exec sh -c pwd ;",
		"(rm file)",
		"(git push || true)",
		"(cd docs && pwd)",
		"echo \"$PATH\"",
		"git -C \"x$HOME/Documents/projects/readonly-bash\" status --short",
		"git log --format='%x1b]52;c;x%a'",
		"git log --pretty=format:%n",
		"git log --format='%G?'",
		"git stash pop",
		"git reflog expire --all",
		"git rev-list --objects --all",
		"git branch -D main",
		"git worktree add ../copy",
		"git submodule update --init",
		"git count-objects --unknown",
		"git cat-file --filters HEAD:README.md",
		"git blame --output out README.md",
		"tree -o out .",
		"stat --printf='\\e]52;c;x' README.md",
		"tr a '\\033'",
		"sort --compress-program=gzip README.md",
		"find . -fprintf out '%p\n'",
		"git stash show --output out",
		"git config user.name value",
		"git help -a",
		"jq -L /dev .",
		"jq 'env' package.json",
		"test README.md -ef other",
		"readlink /dev/tty",
		"wc --files0-from=/dev/tty",
		"pwd 2> err",
		"pwd < /dev/null",
		"pwd >> /dev/null",
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
