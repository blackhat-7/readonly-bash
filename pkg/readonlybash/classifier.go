package readonlybash

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Decision string

const (
	DecisionReadOnly Decision = "readonly"
	DecisionAsk      Decision = "ask"
)

type Classification struct {
	Decision     Decision `json:"decision"`
	Reason       string   `json:"reason,omitempty"`
	CommandToRun string   `json:"commandToRun,omitempty"`
}

type token struct {
	text   string
	quoted bool
}

type parsedCommand struct {
	segments [][]token
	ops      []string
}

type segmentValidator func([]token) ([]string, error)

var networkCommands = map[string]struct{}{
	"gh": {}, "curl": {}, "wget": {}, "ssh": {}, "scp": {}, "sftp": {}, "rsync": {},
	"nc": {}, "ncat": {}, "telnet": {}, "ftp": {}, "dig": {}, "nslookup": {}, "host": {}, "ping": {},
	"npm": {}, "pnpm": {}, "yarn": {}, "bun": {}, "pip": {}, "brew": {},
	"docker": {}, "kubectl": {}, "aws": {}, "gcloud": {}, "az": {},
}

func Classify(command string) Classification {
	parsed, err := parseCommand(command)
	if err != nil {
		return ask(err.Error())
	}

	parts := make([]string, 0, len(parsed.segments)*2-1)
	for i, segment := range parsed.segments {
		normalized, err := validateSegment(segment)
		if err != nil {
			return ask(err.Error())
		}
		parts = append(parts, shellJoin(normalized))
		if i < len(parsed.ops) {
			parts = append(parts, parsed.ops[i])
		}
	}

	return Classification{Decision: DecisionReadOnly, CommandToRun: strings.Join(parts, " ")}
}

func ask(reason string) Classification {
	return Classification{Decision: DecisionAsk, Reason: reason}
}

func parseCommand(input string) (parsedCommand, error) {
	if strings.TrimSpace(input) == "" {
		return parsedCommand{}, errors.New("empty command")
	}

	var out parsedCommand
	var current []token
	var b strings.Builder
	var tokenQuoted bool
	inSingle, inDouble := false, false

	flushToken := func() {
		if b.Len() == 0 && !tokenQuoted {
			return
		}
		current = append(current, token{text: b.String(), quoted: tokenQuoted})
		b.Reset()
		tokenQuoted = false
	}
	addOp := func(op string) error {
		flushToken()
		if len(current) == 0 {
			return fmt.Errorf("operator %q without command", op)
		}
		out.segments = append(out.segments, current)
		out.ops = append(out.ops, op)
		current = nil
		return nil
	}

	for i := 0; i < len(input); i++ {
		c := input[i]
		if inSingle {
			switch c {
			case '\'':
				inSingle = false
			case '\n', '\r':
				return parsedCommand{}, errors.New("newlines are not allowed")
			case '$', '`':
				return parsedCommand{}, errors.New("expansion syntax is not allowed")
			default:
				b.WriteByte(c)
			}
			continue
		}
		if inDouble {
			switch c {
			case '"':
				inDouble = false
			case '\n', '\r':
				return parsedCommand{}, errors.New("newlines are not allowed")
			case '$', '`', '\\':
				return parsedCommand{}, errors.New("expansion syntax is not allowed")
			default:
				b.WriteByte(c)
			}
			continue
		}

		switch c {
		case ' ', '\t':
			flushToken()
		case '\'', '"':
			tokenQuoted = true
			if c == '\'' {
				inSingle = true
			} else {
				inDouble = true
			}
		case '&':
			if i+1 >= len(input) || input[i+1] != '&' {
				return parsedCommand{}, errors.New("background operator is not allowed")
			}
			if err := addOp("&&"); err != nil {
				return parsedCommand{}, err
			}
			i++
		case '|':
			if i+1 < len(input) && input[i+1] == '|' {
				return parsedCommand{}, errors.New("logical-or is not allowed")
			}
			if err := addOp("|"); err != nil {
				return parsedCommand{}, err
			}
		case '\n', '\r', ';', '<', '>', '(', ')', '#', '!', '\\':
			return parsedCommand{}, fmt.Errorf("unsupported shell syntax %q", c)
		case '$', '`':
			return parsedCommand{}, errors.New("expansion syntax is not allowed")
		case '*', '?', '[', ']', '{', '}':
			return parsedCommand{}, errors.New("unquoted glob or brace syntax is not allowed")
		default:
			b.WriteByte(c)
		}
	}
	if inSingle || inDouble {
		return parsedCommand{}, errors.New("unclosed quote")
	}
	flushToken()
	if len(current) == 0 {
		return parsedCommand{}, errors.New("trailing operator")
	}
	out.segments = append(out.segments, current)
	return out, nil
}

