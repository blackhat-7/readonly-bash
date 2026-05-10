package readonlybash

import (
	"errors"
	"fmt"
	"os"
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
	text             string
	quoted           bool
	hasQuotedNewline bool
	homeExpanded     bool
}

type commandSegment struct {
	args            []token
	group           *parsedCommand
	stdoutToDevNull bool
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
	commandToRun, err := validateAndRenderCommand(parsed)
	if err != nil {
		return ask(err.Error())
	}
	return Classification{Decision: DecisionReadOnly, CommandToRun: commandToRun}
}

func ask(reason string) Classification {
	return Classification{Decision: DecisionAsk, Reason: reason}
}

func validateAndRenderCommand(parsed parsedCommand) (string, error) {
	parts := make([]string, 0, len(parsed.segments)*2-1)
	for i, segment := range parsed.segments {
		commandPart, err := validateAndRenderSegment(segment)
		if err != nil {
			return "", err
		}
		parts = append(parts, commandPart)
		if i < len(parsed.ops) {
			parts = append(parts, parsed.ops[i])
		}
	}
	return strings.Join(parts, " "), nil
}

func validateAndRenderSegment(segment commandSegment) (string, error) {
	var commandPart string
	if segment.group != nil {
		rendered, err := validateAndRenderCommand(*segment.group)
		if err != nil {
			return "", err
		}
		commandPart = "( " + rendered + " )"
	} else {
		normalized, err := validateSegment(segment.args)
		if err != nil {
			return "", err
		}
		commandPart = shellJoin(normalized)
	}
	if segment.stdoutToDevNull {
		commandPart += " >/dev/null"
	}
	if segment.stderrToDevNull {
		commandPart += " 2>/dev/null"
	}
	return commandPart, nil
}

