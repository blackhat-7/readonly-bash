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

type commandSegment struct {
	args            []token
	stderrToDevNull bool
}

type parsedCommand struct {
	segments []commandSegment
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
	if err := validateCommandShape(parsed); err != nil {
		return ask(err.Error())
	}

	parts := make([]string, 0, len(parsed.segments)*2-1)
	for i, segment := range parsed.segments {
		normalized, err := validateSegment(segment.args)
		if err != nil {
			return ask(err.Error())
		}
		commandPart := shellJoin(normalized)
		if segment.stderrToDevNull {
			commandPart += " 2>/dev/null"
		}
		parts = append(parts, commandPart)
		if i < len(parsed.ops) {
			parts = append(parts, parsed.ops[i])
		}
	}

	return Classification{Decision: DecisionReadOnly, CommandToRun: strings.Join(parts, " ")}
}

func ask(reason string) Classification {
	return Classification{Decision: DecisionAsk, Reason: reason}
}

func validateCommandShape(parsed parsedCommand) error {
	cdIndex := -1
	hasGit := false
	for i, segment := range parsed.segments {
		if len(segment.args) == 0 {
			continue
		}
		switch segment.args[0].text {
		case "cd":
			if cdIndex >= 0 {
				return errors.New("multiple cd commands are not auto-approved")
			}
			cdIndex = i
		case "git":
			hasGit = true
		}
	}
	if cdIndex < 0 {
		return nil
	}
	if cdIndex != 0 {
		return errors.New("cd must be the first command segment")
	}
	if len(parsed.segments) < 2 {
		return errors.New("standalone cd is not auto-approved")
	}
	if len(parsed.ops) == 0 || parsed.ops[0] != "&&" {
		return errors.New("cd must be followed by &&")
	}
	for _, op := range parsed.ops[1:] {
		if op == ";" || op == "||" {
			return errors.New("cd command chains cannot use ; or ||")
		}
	}
	if hasGit {
		return errors.New("cd with git is not auto-approved")
	}
	return nil
}

func parseCommand(input string) (parsedCommand, error) {
	if strings.TrimSpace(input) == "" {
		return parsedCommand{}, errors.New("empty command")
	}

	var out parsedCommand
	var current []token
	var b strings.Builder
	var tokenQuoted bool
	stderrToDevNull := false
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
		out.segments = append(out.segments, commandSegment{args: current, stderrToDevNull: stderrToDevNull})
		out.ops = append(out.ops, op)
		current = nil
		stderrToDevNull = false
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
				if isControlByte(c) {
					return parsedCommand{}, errors.New("control characters are not allowed")
				}
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
				if isControlByte(c) {
					return parsedCommand{}, errors.New("control characters are not allowed")
				}
				b.WriteByte(c)
			}
			continue
		}
		if isControlByte(c) && c != '\t' {
			return parsedCommand{}, errors.New("control characters are not allowed")
		}
		if b.Len() == 0 && !tokenQuoted && strings.HasPrefix(input[i:], "2>/dev/null") {
			if len(current) == 0 {
				return parsedCommand{}, errors.New("stderr redirection without command")
			}
			next := i + len("2>/dev/null")
			if next < len(input) && !isRedirectBoundary(input[next]) {
				return parsedCommand{}, errors.New("unsupported stderr redirection")
			}
			if stderrToDevNull {
				return parsedCommand{}, errors.New("duplicate stderr redirection")
			}
			stderrToDevNull = true
			i = next - 1
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
				if err := addOp("||"); err != nil {
					return parsedCommand{}, err
				}
				i++
				continue
			}
			if err := addOp("|"); err != nil {
				return parsedCommand{}, err
			}
		case ';':
			if err := addOp(";"); err != nil {
				return parsedCommand{}, err
			}
		case '\n', '\r', '<', '>', '(', ')', '#', '!', '\\':
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
	out.segments = append(out.segments, commandSegment{args: current, stderrToDevNull: stderrToDevNull})
	return out, nil
}

func isRedirectBoundary(c byte) bool {
	return c == ' ' || c == '\t' || c == '&' || c == '|' || c == ';'
}

func isControlByte(c byte) bool {
	return c < 0x20 || c == 0x7f
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
		"pwd": validatePwd, "cd": validateCd, "ls": validateLS, "cat": validateCat, "head": validateHeadTail, "tail": validateHeadTail,
		"nl": validateNL, "wc": validateWC, "rg": validateRG, "grep": validateGrep, "find": validateFind, "git": validateGit,
		"du": validateDU, "df": validateDF, "file": validateFile, "echo": validateEchoPrintf, "printf": validateEchoPrintf,
		"date": validateDate, "uname": validateUname, "whoami": validateNoArgs, "id": validateNoArgs,
		"hostname": validateNoArgs, "uptime": validateNoArgs, "true": validateNoArgs, "sort": validateSort,
		"command": validateCommand, "node": validateVersion, "python": validateVersion, "python3": validateVersion,
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

func validateCd(args []token) ([]string, error) {
	if len(args) != 2 {
		return nil, errors.New("cd requires exactly one path operand")
	}
	if strings.HasPrefix(args[1].text, "-") {
		return nil, errors.New("leading-dash cd path is not allowed")
	}
	if err := validatePathArg(args[1]); err != nil {
		return nil, err
	}
	return texts(args), nil
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

func validateNL(args []token) ([]string, error) {
	if len(args) < 2 {
		return nil, errors.New("nl requires a path operand")
	}
	paths := 0
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if !arg.quoted && arg.text == "-ba" {
			continue
		}
		if strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("unsupported nl flag")
		}
		if err := validatePathArg(arg); err != nil {
			return nil, err
		}
		paths++
	}
	if paths == 0 {
		return nil, errors.New("nl requires a path operand")
	}
	return texts(args), nil
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
		if err := validatePathArg(arg); err != nil {
			return nil, err
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
		if err := validatePathArg(arg); err != nil {
			return nil, err
		}
	}
	return texts(args), nil
}