func validateSegment(args []token) ([]string, error) {
	if len(args) == 0 {
		return nil, errors.New("empty command segment")
	}
	cmd := args[0].text
	if args[0].quoted || strings.Contains(cmd, "/") || isAssignment(cmd) {
		return nil, errors.New("unsupported command word")
	}
	if _, denied := networkCommands[cmd]; denied {
		return nil, fmt.Errorf("network-capable command %q is not auto-approved", cmd)
	}

	validators := map[string]segmentValidator{
		"pwd": validatePwd, "ls": validateLS, "cat": validateCat, "head": validateHeadTail, "tail": validateHeadTail,
		"wc": validateWC, "rg": validateRG, "grep": validateGrep, "find": validateFind, "git": validateGit,
		"du": validateDU, "df": validateDF, "file": validateFile, "echo": validateEchoPrintf, "printf": validateEchoPrintf,
		"date": validateDate, "uname": validateUname, "whoami": validateNoArgs, "id": validateNoArgs,
		"hostname": validateNoArgs, "uptime": validateNoArgs, "node": validateVersion, "python": validateVersion,
		"python3": validateVersion,
	}
	validator, ok := validators[cmd]
	if !ok {
		return nil, fmt.Errorf("command %q is not allowlisted", cmd)
	}
	return validator(args)
}

func validateNoArgs(args []token) ([]string, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s does not allow arguments", args[0].text)
	}
	return texts(args), nil
}

func validatePwd(args []token) ([]string, error) {
	if len(args) == 1 || (len(args) == 2 && !args[1].quoted && (args[1].text == "-L" || args[1].text == "-P")) {
		return texts(args), nil
	}
	return nil, errors.New("unsupported pwd arguments")
}

func validateLS(args []token) ([]string, error) {
	return validateFlagsAndPaths(args, "1aAlhRtrSFGd")
}

func validateCat(args []token) ([]string, error) {
	return validateFlagsAndPaths(args, "benstuvAET")
}

func validateWC(args []token) ([]string, error) {
	return validateFlagsAndPaths(args, "lwcLm")
}

func validateDU(args []token) ([]string, error) {
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if arg.text == "-d" {
			if i+1 >= len(args) || !isUint(args[i+1].text) || args[i+1].quoted {
				return nil, errors.New("du -d requires a numeric depth")
			}
			i++
			continue
		}
		if strings.HasPrefix(arg.text, "--max-depth=") && isUint(strings.TrimPrefix(arg.text, "--max-depth=")) && !arg.quoted {
			continue
		}
		if strings.HasPrefix(arg.text, "-") {
			if !validShortBundle(arg.text, "shkmgad") {
				return nil, errors.New("unsupported du flag")
			}
			continue
		}
	}
	return texts(args), nil
}

func validateDF(args []token) ([]string, error) {
	return validateFlagsAndPaths(args, "hkmgiT")
}

func validateFile(args []token) ([]string, error) {
	allowedLong := map[string]struct{}{"--brief": {}, "--mime": {}, "--mime-type": {}, "--mime-encoding": {}}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if _, ok := allowedLong[arg.text]; ok && !arg.quoted {
			continue
		}
		if strings.HasPrefix(arg.text, "-") {
			if !validShortBundle(arg.text, "biILh") {
				return nil, errors.New("unsupported file flag")
			}
			continue
		}
	}
	return texts(args), nil
}

func validateHeadTail(args []token) ([]string, error) {
	cmd := args[0].text
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if cmd == "tail" && (arg.text == "-f" || arg.text == "-F" || arg.text == "--follow" || strings.HasPrefix(arg.text, "--pid")) {
			return nil, errors.New("tail follow mode is not allowed")
		}
		if arg.text == "-n" || arg.text == "-c" {
			if i+1 >= len(args) || !isCount(args[i+1].text) || args[i+1].quoted {
				return nil, fmt.Errorf("%s requires a count", arg.text)
			}
			i++
			continue
		}
		if strings.HasPrefix(arg.text, "--lines=") || strings.HasPrefix(arg.text, "--bytes=") {
			if !isCount(arg.text[strings.IndexByte(arg.text, '=')+1:]) || arg.quoted {
				return nil, errors.New("invalid count")
			}
			continue
		}
		if strings.HasPrefix(arg.text, "-") {
			return nil, fmt.Errorf("unsupported %s flag", cmd)
		}
	}
	return texts(args), nil
}