func validateCommandShape(parsed parsedCommand) error {
	cdIndex := -1
	for i, segment := range parsed.segments {
		if segment.group != nil {
			if containsCd(*segment.group) {
				return errors.New("cd inside command groups is not auto-approved")
			}
			if err := validateCommandShape(*segment.group); err != nil {
				return err
			}
			continue
		}
		if len(segment.args) == 0 {
			continue
		}
		if segment.args[0].text == "cd" {
			if cdIndex >= 0 {
				return errors.New("multiple cd commands are not auto-approved")
			}
			cdIndex = i
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
	return nil
}

func containsCd(parsed parsedCommand) bool {
	for _, segment := range parsed.segments {
		if segment.group != nil {
			if containsCd(*segment.group) {
				return true
			}
			continue
		}
		if len(segment.args) > 0 && segment.args[0].text == "cd" {
			return true
		}
	}
	return false
}

func parseCommand(input string) (parsedCommand, error) {
	if strings.TrimSpace(input) == "" {
		return parsedCommand{}, errors.New("empty command")
	}

	var out parsedCommand
	var current []token
	var currentGroup *parsedCommand
	var b strings.Builder
	var tokenQuoted bool
	var tokenHasQuotedNewline bool
	var tokenHomeExpanded bool
	stdoutToDevNull := false
	stderrToDevNull := false
	inSingle, inDouble := false, false

	flushToken := func() error {
		if b.Len() == 0 && !tokenQuoted {
			return nil
		}
		if currentGroup != nil {
			return errors.New("command group cannot be combined with arguments")
		}
		current = append(current, token{text: b.String(), quoted: tokenQuoted, hasQuotedNewline: tokenHasQuotedNewline, homeExpanded: tokenHomeExpanded})
		b.Reset()
		tokenQuoted = false
		tokenHasQuotedNewline = false
		tokenHomeExpanded = false
		return nil
	}
	addOp := func(op string) error {
		if err := flushToken(); err != nil {
			return err
		}
		if len(current) == 0 && currentGroup == nil {
			return fmt.Errorf("operator %q without command", op)
		}
		out.segments = append(out.segments, commandSegment{args: current, group: currentGroup, stdoutToDevNull: stdoutToDevNull, stderrToDevNull: stderrToDevNull})
		out.ops = append(out.ops, op)
		current = nil
		currentGroup = nil
		stdoutToDevNull = false
		stderrToDevNull = false
		return nil
	}
	consumeDevNullRedirect := func(pos int, prefix, stream string, seen *bool) (bool, int, error) {
		next := -1
		if strings.HasPrefix(input[pos:], prefix) {
			next = pos + len(prefix)
		} else {
			op := strings.TrimSuffix(prefix, "/dev/null")
			if !strings.HasPrefix(input[pos:], op) {
				return false, pos, nil
			}
			n := pos + len(op)
			for n < len(input) && (input[n] == ' ' || input[n] == '\t') {
				n++
			}
			if !strings.HasPrefix(input[n:], "/dev/null") {
				return false, pos, nil
			}
			next = n + len("/dev/null")
		}
		if len(current) == 0 && currentGroup == nil {
			return false, pos, fmt.Errorf("%s redirection without command", stream)
		}
		if next < len(input) && !isRedirectBoundary(input[next]) {
			return false, pos, fmt.Errorf("unsupported %s redirection", stream)
		}
		if *seen {
			return false, pos, fmt.Errorf("duplicate %s redirection", stream)
		}
		*seen = true
		return true, next - 1, nil
	}

	for i := 0; i < len(input); i++ {
		c := input[i]
		if inSingle {
			switch c {
			case '\'':
				inSingle = false
			case '\n', '\r':
				tokenHasQuotedNewline = true
				b.WriteByte('\n')
				if c == '\r' && i+1 < len(input) && input[i+1] == '\n' {
					i++
				}
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
				tokenHasQuotedNewline = true
				b.WriteByte('\n')
				if c == '\r' && i+1 < len(input) && input[i+1] == '\n' {
					i++
				}
			case '$':
				if b.Len() != 0 {
					return parsedCommand{}, errors.New("expansion syntax is not allowed")
				}
				home, next, ok, err := consumeHomePath(input, i)
				if err != nil {
					return parsedCommand{}, err
				}
				if !ok {
					return parsedCommand{}, errors.New("expansion syntax is not allowed")
				}
				b.WriteString(home)
				tokenHomeExpanded = true
				i = next
			case '`', '\\':
				return parsedCommand{}, errors.New("expansion syntax is not allowed")
			default:
				if isControlByte(c) {
					return parsedCommand{}, errors.New("control characters are not allowed")
				}
				b.WriteByte(c)
			}
			continue
		}
		if isControlByte(c) && c != '\t' && c != '\n' && c != '\r' {
			return parsedCommand{}, errors.New("control characters are not allowed")
		}
		if b.Len() == 0 && !tokenQuoted {
			if ok, next, err := consumeDevNullRedirect(i, "1>/dev/null", "stdout", &stdoutToDevNull); err != nil {
				return parsedCommand{}, err
			} else if ok {
				i = next
				continue
			}
			if ok, next, err := consumeDevNullRedirect(i, ">/dev/null", "stdout", &stdoutToDevNull); err != nil {
				return parsedCommand{}, err
			} else if ok {
				i = next
				continue
			}
			if ok, next, err := consumeDevNullRedirect(i, "2>/dev/null", "stderr", &stderrToDevNull); err != nil {
				return parsedCommand{}, err
			} else if ok {
				i = next
				continue
			}
		}
		if currentGroup != nil && !isSegmentBoundary(c) {
			return parsedCommand{}, errors.New("command group cannot be combined with arguments")
		}

		switch c {
		case ' ', '\t':
			if err := flushToken(); err != nil {
				return parsedCommand{}, err
			}
		case '\'', '"':
			tokenQuoted = true
			if c == '\'' {
				inSingle = true
			} else {
				inDouble = true
			}
		case '(':
			if len(current) != 0 || b.Len() != 0 || tokenQuoted || currentGroup != nil {
				return parsedCommand{}, fmt.Errorf("unsupported shell syntax %q", c)
			}
			end, err := findMatchingParen(input, i)
			if err != nil {
				return parsedCommand{}, err
			}
			inner, err := parseCommand(input[i+1 : end])
			if err != nil {
				return parsedCommand{}, err
			}
			currentGroup = &inner
			i = end
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
		case '\n', '\r':
			if err := addOp(";"); err != nil {
				return parsedCommand{}, err
			}
			if c == '\r' && i+1 < len(input) && input[i+1] == '\n' {
				i++
			}
		case '\\':
			if i+1 < len(input) && (input[i+1] == '(' || input[i+1] == ')') {
				b.WriteByte(input[i+1])
				tokenQuoted = true
				i++
				continue
			}
			return parsedCommand{}, fmt.Errorf("unsupported shell syntax %q", c)
		case '<', '>', ')', '#', '!':
			return parsedCommand{}, fmt.Errorf("unsupported shell syntax %q", c)
		case '$':
			if b.Len() != 0 {
				return parsedCommand{}, errors.New("expansion syntax is not allowed")
			}
			home, next, ok, err := consumeHomePath(input, i)
			if err != nil {
				return parsedCommand{}, err
			}
			if !ok {
				return parsedCommand{}, errors.New("expansion syntax is not allowed")
			}
			b.WriteString(home)
			tokenHomeExpanded = true
			i = next
		case '`':
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
	if err := flushToken(); err != nil {
		return parsedCommand{}, err
	}
	if len(current) == 0 && currentGroup == nil {
		return parsedCommand{}, errors.New("trailing operator")
	}
	out.segments = append(out.segments, commandSegment{args: current, group: currentGroup, stdoutToDevNull: stdoutToDevNull, stderrToDevNull: stderrToDevNull})
	return out, nil
}

func consumeHomePath(input string, pos int) (string, int, bool, error) {
	if !strings.HasPrefix(input[pos:], "$HOME") {
		return "", pos, false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", pos, true, errors.New("home directory is not available")
	}
	if strings.HasPrefix(input[pos:], "$HOME/") {
		return home + "/", pos + len("$HOME/") - 1, true, nil
	}
	next := pos + len("$HOME")
	if next < len(input) && !isHomeBoundary(input[next]) {
		return "", pos, false, nil
	}
	return home, next - 1, true, nil
}

func isHomeBoundary(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '&' || c == '|' || c == ';' || c == ')' || c == '"'
}

func findMatchingParen(input string, start int) (int, error) {
	depth := 0
	inSingle, inDouble := false, false
	for i := start; i < len(input); i++ {
		c := input[i]
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if c == '"' {
				inDouble = false
			}
			continue
		}
		switch c {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '\\':
			if i+1 < len(input) && (input[i+1] == '(' || input[i+1] == ')') {
				i++
			}
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return -1, errors.New("unclosed command group")
}

func isSegmentBoundary(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '&' || c == '|' || c == ';'
}

func isRedirectBoundary(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '&' || c == '|' || c == ';'
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
	if cmd != "printf" && hasQuotedNewline(args) {
		return nil, errors.New("quoted newlines are only allowed for printf")
	}

	validators := map[string]segmentValidator{
		"pwd": validatePwd, "cd": validateCd, "ls": validateLS, "cat": validateCat, "head": validateHeadTail, "tail": validateHeadTail,
		"nl": validateNL, "sed": validateSed, "wc": validateWC, "rg": validateRG, "grep": validateGrep, "find": validateFind, "git": validateGit,
		"du": validateDU, "df": validateDF, "file": validateFile, "echo": validateEchoPrintf, "printf": validateEchoPrintf,
		"date": validateDate, "uname": validateUname, "whoami": validateNoArgs, "id": validateNoArgs,
		"hostname": validateNoArgs, "uptime": validateNoArgs, "true": validateNoArgs, "false": validateNoArgs, "sort": validateSort,
		"uniq": validateUniq, "stat": validateStat, "readlink": validateReadlinkRealpath, "realpath": validateReadlinkRealpath,
		"tree": validateTree, "cut": validateCut, "tr": validateTr, "basename": validateBaseDirName, "dirname": validateBaseDirName,
		"test": validateTest, "jq": validateJQ,
		"command": validateCommand, "node": validateVersion, "python": validateVersion, "python3": validateVersion,
	}
	validator, ok := validators[cmd]
	if !ok {
		return nil, fmt.Errorf("command %q is not allowlisted", cmd)
	}
	return validator(args)
}

func hasQuotedNewline(args []token) bool {
	for _, arg := range args {
		if arg.hasQuotedNewline {
			return true
		}
	}
	return false
}

func hasHomeExpanded(args []token) bool {
	for _, arg := range args {
		if arg.homeExpanded {
			return true
		}
	}
	return false
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
	return validateFlagsAndPaths(args, "1aAlhRtrSFGdinpsX")
}

func validateCat(args []token) ([]string, error) {
	return validateFlagsAndPaths(args, "benstuvAET")
}

func validateWC(args []token) ([]string, error) {
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if arg.text == "--files0-from" && !arg.quoted {
			if i+1 >= len(args) || isLeadingDash(args[i+1]) {
				return nil, errors.New("wc --files0-from requires safe path")
			}
			if err := validatePathArg(args[i+1]); err != nil {
				return nil, err
			}
			i++
			continue
		}
		if strings.HasPrefix(arg.text, "--files0-from=") && !arg.quoted {
			path := token{text: strings.TrimPrefix(arg.text, "--files0-from=")}
			if path.text == "" || isLeadingDash(path) {
				return nil, errors.New("wc --files0-from requires safe path")
			}
			if err := validatePathArg(path); err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(arg.text, "-") {
			if !validShortBundle(arg.text, "lwcLm") {
				return nil, errors.New("unsupported wc flag")
			}
			continue
		}
		if err := validatePathArg(arg); err != nil {
			return nil, err
		}
	}
	return texts(args), nil
}

func validateNL(args []token) ([]string, error) {
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
	}
	return texts(args), nil
}

func validateSed(args []token) ([]string, error) {
	if len(args) >= 2 && isSafeSedSubstituteScript(args[1].text) {
		for _, arg := range args[2:] {
			if isLeadingDash(arg) {
				return nil, errors.New("sed file operands cannot start with dash")
			}
			if err := validatePathArg(arg); err != nil {
				return nil, err
			}
		}
		return texts(args), nil
	}
	if len(args) < 3 || args[1].quoted || args[1].text != "-n" {
		return nil, errors.New("sed only allows -n with a numeric print script or a safe stdin substitute script")
	}
	if !isSafeSedPrintScript(args[2].text) {
		return nil, errors.New("sed only allows numeric print scripts")
	}
	for _, arg := range args[3:] {
		if isLeadingDash(arg) {
			return nil, errors.New("sed file operands cannot start with dash")
		}
		if err := validatePathArg(arg); err != nil {
			return nil, err
		}
	}
	return texts(args), nil
}

func isSafeSedPrintScript(script string) bool {
	if !strings.HasSuffix(script, "p") {
		return false
	}
	body := strings.TrimSuffix(script, "p")
	if body == "" {
		return false
	}
	parts := strings.Split(body, ",")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "0" || !isUint(part) {
			return false
		}
	}
	return true
}

func isSafeSedSubstituteScript(script string) bool {
	if len(script) < 4 || script[0] != 's' || strings.Contains(script, "\\") || !isSafeSedDelimiter(script[1]) {
		return false
	}
	parts := strings.Split(script[2:], string(script[1]))
	return len(parts) == 3 && parts[0] != "" && isSafeSedSubstituteFlags(parts[2])
}

func isSafeSedSubstituteFlags(flags string) bool {
	if flags == "" {
		return true
	}
	seen := map[rune]bool{}
	for _, r := range flags {
		if r >= '0' && r <= '9' {
			continue
		}
		if !strings.ContainsRune("gIp", r) || seen[r] {
			return false
		}
		seen[r] = true
	}
	return true
}

func isSafeSedDelimiter(c byte) bool {
	return strings.ContainsRune("#/|,:@", rune(c))
}

func validateDU(args []token) ([]string, error) {
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg.text, "--exclude=") && strings.TrimPrefix(arg.text, "--exclude=") != "" {
			continue
		}
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
		if !arg.quoted && oneOf(arg.text, "--apparent-size") {
			continue
		}
		if !arg.quoted && arg.text == "--exclude" {
			if i+1 >= len(args) || isLeadingDash(args[i+1]) {
				return nil, errors.New("du --exclude requires safe pattern")
			}
			i++
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
		if !arg.quoted && oneOf(arg.text, "-q", "-v", "--quiet", "--silent", "--verbose") {
			normalized = append(normalized, arg.text)
			continue
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
	boolLong := set("--files", "--line-number", "--ignore-case", "--smart-case", "--fixed-strings", "--word-regexp", "--hidden", "--no-ignore", "--no-heading", "--heading", "--json", "--stats", "--count", "--count-matches", "--pretty", "--with-filename", "--files-with-matches", "--files-without-match", "--follow")
	boolShort := set("-n", "-i", "-S", "-F", "-w", "-l", "-H", "-u")
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
	boolLong := set("--line-number", "--ignore-case", "--fixed-strings", "--extended-regexp", "--word-regexp", "--invert-match", "--files-with-matches", "--files-without-match", "--count", "--with-filename", "--no-filename", "--only-matching", "--quiet", "--silent")
	positionals := 0
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if _, ok := boolLong[arg.text]; ok && !arg.quoted {
			continue
		}
		if arg.text == "-C" || arg.text == "-A" || arg.text == "-B" || arg.text == "-m" || arg.text == "--max-count" {
			if i+1 >= len(args) || args[i+1].quoted || !isUint(args[i+1].text) {
				return nil, errors.New("grep numeric flag requires numeric argument")
			}
			i++
			continue
		}
		if hasLongNumeric(arg.text, "--context=", "--after-context=", "--before-context=", "--max-count=") && !arg.quoted {
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
			if !validShortBundle(arg.text, "rRniIFEwvlLHhcqso") {
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
	prevPrimary := false
	expectPrimary := false
	groups := 0
	seenExpr := false
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		switch arg.text {
		case "(":
			groups++
			prevPrimary = false
			expectPrimary = true
			seenExpr = true
		case ")":
			if groups == 0 || !prevPrimary {
				return nil, errors.New("empty or unmatched find group")
			}
			groups--
			prevPrimary = true
			expectPrimary = false
		case "-o", "-a":
			if !prevPrimary {
				return nil, errors.New("find boolean predicate must be between safe predicates")
			}
			prevPrimary = false
			expectPrimary = true
			seenExpr = true
		case "-not":
			prevPrimary = false
			expectPrimary = true
			seenExpr = true
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
			prevPrimary = true
			expectPrimary = false
			seenExpr = true
		case "-name", "-iname", "-path", "-ipath", "-regex", "-iregex":
			if i+1 >= len(args) || isLeadingDash(args[i+1]) || args[i+1].text == "" {
				return nil, errors.New("find pattern predicate requires safe pattern")
			}
			if err := validatePathArg(args[i+1]); err != nil {
				return nil, err
			}
			i++
			prevPrimary = true
			expectPrimary = false
			seenExpr = true
		case "-size", "-mtime", "-mmin", "-ctime", "-cmin", "-atime", "-amin", "-inum", "-links":
			if i+1 >= len(args) || args[i+1].quoted || !isFindNumeric(args[i+1].text) {
				return nil, errors.New("find numeric predicate requires safe number")
			}
			i++
			prevPrimary = true
			expectPrimary = false
			seenExpr = true
		case "-newer", "-samefile":
			if i+1 >= len(args) || isLeadingDash(args[i+1]) {
				return nil, errors.New("find path predicate requires safe path")
			}
			if err := validatePathArg(args[i+1]); err != nil {
				return nil, err
			}
			i++
			prevPrimary = true
			expectPrimary = false
			seenExpr = true
		case "-user", "-group":
			if i+1 >= len(args) || !isSafeFindName(args[i+1]) {
				return nil, errors.New("find user/group predicate requires safe name")
			}
			i++
			prevPrimary = true
			expectPrimary = false
			seenExpr = true
		case "-perm":
			if i+1 >= len(args) || args[i+1].quoted || !isSafeFindPerm(args[i+1].text) {
				return nil, errors.New("find perm predicate requires safe mode")
			}
			i++
			prevPrimary = true
			expectPrimary = false
			seenExpr = true
		case "-printf":
			if i+1 >= len(args) || !isSafeFindPrintf(args[i+1].text) {
				return nil, errors.New("find printf requires safe format")
			}
			i++
			prevPrimary = true
			expectPrimary = false
			seenExpr = true
		case "-empty", "-readable", "-writable", "-executable", "-prune", "-quit", "-print", "-print0", "-ls":
			prevPrimary = true
			expectPrimary = false
			seenExpr = true
		default:
			if strings.HasPrefix(arg.text, "-") {
				return nil, errors.New("unsupported find predicate")
			}
			if seenExpr || expectPrimary {
				return nil, errors.New("find paths must precede predicates")
			}
			if err := validatePathArg(arg); err != nil {
				return nil, err
			}
		}
	}
	if groups != 0 {
		return nil, errors.New("unclosed find group")
	}
	if expectPrimary {
		return nil, errors.New("find boolean predicate must be between safe predicates")
	}
	return texts(args), nil
}

func isFindNumeric(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '+' || s[0] == '-' {
		s = s[1:]
	}
	if s == "" {
		return false
	}
	if last := s[len(s)-1]; strings.ContainsRune("bcwkMG", rune(last)) {
		s = s[:len(s)-1]
	}
	return isUint(s)
}

func isSafeFindName(arg token) bool {
	if arg.quoted || arg.text == "" || strings.HasPrefix(arg.text, "-") || strings.Contains(arg.text, "/") {
		return false
	}
	for _, r := range arg.text {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("._-", r):
		default:
			return false
		}
	}
	return true
}

func isSafeFindPerm(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !strings.ContainsRune("01234567ugoarwxXst,+-/=", r) {
			return false
		}
	}
	return true
}

func isSafeFindPrintf(value string) bool {
	if value == "" || strings.ContainsAny(value, "\x00\r") {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			if isControlByte(value[i]) {
				return false
			}
			continue
		}
		i++
		if i >= len(value) || !strings.ContainsRune("n0\\", rune(value[i])) {
			return false
		}
	}
	return true
}

func validateGit(args []token) ([]string, error) {
	prefix, subArgs, err := normalizeGitInvocation(args)
	if err != nil {
		return nil, err
	}
	validators := map[string]segmentValidator{
		"status": validateGitStatus, "diff": validateGitDiff, "log": validateGitLog, "branch": validateGitBranch,
		"rev-parse": validateGitRevParse, "ls-files": validateGitLsFiles, "remote": validateGitRemote, "tag": validateGitTag,
		"show": validateGitShow, "grep": validateGitGrep, "ls-tree": validateGitLsTree, "merge-base": validateGitMergeBase,
		"shortlog": validateGitShortlog, "rev-list": validateGitRevList, "reflog": validateGitReflog, "stash": validateGitStash,
		"describe": validateGitDescribe, "show-ref": validateGitShowRef, "symbolic-ref": validateGitSymbolicRef,
		"worktree": validateGitWorktree, "submodule": validateGitSubmodule, "count-objects": validateGitCountObjects,
		"cat-file": validateGitCatFile, "blame": validateGitBlame, "archive": validateGitArchive,
		"verify-commit": validateGitVerifyObject, "verify-tag": validateGitVerifyObject, "var": validateGitVar,
		"config": validateGitConfig, "help": validateGitHelp, "bundle": validateGitBundle,
		"notes": validateGitNotes, "rerere": validateGitRerere,
	}
	validator, ok := validators[subArgs[1].text]
	if !ok {
		return nil, errors.New("unsupported git command")
	}
	normalized, err := validator(subArgs)
	if err != nil {
		return nil, err
	}
	return append(prefix, normalized[1:]...), nil
}

func normalizeGitInvocation(args []token) ([]string, []token, error) {
	if len(args) < 2 {
		return nil, nil, errors.New("unsupported git invocation")
	}
	prefix := []string{"git"}
	i := 1
	for i < len(args) && args[i].text == "-C" && !args[i].quoted {
		if i+1 >= len(args) {
			return nil, nil, errors.New("git -C requires a path")
		}
		if err := validatePathArg(args[i+1]); err != nil {
			return nil, nil, err
		}
		prefix = append(prefix, "-C", args[i+1].text)
		i += 2
	}
	if i >= len(args) || args[i].quoted || strings.HasPrefix(args[i].text, "-") {
		return nil, nil, errors.New("unsupported git invocation")
	}
	subArgs := make([]token, 1, len(args)-i+1)
	subArgs[0] = args[0]
	subArgs = append(subArgs, args[i:]...)
	return prefix, subArgs, nil
}

func validateGitBranch(args []token) ([]string, error) {
	for i := 2; i < len(args); i++ {
		a := args[i]
		if a.quoted {
			return nil, errors.New("quoted git branch flag is not allowed")
		}
		if oneOf(a.text, "--show-current", "-a", "--all", "-r", "--remote", "--remotes", "-v", "-vv", "--verbose", "--merged", "--no-merged") {
			continue
		}
		if oneOf(a.text, "--contains", "--points-at") {
			if i+1 >= len(args) || args[i+1].quoted || !isSafeGitObject(args[i+1].text) {
				return nil, errors.New("git branch flag requires safe object")
			}
			i++
			continue
		}
		return nil, errors.New("unsupported git branch arguments")
	}
	return texts(args), nil
}

func validateGitRemote(args []token) ([]string, error) {
	if len(args) == 2 || (len(args) == 3 && !args[2].quoted && args[2].text == "-v") {
		return texts(args), nil
	}
	return nil, errors.New("unsupported git remote arguments")
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
	pathspecs := 0
	explicitPathspecs := false
	afterSep := false
	for i := 2; i < len(args); i++ {
		a := args[i]
		if !afterSep && a.text == "--" && !a.quoted {
			afterSep = true
			explicitPathspecs = true
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
			if err := validateGitPathspec(a); err != nil {
				return nil, err
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
		pathspecs++
	}
	if explicitPathspecs && pathspecs == 0 {
		return nil, errors.New("git diff pathspecs require --")
	}
	return withGitDiffHardening("diff", args[2:]), nil
}

func withGitDiffHardening(command string, tail []token) []string {
	out := []string{"git", command, "--no-ext-diff", "--no-textconv"}
	for _, arg := range tail {
		if !arg.quoted && oneOf(arg.text, "--no-ext-diff", "--no-textconv") {
			continue
		}
		out = append(out, arg.text)
	}
	return out
}

func isSafeGitDiffRev(value string) bool {
	return isSafeGitName(value, "._/@+-^~")
}

func isSafeGitObject(value string) bool {
	return isSafeGitName(value, "._/@+-^~:")
}

func isSafeGitName(value, extra string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.Contains(value, ":/") || strings.Contains(value, "@{") {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune(extra, r):
		default:
			return false
		}
	}
	return true
}

func validateGitLog(args []token) ([]string, error) {
	reject := set("--numstat", "--name-only", "--name-status", "--summary", "--raw", "-p", "--patch", "--ext-diff", "--textconv")
	needsHardening := false
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
			if a.text == "--stat" && !a.quoted {
				needsHardening = true
				continue
			}
			if oneOf(a.text, "--oneline", "--graph", "--decorate", "--all", "--left-right", "--cherry-pick", "--reverse", "--merges", "--no-merges", "--first-parent", "--topo-order", "--date-order", "--author-date-order") && !a.quoted {
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
			if isDashCount(a.text) && !a.quoted {
				continue
			}
			if strings.HasPrefix(a.text, "--max-count=") && isUint(strings.TrimPrefix(a.text, "--max-count=")) && !a.quoted {
				continue
			}
			if oneOf(a.text, "--since", "--until", "--author", "--grep", "--format", "--pretty", "--date") {
				if i+1 >= len(args) || !isSafeGitLogValue(a.text, args[i+1].text) {
					return nil, errors.New("git log flag requires safe value")
				}
				i++
				continue
			}
			if hasGitLogValue(a.text, "--since=", "--until=", "--author=", "--grep=", "--format=", "--pretty=", "--date=") {
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
	if needsHardening {
		return withGitDiffHardening("log", args[2:]), nil
	}
	return texts(args), nil
}

func hasGitLogValue(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return isSafeGitLogValue(strings.TrimSuffix(prefix, "="), strings.TrimPrefix(value, prefix))
		}
	}
	return false
}

func isSafeGitLogValue(flag, value string) bool {
	if value == "" || strings.ContainsAny(value, "\x00\n\r") {
		return false
	}
	if flag == "--date" {
		return oneOf(value, "short", "iso", "iso-strict", "relative", "local", "default", "raw", "unix", "human")
	}
	if flag == "--format" {
		return isSafeGitFormat(value)
	}
	if flag == "--pretty" {
		if strings.HasPrefix(value, "format:") {
			return isSafeGitFormat(strings.TrimPrefix(value, "format:"))
		}
		return oneOf(value, "medium", "oneline", "short", "full", "fuller", "reference", "email")
	}
	return true
}

func isSafeGitFormat(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] != '%' {
			if isControlByte(value[i]) {
				return false
			}
			continue
		}
		i++
		if i >= len(value) {
			return false
		}
		if value[i] == '%' {
			continue
		}
		if strings.ContainsRune("CGxnw<>", rune(value[i])) {
			return false
		}
		if value[i] == '(' || value[i] == ')' {
			return false
		}
	}
	return true
}

func validateGitRevParse(args []token) ([]string, error) {
	if len(args) == 3 && !args[2].quoted && oneOf(args[2].text, "--show-toplevel", "--git-dir", "--git-common-dir", "--is-inside-work-tree", "--is-bare-repository", "--show-prefix", "--show-cdup", "--show-superproject-working-tree") {
		return texts(args), nil
	}
	if len(args) == 3 && isSafeGitObject(args[2].text) {
		return texts(args), nil
	}
	if len(args) == 4 && !args[2].quoted && !args[3].quoted && args[2].text == "--abbrev-ref" && args[3].text == "HEAD" {
		return texts(args), nil
	}
	if len(args) == 4 && !args[2].quoted && !args[3].quoted && oneOf(args[2].text, "--short", "--verify") && isSafeGitObject(args[3].text) {
		return texts(args), nil
	}
	return nil, errors.New("unsupported git rev-parse arguments")
}

func validateGitLsFiles(args []token) ([]string, error) {
	allowed := set("--stage", "--deleted", "--modified", "--others", "--exclude-standard", "--cached", "--ignored", "--killed", "--unmerged", "--directory", "--deduplicate", "--error-unmatch", "--full-name", "--recurse-submodules", "-z", "-c", "-d", "-m", "-o", "-i", "-s", "-u", "-k")
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
	if len(args) >= 3 && !args[2].quoted && oneOf(args[2].text, "--list", "-l") {
		for _, a := range args[3:] {
			if isLeadingDash(a) {
				return nil, errors.New("leading-dash tag pattern is not allowed")
			}
		}
		return texts(args), nil
	}
	return nil, errors.New("unsupported git tag arguments")
}

func validateGitShow(args []token) ([]string, error) {
	boolFlags := set("--no-ext-diff", "--no-textconv", "--stat", "--name-only", "--name-status", "--oneline", "--decorate", "--summary")
	objects := 0
	for i := 2; i < len(args); i++ {
		a := args[i]
		if _, ok := boolFlags[a.text]; ok && !a.quoted {
			continue
		}
		if strings.HasPrefix(a.text, "--unified=") && !a.quoted && isUint(strings.TrimPrefix(a.text, "--unified=")) {
			continue
		}
		if strings.HasPrefix(a.text, "--format=") && isSafeGitLogValue("--format", strings.TrimPrefix(a.text, "--format=")) {
			continue
		}
		if strings.HasPrefix(a.text, "--pretty=") && isSafeGitLogValue("--pretty", strings.TrimPrefix(a.text, "--pretty=")) {
			continue
		}
		if strings.HasPrefix(a.text, "-") {
			return nil, errors.New("unsupported git show flag")
		}
		if !isSafeGitObject(a.text) {
			return nil, errors.New("unsupported git show object")
		}
		objects++
	}
	if objects > 1 {
		return nil, errors.New("git show allows one object")
	}
	return withGitDiffHardening("show", args[2:]), nil
}

func validateGitGrep(args []token) ([]string, error) {
	boolFlags := set("-n", "-i", "-I", "-F", "-w", "-l", "-L", "--line-number", "--ignore-case", "--fixed-strings", "--word-regexp", "--files-with-matches", "--files-without-match", "--count")
	positionals := 0
	afterSep := false
	for i := 2; i < len(args); i++ {
		a := args[i]
		if !afterSep && a.text == "--" && !a.quoted {
			afterSep = true
			continue
		}
		if afterSep {
			if err := validateGitPathspec(a); err != nil {
				return nil, err
			}
			continue
		}
		if _, ok := boolFlags[a.text]; ok && !a.quoted {
			continue
		}
		if oneOf(a.text, "-C", "-A", "-B", "--context", "--after-context", "--before-context") {
			if i+1 >= len(args) || args[i+1].quoted || !isUint(args[i+1].text) {
				return nil, errors.New("git grep context flag requires number")
			}
			i++
			continue
		}
		if hasLongNumeric(a.text, "--context=", "--after-context=", "--before-context=") && !a.quoted {
			continue
		}
		if strings.HasPrefix(a.text, "-") {
			return nil, errors.New("unsupported git grep flag")
		}
		if positionals > 0 && !isSafeGitObject(a.text) {
			return nil, errors.New("unsupported git grep revision")
		}
		positionals++
	}
	if positionals == 0 {
		return nil, errors.New("git grep requires a pattern")
	}
	return texts(args), nil
}

func validateGitLsTree(args []token) ([]string, error) {
	boolFlags := set("-r", "-d", "-t", "-l", "-z", "--name-only", "--long")
	seenTree := false
	afterSep := false
	pathspecs := 0
	for i := 2; i < len(args); i++ {
		a := args[i]
		if !seenTree {
			if _, ok := boolFlags[a.text]; ok && !a.quoted {
				continue
			}
			if strings.HasPrefix(a.text, "-") || !isSafeGitObject(a.text) {
				return nil, errors.New("unsupported git ls-tree tree")
			}
			seenTree = true
			continue
		}
		if !afterSep {
			if a.text == "--" && !a.quoted {
				afterSep = true
				continue
			}
			return nil, errors.New("git ls-tree pathspecs require --")
		}
		if err := validateGitPathspec(a); err != nil {
			return nil, err
		}
		pathspecs++
	}
	if !seenTree {
		return nil, errors.New("git ls-tree requires a tree")
	}
	if afterSep && pathspecs == 0 {
		return nil, errors.New("git ls-tree pathspecs require --")
	}
	return texts(args), nil
}

func validateGitMergeBase(args []token) ([]string, error) {
	revs := 0
	for i := 2; i < len(args); i++ {
		a := args[i]
		if !a.quoted && a.text == "--all" {
			continue
		}
		if a.quoted || !isSafeGitObject(a.text) {
			return nil, errors.New("unsupported git merge-base revision")
		}
		revs++
	}
	if revs < 2 {
		return nil, errors.New("git merge-base requires two revisions")
	}
	return texts(args), nil
}

func validateGitArchive(args []token) ([]string, error) {
	if len(args) == 3 && !args[2].quoted && args[2].text == "--list" {
		return texts(args), nil
	}
	return nil, errors.New("unsupported git archive arguments")
}

func validateGitVerifyObject(args []token) ([]string, error) {
	if len(args) < 3 {
		return nil, errors.New("git verify requires an object")
	}
	for _, a := range args[2:] {
		if a.quoted || !isSafeGitObject(a.text) {
			return nil, errors.New("unsupported git verify object")
		}
	}
	return texts(args), nil
}

func validateGitVar(args []token) ([]string, error) {
	if len(args) == 3 && !args[2].quoted && args[2].text == "-l" {
		return texts(args), nil
	}
	if len(args) == 3 && !args[2].quoted && isSafeGitVarName(args[2].text) {
		return texts(args), nil
	}
	return nil, errors.New("unsupported git var arguments")
}

func isSafeGitVarName(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !(r >= 'A' && r <= 'Z' || r == '_') {
			return false
		}
	}
	return true
}