func validateHeadTail(args []token) ([]string, error) {
	cmd := args[0].text
	normalized := []string{cmd}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if cmd == "tail" && (arg.text == "-f" || arg.text == "-F" || arg.text == "--follow" || strings.HasPrefix(arg.text, "--pid")) {
			return nil, errors.New("tail follow mode is not allowed")
		}
		if !arg.quoted && isDashCount(arg.text) {
			normalized = append(normalized, "-n", strings.TrimPrefix(arg.text, "-"))
			continue
		}
		if arg.text == "-n" || arg.text == "-c" {
			if i+1 >= len(args) || !isCount(args[i+1].text) || args[i+1].quoted {
				return nil, fmt.Errorf("%s requires a count", arg.text)
			}
			normalized = append(normalized, arg.text, args[i+1].text)
			i++
			continue
		}
		if strings.HasPrefix(arg.text, "--lines=") || strings.HasPrefix(arg.text, "--bytes=") {
			if !isCount(arg.text[strings.IndexByte(arg.text, '=')+1:]) || arg.quoted {
				return nil, errors.New("invalid count")
			}
			normalized = append(normalized, arg.text)
			continue
		}
		if strings.HasPrefix(arg.text, "-") {
			return nil, fmt.Errorf("unsupported %s flag", cmd)
		}
		if err := validatePathArg(arg); err != nil {
			return nil, err
		}
		normalized = append(normalized, arg.text)
	}
	return normalized, nil
}

func validateRG(args []token) ([]string, error) {
	boolLong := set("--files", "--line-number", "--ignore-case", "--smart-case", "--fixed-strings", "--word-regexp", "--hidden", "--no-ignore", "--no-heading", "--heading", "--json", "--stats", "--count", "--count-matches", "--pretty", "--with-filename", "--files-with-matches", "--files-without-match")
	boolShort := set("-n", "-i", "-S", "-F", "-w")
	positionals := 0
	rgFiles := false
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if _, ok := boolLong[arg.text]; ok && !arg.quoted {
			if arg.text == "--files" {
				rgFiles = true
			}
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
		if rgFiles || positionals > 0 {
			if err := validatePathArg(arg); err != nil {
				return nil, err
			}
		}
		positionals++
	}
	return texts(args), nil
}

func validateGrep(args []token) ([]string, error) {
	positionals := 0
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
		if positionals > 0 {
			if err := validatePathArg(arg); err != nil {
				return nil, err
			}
		}
		positionals++
	}
	return texts(args), nil
}

func validateFind(args []token) ([]string, error) {
	prevPredicate := false
	expectPredicate := false
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		switch arg.text {
		case "-maxdepth", "-mindepth":
			if expectPredicate {
				return nil, errors.New("find boolean predicate must be between safe predicates")
			}
			if i+1 >= len(args) || args[i+1].quoted || !isUint(args[i+1].text) {
				return nil, errors.New("find depth predicate requires number")
			}
			i++
			prevPredicate = false
		case "-type":
			if i+1 >= len(args) || args[i+1].quoted || !oneOf(args[i+1].text, "f", "d", "l", "b", "c", "p", "s") {
				return nil, errors.New("unsupported find type")
			}
			i++
			prevPredicate = true
			expectPredicate = false
		case "-name", "-iname", "-path", "-ipath":
			if i+1 >= len(args) || isLeadingDash(args[i+1]) {
				return nil, errors.New("find pattern predicate requires safe pattern")
			}
			i++
			prevPredicate = true
			expectPredicate = false
		case "-o", "-a":
			if !prevPredicate {
				return nil, errors.New("find boolean predicate must be between safe predicates")
			}
			prevPredicate = false
			expectPredicate = true
		case "-print", "-print0":
			prevPredicate = true
			expectPredicate = false
		default:
			if strings.HasPrefix(arg.text, "-") {
				return nil, errors.New("unsupported find predicate")
			}
			if expectPredicate {
				return nil, errors.New("find boolean predicate must be between safe predicates")
			}
			if err := validatePathArg(arg); err != nil {
				return nil, err
			}
			prevPredicate = false
		}
	}
	if expectPredicate {
		return nil, errors.New("find boolean predicate must be between safe predicates")
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
	revArgs := 0
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
			if !isSafeGitDiffRev(a.text) {
				return nil, errors.New("unsupported git diff revision")
			}
			revArgs++
			if revArgs > 2 {
				return nil, errors.New("too many git diff revisions")
			}
			continue
		}
		if err := validateGitPathspec(a); err != nil {
			return nil, err
		}
	}
	if seenOutput == "" {
		return nil, errors.New("patch-producing git diff is not allowed")
	}
	normalized := append([]string{"git", "diff", "--no-ext-diff", "--no-textconv"}, texts(args[2:])...)
	return normalized, nil
}