func validateRG(args []token) ([]string, error) {
	boolLong := set("--files", "--line-number", "--ignore-case", "--smart-case", "--fixed-strings", "--word-regexp", "--hidden", "--no-ignore", "--no-heading", "--heading", "--json", "--stats", "--count", "--count-matches", "--pretty", "--with-filename", "--files-with-matches", "--files-without-match")
	boolShort := set("-n", "-i", "-S", "-F", "-w")
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if _, ok := boolLong[arg.text]; ok && !arg.quoted {
			continue
		}
		if _, ok := boolShort[arg.text]; ok && !arg.quoted {
			continue
		}
		if oneOf(arg.text, "-C", "-A", "-B", "--context", "--after-context", "--before-context", "--max-depth") {
			if i+1 >= len(args) || args[i+1].quoted || !isUint(args[i+1].text) {
				return nil, errors.New("rg flag requires numeric argument")
			}
			i++
			continue
		}
		if hasLongNumeric(arg.text, "--context=", "--after-context=", "--before-context=", "--max-depth=") && !arg.quoted {
			continue
		}
		if oneOf(arg.text, "-g", "--glob", "-t", "--type", "-T", "--type-not") {
			if i+1 >= len(args) || isLeadingDash(args[i+1]) {
				return nil, errors.New("rg flag requires safe argument")
			}
			i++
			continue
		}
		if strings.HasPrefix(arg.text, "--glob=") || strings.HasPrefix(arg.text, "--type=") || strings.HasPrefix(arg.text, "--type-not=") {
			if arg.quoted || strings.TrimPrefix(arg.text[strings.IndexByte(arg.text, '='):], "=") == "" {
				return nil, errors.New("rg flag requires argument")
			}
			continue
		}
		if arg.text == "--sort" || arg.text == "--sortr" || arg.text == "--color" {
			if i+1 >= len(args) || args[i+1].quoted || !validRGValue(arg.text, args[i+1].text) {
				return nil, errors.New("invalid rg flag value")
			}
			i++
			continue
		}
		if strings.HasPrefix(arg.text, "--color=") && !arg.quoted && oneOf(strings.TrimPrefix(arg.text, "--color="), "never", "always", "auto") {
			continue
		}
		if strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("unsupported rg flag")
		}
	}
	return texts(args), nil
}

func validateGrep(args []token) ([]string, error) {
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if arg.text == "-C" || arg.text == "-A" || arg.text == "-B" {
			if i+1 >= len(args) || args[i+1].quoted || !isUint(args[i+1].text) {
				return nil, errors.New("grep context flag requires numeric argument")
			}
			i++
			continue
		}
		if oneOf(arg.text, "--include", "--exclude", "--exclude-dir") {
			if i+1 >= len(args) || isLeadingDash(args[i+1]) {
				return nil, errors.New("grep flag requires safe pattern")
			}
			i++
			continue
		}
		if strings.HasPrefix(arg.text, "--include=") || strings.HasPrefix(arg.text, "--exclude=") || strings.HasPrefix(arg.text, "--exclude-dir=") {
			if arg.quoted || arg.text[strings.IndexByte(arg.text, '=')+1:] == "" {
				return nil, errors.New("grep flag requires pattern")
			}
			continue
		}
		if strings.HasPrefix(arg.text, "--color=") && !arg.quoted && oneOf(strings.TrimPrefix(arg.text, "--color="), "never", "always", "auto") {
			continue
		}
		if strings.HasPrefix(arg.text, "-") {
			if !validShortBundle(arg.text, "rRniIFEwvlLHh") {
				return nil, errors.New("unsupported grep flag")
			}
			continue
		}
	}
	return texts(args), nil
}

func validateFind(args []token) ([]string, error) {
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		switch arg.text {
		case "-maxdepth", "-mindepth":
			if i+1 >= len(args) || args[i+1].quoted || !isUint(args[i+1].text) {
				return nil, errors.New("find depth predicate requires number")
			}
			i++
		case "-type":
			if i+1 >= len(args) || args[i+1].quoted || !oneOf(args[i+1].text, "f", "d", "l", "b", "c", "p", "s") {
				return nil, errors.New("unsupported find type")
			}
			i++
		case "-name", "-iname", "-path", "-ipath":
			if i+1 >= len(args) || isLeadingDash(args[i+1]) {
				return nil, errors.New("find pattern predicate requires safe pattern")
			}
			i++
		case "-print", "-print0":
		default:
			if strings.HasPrefix(arg.text, "-") {
				return nil, errors.New("unsupported find predicate")
			}
		}
	}
	return texts(args), nil
}