func validateGitConfig(args []token) ([]string, error) {
	if len(args) == 3 && !args[2].quoted && oneOf(args[2].text, "--list", "-l") {
		return texts(args), nil
	}
	if len(args) == 4 && !args[2].quoted && oneOf(args[2].text, "--get", "--get-regexp") && isSafeGitConfigKey(args[3].text) {
		return texts(args), nil
	}
	return nil, errors.New("unsupported git config arguments")
}

func isSafeGitConfigKey(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "\x00\n\r=/") {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("._-*", r):
		default:
			return false
		}
	}
	return true
}

func validateGitHelp(args []token) ([]string, error) {
	if len(args) != 3 || args[2].quoted || !isSafeCommandLookupName(args[2]) {
		return nil, errors.New("unsupported git help arguments")
	}
	return texts(args), nil
}

func validateGitBundle(args []token) ([]string, error) {
	if len(args) < 4 || args[2].quoted || args[2].text != "list-heads" {
		return nil, errors.New("unsupported git bundle arguments")
	}
	if err := validatePathArg(args[3]); err != nil {
		return nil, err
	}
	for _, a := range args[4:] {
		if a.quoted || !isSafeGitObject(a.text) {
			return nil, errors.New("unsupported git bundle ref")
		}
	}
	return texts(args), nil
}