func isSafeGitDiffRev(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.Contains(value, ":/") || strings.Contains(value, "@{") {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("._/@+-^~", r):
		default:
			return false
		}
	}
	return true
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
			continue
		}
		if err := validateGitPathspec(a); err != nil {
			return nil, err
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
			if err := validateGitPathspec(a); err != nil {
				return nil, err
			}
			continue
		}
		if err := validateGitPathspec(a); err != nil {
			return nil, err
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

func validateSort(args []token) ([]string, error) {
	allowedLong := set("--reverse", "--numeric-sort", "--ignore-case", "--unique", "--version-sort", "--month-sort", "--human-numeric-sort")
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if _, ok := allowedLong[arg.text]; ok && !arg.quoted {
			continue
		}
		if strings.HasPrefix(arg.text, "-") {
			if !validShortBundle(arg.text, "fhmnruV") {
				return nil, errors.New("unsupported sort flag")
			}
			continue
		}
		if err := validatePathArg(arg); err != nil {
			return nil, err
		}
	}
	return texts(args), nil
}

func validateCommand(args []token) ([]string, error) {
	if len(args) != 3 || args[1].quoted || args[1].text != "-v" || !isSafeCommandLookupName(args[2]) {
		return nil, errors.New("only command -v with a safe command name is allowed")
	}
	return texts(args), nil
}

func isSafeCommandLookupName(arg token) bool {
	if arg.quoted || arg.text == "" || strings.HasPrefix(arg.text, "-") || strings.Contains(arg.text, "/") || isAssignment(arg.text) {
		return false
	}
	for _, r := range arg.text {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("._+-", r):
		default:
			return false
		}
	}
	return true
}

func validateEchoPrintf(args []token) ([]string, error) {
	if args[0].text == "echo" {
		for _, arg := range args[1:] {
			if !arg.quoted && strings.HasPrefix(arg.text, "-") {
				return nil, errors.New("echo options are not allowed")
			}
		}
		return texts(args), nil
	}
	if len(args) > 1 && strings.HasPrefix(args[1].text, "-") {
		return nil, errors.New("printf options are not allowed")
	}
	if len(args) > 1 && printfFormatHasUnsafeConversion(args[1].text) {
		return nil, errors.New("unsafe printf format is not allowed")
	}
	if len(args) > 1 && printfFormatHasUnsafeEscape(args[1].text) {
		return nil, errors.New("unsafe printf escape is not allowed")
	}
	return texts(args), nil
}

func printfFormatHasUnsafeConversion(format string) bool {
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		if i < len(format) && format[i] == '%' {
			continue
		}
		for i < len(format) && strings.ContainsRune("#0- +'", rune(format[i])) {
			i++
		}
		if i < len(format) && format[i] == '*' {
			i++
		} else {
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				i++
			}
		}
		if i < len(format) && format[i] == '.' {
			i++
			if i < len(format) && format[i] == '*' {
				i++
			} else {
				for i < len(format) && format[i] >= '0' && format[i] <= '9' {
					i++
				}
			}
		}
		for i < len(format) && strings.ContainsRune("hjlLtz", rune(format[i])) {
			i++
		}
		if i < len(format) && (format[i] == 'n' || format[i] == 'b') {
			return true
		}
	}
	return false
}

func printfFormatHasUnsafeEscape(format string) bool {
	for i := 0; i < len(format); i++ {
		if format[i] != '\\' {
			continue
		}
		i++
		if i >= len(format) {
			return true
		}
		switch format[i] {
		case '\\', '\'', '"', 'n', 't':
			continue
		default:
			return true
		}
	}
	return false
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
		if err := validatePathArg(a); err != nil {
			return nil, err
		}
	}
	return texts(args), nil
}

func validatePathArg(arg token) error {
	if strings.HasPrefix(arg.text, "~") {
		return errors.New("leading-tilde path operand is not allowed")
	}
	if isSpecialAbsolutePath(arg.text) {
		return errors.New("special absolute path is not allowed")
	}
	return nil
}

func validateGitPathspec(arg token) error {
	if strings.HasPrefix(arg.text, "~") {
		return errors.New("leading-tilde pathspec is not allowed")
	}
	if isSpecialAbsolutePath(arg.text) {
		return errors.New("special absolute pathspec is not allowed")
	}
	return nil
}

func isSpecialAbsolutePath(path string) bool {
	for _, root := range []string{"/dev", "/proc", "/sys", "/net"} {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
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

func isDashCount(s string) bool {
	return len(s) > 1 && s[0] == '-' && isUint(s[1:])
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