func validateGit(args []token) ([]string, error) {
	if len(args) < 2 || args[1].quoted || strings.HasPrefix(args[1].text, "-") {
		return nil, errors.New("unsupported git invocation")
	}
	switch args[1].text {
	case "status":
		return validateGitStatus(args)
	case "diff":
		return validateGitDiff(args)
	case "log":
		return validateGitLog(args)
	case "branch":
		if len(args) == 3 && !args[2].quoted && args[2].text == "--show-current" {
			return texts(args), nil
		}
	case "rev-parse":
		return validateGitRevParse(args)
	case "ls-files":
		return validateGitLsFiles(args)
	case "remote":
		if len(args) == 2 || (len(args) == 3 && !args[2].quoted && args[2].text == "-v") {
			return texts(args), nil
		}
	case "tag":
		return validateGitTag(args)
	}
	return nil, errors.New("unsupported git command")
}

func validateGitStatus(args []token) ([]string, error) {
	for i := 2; i < len(args); i++ {
		a := args[i]
		if a.quoted {
			return nil, errors.New("quoted git status flag is not allowed")
		}
		if oneOf(a.text, "--short", "--porcelain", "--porcelain=v1", "--branch", "-s", "-b", "--ignored", "--untracked-files") {
			continue
		}
		if strings.HasPrefix(a.text, "--untracked-files=") && oneOf(strings.TrimPrefix(a.text, "--untracked-files="), "no", "normal", "all") {
			continue
		}
		return nil, errors.New("unsupported git status flag")
	}
	return texts(args), nil
}

func validateGitDiff(args []token) ([]string, error) {
	outModes := set("--stat", "--numstat", "--shortstat", "--name-only", "--name-status")
	seenOutput := ""
	afterSep := false
	for i := 2; i < len(args); i++ {
		a := args[i]
		if !afterSep && a.text == "--" && !a.quoted {
			afterSep = true
			continue
		}
		if !afterSep {
			if _, ok := outModes[a.text]; ok && !a.quoted {
				if seenOutput != "" {
					return nil, errors.New("git diff allows one output mode")
				}
				seenOutput = a.text
				continue
			}
			if oneOf(a.text, "--cached", "--staged") && !a.quoted {
				continue
			}
			if strings.HasPrefix(a.text, "-") {
				return nil, errors.New("unsupported git diff flag")
			}
			return nil, errors.New("git diff pathspecs require --")
		}
	}
	if seenOutput == "" {
		return nil, errors.New("patch-producing git diff is not allowed")
	}
	normalized := append([]string{"git", "diff", "--no-ext-diff", "--no-textconv"}, texts(args[2:])...)
	return normalized, nil
}

func validateGitLog(args []token) ([]string, error) {
	reject := set("--stat", "--numstat", "--name-only", "--name-status", "--summary", "--raw", "-p", "--patch", "--ext-diff", "--textconv")
	afterSep := false
	for i := 2; i < len(args); i++ {
		a := args[i]
		if !afterSep && a.text == "--" && !a.quoted {
			afterSep = true
			continue
		}
		if !afterSep {
			if _, bad := reject[a.text]; bad {
				return nil, errors.New("diff-producing git log mode is not allowed")
			}
			if oneOf(a.text, "--oneline", "--graph", "--decorate", "--all") && !a.quoted {
				continue
			}
			if a.text == "-n" || a.text == "--max-count" {
				if i+1 >= len(args) || args[i+1].quoted || !isUint(args[i+1].text) {
					return nil, errors.New("git log count requires number")
				}
				i++
				continue
			}
			if strings.HasPrefix(a.text, "-n") && len(a.text) > 2 && isUint(strings.TrimPrefix(a.text, "-n")) && !a.quoted {
				continue
			}
			if strings.HasPrefix(a.text, "--max-count=") && isUint(strings.TrimPrefix(a.text, "--max-count=")) && !a.quoted {
				continue
			}
			if oneOf(a.text, "--since", "--until", "--author", "--grep") {
				if i+1 >= len(args) || isLeadingDash(args[i+1]) {
					return nil, errors.New("git log flag requires safe value")
				}
				i++
				continue
			}
			if hasLongValue(a.text, "--since=", "--until=", "--author=", "--grep=") && !a.quoted {
				continue
			}
			if strings.HasPrefix(a.text, "-") {
				return nil, errors.New("unsupported git log flag")
			}
		}
	}
	return texts(args), nil
}