func validateGitNotes(args []token) ([]string, error) {
	if len(args) == 3 && !args[2].quoted && args[2].text == "list" {
		return texts(args), nil
	}
	return nil, errors.New("unsupported git notes arguments")
}

func validateGitRerere(args []token) ([]string, error) {
	if len(args) == 3 && !args[2].quoted && args[2].text == "status" {
		return texts(args), nil
	}
	return nil, errors.New("unsupported git rerere arguments")
}

func validateGitShortlog(args []token) ([]string, error) {
	for i := 2; i < len(args); i++ {
		a := args[i]
		if a.quoted && strings.HasPrefix(a.text, "-") {
			return nil, errors.New("quoted git shortlog flag is not allowed")
		}
		if strings.HasPrefix(a.text, "-") {
			if !validShortBundle(a.text, "sne") {
				return nil, errors.New("unsupported git shortlog flag")
			}
			continue
		}
		if !isSafeGitObject(a.text) {
			return nil, errors.New("unsupported git shortlog revision")
		}
	}
	return texts(args), nil
}

func validateGitRevList(args []token) ([]string, error) {
	if len(args) == 4 && !args[2].quoted && !args[3].quoted && args[2].text == "--count" && args[3].text == "--all" {
		return texts(args), nil
	}
	return nil, errors.New("unsupported git rev-list arguments")
}

func validateGitReflog(args []token) ([]string, error) {
	if len(args) == 2 {
		return texts(args), nil
	}
	return nil, errors.New("unsupported git reflog arguments")
}

func validateGitStash(args []token) ([]string, error) {
	if len(args) == 3 && !args[2].quoted && args[2].text == "list" {
		return texts(args), nil
	}
	if len(args) == 3 && !args[2].quoted && args[2].text == "show" {
		return []string{"git", "stash", "show", "--no-ext-diff", "--no-textconv"}, nil
	}
	return nil, errors.New("unsupported git stash arguments")
}

func validateGitDescribe(args []token) ([]string, error) {
	objects := 0
	for i := 2; i < len(args); i++ {
		a := args[i]
		if a.quoted && strings.HasPrefix(a.text, "-") {
			return nil, errors.New("quoted git describe flag is not allowed")
		}
		if oneOf(a.text, "--tags", "--always", "--dirty", "--all", "--contains", "--abbrev") && !a.quoted {
			continue
		}
		if strings.HasPrefix(a.text, "--abbrev=") && !a.quoted && isUint(strings.TrimPrefix(a.text, "--abbrev=")) {
			continue
		}
		if strings.HasPrefix(a.text, "-") {
			return nil, errors.New("unsupported git describe flag")
		}
		if !isSafeGitObject(a.text) {
			return nil, errors.New("unsupported git describe object")
		}
		objects++
	}
	if objects > 1 {
		return nil, errors.New("git describe allows one object")
	}
	return texts(args), nil
}