func validateGitRevParse(args []token) ([]string, error) {
	if len(args) == 3 && !args[2].quoted && oneOf(args[2].text, "--show-toplevel", "--git-dir", "--is-inside-work-tree") {
		return texts(args), nil
	}
	if len(args) == 4 && !args[2].quoted && !args[3].quoted && args[2].text == "--abbrev-ref" && args[3].text == "HEAD" {
		return texts(args), nil
	}
	return nil, errors.New("unsupported git rev-parse arguments")
}

func validateGitLsFiles(args []token) ([]string, error) {
	allowed := set("--stage", "--deleted", "--modified", "--others", "--exclude-standard", "-z")
	afterSep := false
	for i := 2; i < len(args); i++ {
		a := args[i]
		if !afterSep && a.text == "--" && !a.quoted {
			afterSep = true
			continue
		}
		if !afterSep {
			if _, ok := allowed[a.text]; ok && !a.quoted {
				continue
			}
			if strings.HasPrefix(a.text, "-") {
				return nil, errors.New("unsupported git ls-files flag or leading-dash pathspec")
			}
		}
	}
	return texts(args), nil
}

func validateGitTag(args []token) ([]string, error) {
	if len(args) >= 3 && !args[2].quoted && args[2].text == "--list" {
		for _, a := range args[3:] {
			if isLeadingDash(a) {
				return nil, errors.New("leading-dash tag pattern is not allowed")
			}
		}
		return texts(args), nil
	}
	return nil, errors.New("unsupported git tag arguments")
}

func validateEchoPrintf(args []token) ([]string, error) {
	if args[0].text == "printf" && len(args) > 1 && strings.HasPrefix(args[1].text, "-") {
		return nil, errors.New("printf options are not allowed")
	}
	return texts(args), nil
}

func validateDate(args []token) ([]string, error) {
	seenUTC, seenFormat := false, false
	for _, a := range args[1:] {
		if !a.quoted && a.text == "-u" && !seenUTC {
			seenUTC = true
			continue
		}
		if strings.HasPrefix(a.text, "+") && !seenFormat {
			seenFormat = true
			continue
		}
		return nil, errors.New("unsupported date arguments")
	}
	return texts(args), nil
}

func validateUname(args []token) ([]string, error) {
	if len(args) == 1 || (len(args) == 2 && !args[1].quoted && validShortBundle(args[1].text, "asnrvmpio")) {
		return texts(args), nil
	}
	return nil, errors.New("unsupported uname arguments")
}

func validateVersion(args []token) ([]string, error) {
	if len(args) != 2 || args[1].quoted {
		return nil, errors.New("only version checks are allowed")
	}
	if args[0].text == "node" && (args[1].text == "-v" || args[1].text == "--version") {
		return texts(args), nil
	}
	if (args[0].text == "python" || args[0].text == "python3") && args[1].text == "--version" {
		return texts(args), nil
	}
	return nil, errors.New("only version checks are allowed")
}

func validateFlagsAndPaths(args []token, allowedFlags string) ([]string, error) {
	for i := 1; i < len(args); i++ {
		a := args[i]
		if a.quoted && strings.HasPrefix(a.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if strings.HasPrefix(a.text, "-") {
			if !validShortBundle(a.text, allowedFlags) {
				return nil, errors.New("unsupported flag")
			}
			continue
		}
	}
	return texts(args), nil
}

func texts(args []token) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		out[i] = arg.text
	}
	return out
}

func isAssignment(s string) bool {
	idx := strings.IndexByte(s, '=')
	if idx <= 0 {
		return false
	}
	for i, r := range s[:idx] {
		if i == 0 {
			if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
				return false
			}
			continue
		}
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func validShortBundle(s, allowed string) bool {
	if len(s) < 2 || s[0] != '-' || s == "--" {
		return false
	}
	for _, r := range s[1:] {
		if !strings.ContainsRune(allowed, r) {
			return false
		}
	}
	return true
}

func isUint(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.ParseUint(s, 10, 31)
	return err == nil
}

func isCount(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '+' {
		s = s[1:]
	}
	return isUint(s)
}

func isLeadingDash(t token) bool { return strings.HasPrefix(t.text, "-") }

func set(values ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(values))
	for _, value := range values {
		m[value] = struct{}{}
	}
	return m
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func hasLongNumeric(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return isUint(strings.TrimPrefix(value, prefix))
		}
	}
	return false
}

func hasLongValue(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) && strings.TrimPrefix(value, prefix) != "" {
			return true
		}
	}
	return false
}

func validRGValue(flag, value string) bool {
	switch flag {
	case "--sort", "--sortr":
		return value == "path"
	case "--color":
		return oneOf(value, "never", "always", "auto")
	default:
		return false
	}
}