func validateGitShowRef(args []token) ([]string, error) {
	for i := 2; i < len(args); i++ {
		a := args[i]
		if a.quoted && strings.HasPrefix(a.text, "-") {
			return nil, errors.New("quoted git show-ref flag is not allowed")
		}
		if oneOf(a.text, "--head", "--heads", "--tags", "--verify", "--hash", "--abbrev", "-d") && !a.quoted {
			continue
		}
		if strings.HasPrefix(a.text, "--hash=") && !a.quoted && isUint(strings.TrimPrefix(a.text, "--hash=")) {
			continue
		}
		if strings.HasPrefix(a.text, "--abbrev=") && !a.quoted && isUint(strings.TrimPrefix(a.text, "--abbrev=")) {
			continue
		}
		if strings.HasPrefix(a.text, "-") || !isSafeGitObject(a.text) {
			return nil, errors.New("unsupported git show-ref argument")
		}
	}
	return texts(args), nil
}

func validateGitSymbolicRef(args []token) ([]string, error) {
	if len(args) == 3 && !args[2].quoted && isSafeGitObject(args[2].text) {
		return texts(args), nil
	}
	if len(args) == 4 && !args[2].quoted && !args[3].quoted && args[2].text == "--short" && isSafeGitObject(args[3].text) {
		return texts(args), nil
	}
	return nil, errors.New("unsupported git symbolic-ref arguments")
}

func validateGitWorktree(args []token) ([]string, error) {
	if len(args) == 3 && !args[2].quoted && args[2].text == "list" {
		return texts(args), nil
	}
	if len(args) == 4 && !args[2].quoted && !args[3].quoted && args[2].text == "list" && args[3].text == "--porcelain" {
		return texts(args), nil
	}
	return nil, errors.New("unsupported git worktree arguments")
}

func validateGitSubmodule(args []token) ([]string, error) {
	if len(args) < 3 || args[2].quoted || args[2].text != "status" {
		return nil, errors.New("unsupported git submodule arguments")
	}
	for _, a := range args[3:] {
		if a.quoted || !oneOf(a.text, "--recursive", "--cached") {
			return nil, errors.New("unsupported git submodule status flag")
		}
	}
	return texts(args), nil
}

func validateGitCountObjects(args []token) ([]string, error) {
	for _, a := range args[2:] {
		if a.quoted || !oneOf(a.text, "-v", "-H", "-vH") {
			return nil, errors.New("unsupported git count-objects flag")
		}
	}
	return texts(args), nil
}

func validateGitCatFile(args []token) ([]string, error) {
	if len(args) == 4 && !args[2].quoted && oneOf(args[2].text, "-t", "-s", "-p", "-e") && isSafeGitObject(args[3].text) {
		return texts(args), nil
	}
	if len(args) == 3 && !args[2].quoted && oneOf(args[2].text, "--batch-check", "--batch") {
		return texts(args), nil
	}
	return nil, errors.New("unsupported git cat-file arguments")
}

func validateGitBlame(args []token) ([]string, error) {
	paths := 0
	for i := 2; i < len(args); i++ {
		a := args[i]
		if a.quoted && strings.HasPrefix(a.text, "-") {
			return nil, errors.New("quoted git blame flag is not allowed")
		}
		if oneOf(a.text, "-w", "-M", "-C", "--line-porcelain", "--porcelain", "--show-name", "--show-number") && !a.quoted {
			continue
		}
		if a.text == "-L" && !a.quoted {
			if i+1 >= len(args) || args[i+1].quoted || !isSafeBlameRange(args[i+1].text) {
				return nil, errors.New("git blame -L requires safe range")
			}
			i++
			continue
		}
		if strings.HasPrefix(a.text, "-") {
			return nil, errors.New("unsupported git blame flag")
		}
		if err := validateGitPathspec(a); err != nil {
			return nil, err
		}
		paths++
	}
	if paths == 0 {
		return nil, errors.New("git blame requires a path")
	}
	return withGitBlameHardening(args[2:]), nil
}

func withGitBlameHardening(tail []token) []string {
	out := []string{"git", "blame", "--no-textconv"}
	for _, arg := range tail {
		if !arg.quoted && arg.text == "--no-textconv" {
			continue
		}
		out = append(out, arg.text)
	}
	return out
}

func isSafeBlameRange(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
		case strings.ContainsRune(",+-", r):
		default:
			return false
		}
	}
	return true
}

func validateSort(args []token) ([]string, error) {
	allowedLong := set("--reverse", "--numeric-sort", "--ignore-case", "--unique", "--version-sort", "--month-sort", "--human-numeric-sort", "--stable", "--random-sort", "--debug")
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if _, ok := allowedLong[arg.text]; ok && !arg.quoted {
			continue
		}
		if oneOf(arg.text, "-k", "--key", "-t", "--field-separator", "--buffer-size") && !arg.quoted {
			if i+1 >= len(args) || args[i+1].text == "" {
				return nil, errors.New("sort flag requires safe argument")
			}
			i++
			continue
		}
		if strings.HasPrefix(arg.text, "--key=") || strings.HasPrefix(arg.text, "--field-separator=") || strings.HasPrefix(arg.text, "--buffer-size=") {
			if strings.TrimPrefix(arg.text[strings.IndexByte(arg.text, '='):], "=") == "" {
				return nil, errors.New("sort flag requires argument")
			}
			continue
		}
		if strings.HasPrefix(arg.text, "-") {
			if !validShortBundle(arg.text, "fhmnruVsR") {
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

func validateUniq(args []token) ([]string, error) {
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if strings.HasPrefix(arg.text, "-") {
			if !oneOf(arg.text, "-c", "--count", "-d", "--repeated", "-u", "--unique", "-i", "--ignore-case") {
				return nil, errors.New("unsupported uniq flag")
			}
			continue
		}
		if err := validatePathArg(arg); err != nil {
			return nil, err
		}
	}
	return texts(args), nil
}

func validateStat(args []token) ([]string, error) {
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if oneOf(arg.text, "-f", "-c", "--format") && !arg.quoted {
			if i+1 >= len(args) || args[i+1].homeExpanded || !isSafeStatFormat(args[i+1].text) {
				return nil, errors.New("stat format flag requires safe format")
			}
			i++
			continue
		}
		if strings.HasPrefix(arg.text, "--format=") {
			if !isSafeStatFormat(arg.text[strings.IndexByte(arg.text, '=')+1:]) {
				return nil, errors.New("stat format flag requires safe format")
			}
			continue
		}
		if strings.HasPrefix(arg.text, "--printf") {
			return nil, errors.New("stat --printf is not allowed")
		}
		if strings.HasPrefix(arg.text, "-") {
			if !validShortBundle(arg.text, "Llxqst") {
				return nil, errors.New("unsupported stat flag")
			}
			continue
		}
		if err := validatePathArg(arg); err != nil {
			return nil, err
		}
	}
	return texts(args), nil
}

func isSafeStatFormat(value string) bool {
	return value != "" && !strings.ContainsAny(value, "\x00\n\r\\")
}

func validateReadlinkRealpath(args []token) ([]string, error) {
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if strings.HasPrefix(arg.text, "-") {
			if !validShortBundle(arg.text, "femqs") {
				return nil, errors.New("unsupported path resolution flag")
			}
			continue
		}
		if err := validatePathArg(arg); err != nil {
			return nil, err
		}
	}
	return texts(args), nil
}

func validateTree(args []token) ([]string, error) {
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if oneOf(arg.text, "-L", "-I", "-P") && !arg.quoted {
			if i+1 >= len(args) || args[i+1].text == "" || isLeadingDash(args[i+1]) {
				return nil, errors.New("tree flag requires safe argument")
			}
			i++
			continue
		}
		if arg.text == "--fromfile" && !arg.quoted {
			continue
		}
		if strings.HasPrefix(arg.text, "--") && !arg.quoted {
			if !oneOf(arg.text, "--dirsfirst", "--noreport", "--charset=ascii", "--charset=utf-8") {
				return nil, errors.New("unsupported tree flag")
			}
			continue
		}
		if strings.HasPrefix(arg.text, "-") {
			if strings.ContainsRune(arg.text, 'o') || !validShortBundle(arg.text, "aCdfFhinsuDp") {
				return nil, errors.New("unsupported tree flag")
			}
			continue
		}
		if err := validatePathArg(arg); err != nil {
			return nil, err
		}
	}
	return texts(args), nil
}

func validateCut(args []token) ([]string, error) {
	selectors := 0
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if oneOf(arg.text, "-b", "-c", "-f", "-d") && !arg.quoted {
			if i+1 >= len(args) || args[i+1].homeExpanded || args[i+1].text == "" {
				return nil, errors.New("cut flag requires safe argument")
			}
			if oneOf(arg.text, "-b", "-c", "-f") {
				selectors++
			}
			i++
			continue
		}
		if strings.HasPrefix(arg.text, "--bytes=") || strings.HasPrefix(arg.text, "--characters=") || strings.HasPrefix(arg.text, "--fields=") {
			if arg.text[strings.IndexByte(arg.text, '=')+1:] == "" {
				return nil, errors.New("cut selector requires argument")
			}
			selectors++
			continue
		}
		if strings.HasPrefix(arg.text, "--delimiter=") || strings.HasPrefix(arg.text, "--output-delimiter=") {
			if arg.homeExpanded || arg.text[strings.IndexByte(arg.text, '=')+1:] == "" {
				return nil, errors.New("cut delimiter requires argument")
			}
			continue
		}
		if strings.HasPrefix(arg.text, "-") {
			if !oneOf(arg.text, "-s", "--only-delimited", "--complement") {
				return nil, errors.New("unsupported cut flag")
			}
			continue
		}
		if err := validatePathArg(arg); err != nil {
			return nil, err
		}
	}
	if selectors == 0 {
		return nil, errors.New("cut requires a byte, character, or field selector")
	}
	return texts(args), nil
}

func validateTr(args []token) ([]string, error) {
	sets := 0
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if !arg.quoted && strings.HasPrefix(arg.text, "-") {
			if !validShortBundle(arg.text, "dsct") {
				return nil, errors.New("unsupported tr flag")
			}
			continue
		}
		if arg.homeExpanded || strings.ContainsAny(arg.text, "\x00\n\r\\") {
			return nil, errors.New("unsafe tr set")
		}
		sets++
	}
	if sets == 0 || sets > 2 {
		return nil, errors.New("tr requires one or two sets")
	}
	return texts(args), nil
}

func validateBaseDirName(args []token) ([]string, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("%s requires one path operand", args[0].text)
	}
	for _, arg := range args[1:] {
		if isLeadingDash(arg) {
			return nil, errors.New("leading-dash path operand is not allowed")
		}
		if err := validatePathArg(arg); err != nil {
			return nil, err
		}
	}
	return texts(args), nil
}

func validateTest(args []token) ([]string, error) {
	if len(args) != 3 || args[1].quoted || !oneOf(args[1].text, "-f", "-d", "-e", "-s", "-r", "-w", "-x", "-L", "-u", "-g", "-k", "-O", "-G", "-N") {
		return nil, errors.New("test only allows one safe file predicate")
	}
	if err := validatePathArg(args[2]); err != nil {
		return nil, err
	}
	return texts(args), nil
}

func validateJQ(args []token) ([]string, error) {
	filters := 0
	programFromFile := false
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg.quoted && strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("quoted leading-dash operand is not allowed")
		}
		if !arg.quoted && oneOf(arg.text, "-r", "--raw-output", "-c", "--compact-output", "-M", "--monochrome-output", "-S", "--sort-keys", "-e", "--exit-status", "-s", "--slurp", "-n", "--null-input") {
			continue
		}
		if !arg.quoted && oneOf(arg.text, "--arg", "--argjson") {
			if i+2 >= len(args) || !isSafeJQName(args[i+1]) || strings.ContainsAny(args[i+2].text, "\x00\n\r") {
				return nil, errors.New("jq arg requires safe name and value")
			}
			i += 2
			continue
		}
		if !arg.quoted && oneOf(arg.text, "--slurpfile", "--rawfile") {
			if i+2 >= len(args) || !isSafeJQName(args[i+1]) || isLeadingDash(args[i+2]) {
				return nil, errors.New("jq file arg requires safe name and path")
			}
			if err := validatePathArg(args[i+2]); err != nil {
				return nil, err
			}
			i += 2
			continue
		}
		if !arg.quoted && oneOf(arg.text, "-L", "--from-file", "-f") {
			if i+1 >= len(args) || isLeadingDash(args[i+1]) {
				return nil, errors.New("jq file flag requires safe path")
			}
			if err := validatePathArg(args[i+1]); err != nil {
				return nil, err
			}
			if oneOf(arg.text, "--from-file", "-f") {
				programFromFile = true
			}
			i++
			continue
		}
		if strings.HasPrefix(arg.text, "-") {
			return nil, errors.New("unsupported jq flag")
		}
		if filters == 0 && !programFromFile {
			if arg.homeExpanded || !isSafeJQFilter(arg.text) {
				return nil, errors.New("unsafe jq filter")
			}
			filters++
			continue
		}
		if err := validatePathArg(arg); err != nil {
			return nil, err
		}
	}
	if filters == 0 && !programFromFile {
		return nil, errors.New("jq requires a filter")
	}
	return texts(args), nil
}

func isSafeJQFilter(value string) bool {
	if value == "" || strings.ContainsAny(value, "\x00\n\r@$;`\\") {
		return false
	}
	for _, word := range []string{"env", "input", "inputs", "include", "import", "module", "debug", "halt", "halt_error"} {
		if value == word || strings.Contains(value, word+" ") || strings.Contains(value, word+"(") {
			return false
		}
	}
	return true
}

func isSafeJQName(arg token) bool {
	if arg.quoted || arg.text == "" || strings.HasPrefix(arg.text, "-") {
		return false
	}
	for _, r := range arg.text {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
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
	if hasHomeExpanded(args[1:]) {
		return nil, errors.New("home expansion is only allowed for path operands")
	}
	if args[0].text == "echo" {
		allowDash := len(args) > 1 && !args[1].quoted && args[1].text == "--"
		for i, arg := range args[1:] {
			if allowDash && i == 0 {
				continue
			}
			if !allowDash && !arg.quoted && strings.HasPrefix(arg.text, "-") {
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
	for i := 1; i < len(args); i++ {
		a := args[i]
		if !a.quoted && a.text == "-u" && !seenUTC {
			seenUTC = true
			continue
		}
		if strings.HasPrefix(a.text, "+") && !seenFormat {
			seenFormat = true
			continue
		}
		if !a.quoted && (a.text == "-d" || a.text == "--date") {
			if i+1 >= len(args) || strings.ContainsAny(args[i+1].text, "\x00\n\r") {
				return nil, errors.New("date flag requires safe value")
			}
			i++
			continue
		}
		if !a.quoted && strings.HasPrefix(a.text, "--date=") && strings.TrimPrefix(a.text, "--date=") != "" {
			continue
		}
		if !a.quoted && a.text == "-r" {
			if i+1 >= len(args) || isLeadingDash(args[i+1]) {
				return nil, errors.New("date -r requires safe path")
			}
			if err := validatePathArg(args[i+1]); err != nil {
				return nil, err
			}
			i++
			continue
		}
		if !a.quoted && oneOf(a.text, "-I", "-Idate", "-Ihours", "-Iminutes", "-Iseconds", "-Ins") {
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
	if s[0] == '+' || s[0] == '-' {
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
