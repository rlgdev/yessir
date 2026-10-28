package hook

import (
	"errors"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Rule is one entry of a yessir ask/deny/neverApprove list, written in the
// subset of Claude Code's permission-rule syntax that yessir understands:
//
//	Tool                       every use of Tool
//	Tool(*)                    same
//	Bash(git push *)           command (or any sub-command) starting with "git push"
//	Bash(git push:*)           same, ":*" suffix form
//	Bash(npm run build)        exact sub-command
//	Bash(ls*)                  raw prefix, no word boundary
//	Edit(src/**) Write(...)    file_path glob; Read/NotebookEdit the same
//	WebFetch(domain:host)      url host, "*.host" matches sub-domains too
//	mcp__server                every tool of that MCP server
//	mcp__server__*             same
//	mcp__server__tool          one MCP tool
type Rule struct {
	Raw     string
	Tool    string
	Spec    string
	HasSpec bool
}

var ruleRe = regexp.MustCompile(`^([A-Za-z0-9_*-]+)(?:\((.*)\))?$`)

// ParseRule parses one rule string. It returns false for strings that are not
// in the supported subset; such rules are ignored (and reported by doctor).
func ParseRule(s string) (Rule, bool) {
	s = strings.TrimSpace(s)
	m := ruleRe.FindStringSubmatch(s)
	if m == nil {
		return Rule{}, false
	}
	r := Rule{Raw: s, Tool: m[1]}
	if strings.Contains(s, "(") {
		r.HasSpec = true
		r.Spec = strings.TrimSpace(m[2])
	}
	return r, true
}

// ParseRules parses a list, dropping entries it does not understand.
func ParseRules(list []string) (rules []Rule, bad []string) {
	for _, s := range list {
		if strings.TrimSpace(s) == "" {
			continue
		}
		r, ok := ParseRule(s)
		if !ok {
			bad = append(bad, s)
			continue
		}
		rules = append(rules, r)
	}
	return rules, bad
}

// toolMatches reports whether the rule's tool part covers toolName.
func (r Rule) toolMatches(toolName string) bool {
	t := r.Tool
	if t == toolName {
		return true
	}
	if strings.HasPrefix(t, "mcp__") {
		// mcp__server and mcp__server__* cover the whole server.
		base := strings.TrimSuffix(t, "__*")
		if base == t && strings.Count(t, "__") == 1 {
			// bare mcp__server
			return strings.HasPrefix(toolName, t+"__")
		}
		if base != t {
			return strings.HasPrefix(toolName, base+"__")
		}
		return false
	}
	if t == "*" {
		return true
	}
	return false
}

// MatchResult is the outcome of matching one rule against one tool call.
type MatchResult struct {
	Matched     bool
	Unparseable bool // Bash command could not be split; caller decides
}

// Match evaluates the rule against a tool call. toolInput is the decoded
// tool_input object (may be nil).
func (r Rule) Match(toolName string, toolInput map[string]any, cwd string) MatchResult {
	if !r.toolMatches(toolName) {
		return MatchResult{}
	}
	if !r.HasSpec || r.Spec == "*" || r.Spec == "" {
		return MatchResult{Matched: true}
	}
	switch toolName {
	case "Bash", "PowerShell":
		cmd, _ := toolInput["command"].(string)
		return matchBash(r.Spec, cmd)
	case "Edit", "Write", "Read", "MultiEdit", "NotebookEdit", "Glob":
		p, _ := toolInput["file_path"].(string)
		if p == "" {
			p, _ = toolInput["notebook_path"].(string)
		}
		if p == "" {
			p, _ = toolInput["path"].(string)
		}
		return MatchResult{Matched: matchPath(r.Spec, p, cwd)}
	case "WebFetch":
		u, _ := toolInput["url"].(string)
		return MatchResult{Matched: matchDomain(r.Spec, u)}
	default:
		// A spec on a tool we don't know how to inspect: treat as whole-tool.
		return MatchResult{Matched: true}
	}
}

// ---- Bash -----------------------------------------------------------------

var envAssignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
var wsRe = regexp.MustCompile(`\s+`)

func matchBash(spec, command string) MatchResult {
	segs, err := SplitCommand(command)
	if err != nil {
		return MatchResult{Unparseable: true}
	}
	for _, seg := range segs {
		if bashSegmentMatches(spec, seg) {
			return MatchResult{Matched: true}
		}
	}
	return MatchResult{}
}

func bashSegmentMatches(spec, seg string) bool {
	seg = wsRe.ReplaceAllString(strings.TrimSpace(seg), " ")
	spec = wsRe.ReplaceAllString(strings.TrimSpace(spec), " ")
	switch {
	case strings.HasSuffix(spec, " *"), strings.HasSuffix(spec, ":*"):
		prefix := strings.TrimSpace(spec[:len(spec)-2])
		return seg == prefix || strings.HasPrefix(seg, prefix+" ")
	case strings.HasSuffix(spec, "*"):
		return strings.HasPrefix(seg, spec[:len(spec)-1])
	default:
		return seg == spec
	}
}

// SplitCommand splits a shell command line into its sub-commands: it cuts on
// &&, ||, ;, |, &, newlines, and on $( ) and backtick substitutions, outside
// quotes. Leading VAR=value assignments are stripped from every segment. It
// returns an error when quotes are unbalanced, which callers treat as "could
// not inspect this command".
func SplitCommand(cmd string) ([]string, error) {
	var segs []string
	var words []string
	var cur strings.Builder
	inWord := false
	endWord := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	flush := func() {
		endWord()
		// drop leading VAR=value assignments
		for len(words) > 0 && envAssignRe.MatchString(words[0]) {
			words = words[1:]
		}
		if len(words) > 0 {
			segs = append(segs, strings.Join(words, " "))
		}
		words = nil
	}
	inS, inD := false, false
	depth := 0
	runes := []rune(cmd)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case inS:
			if c == '\'' {
				inS = false
			} else {
				cur.WriteRune(c)
			}
		case inD:
			if c == '\\' && i+1 < len(runes) {
				cur.WriteRune(runes[i+1])
				i++
			} else if c == '"' {
				inD = false
			} else {
				cur.WriteRune(c)
			}
		case c == '\\' && i+1 < len(runes):
			inWord = true
			cur.WriteRune(runes[i+1])
			i++
		case c == '\'':
			inWord = true
			inS = true
		case c == '"':
			inWord = true
			inD = true
		case c == '$' && i+1 < len(runes) && runes[i+1] == '(':
			flush()
			depth++
			i++
		case c == ')' && depth > 0:
			flush()
			depth--
		case c == '`':
			flush()
		case c == '\n', c == ';':
			flush()
		case c == '&' || c == '|':
			flush()
			if i+1 < len(runes) && runes[i+1] == c {
				i++
			}
		case c == ' ' || c == '\t' || c == '\r':
			endWord()
		default:
			inWord = true
			cur.WriteRune(c)
		}
	}
	if inS || inD {
		return nil, errors.New("unbalanced quotes")
	}
	flush()
	return segs, nil
}

// ---- paths ----------------------------------------------------------------

func normPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	// C:/x -> /c/x (Claude normalises Windows paths to POSIX form the same way)
	if len(p) >= 2 && p[1] == ':' {
		p = "/" + strings.ToLower(p[:1]) + p[2:]
	}
	return p
}

func globToRegexp(glob string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	rs := []rune(glob)
	for i := 0; i < len(rs); i++ {
		switch rs[i] {
		case '*':
			if i+1 < len(rs) && rs[i+1] == '*' {
				// "**/" matches zero or more directories; "**" alone matches anything
				if i+2 < len(rs) && rs[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 2
				} else {
					b.WriteString(".*")
					i++
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(rs[i])))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

func matchPath(spec, filePath, cwd string) bool {
	if filePath == "" {
		return false
	}
	fp := normPath(filePath)
	spec = normPath(spec)
	cwdN := normPath(cwd)
	candidates := []string{fp}
	if cwdN != "" && strings.HasPrefix(fp, strings.TrimSuffix(cwdN, "/")+"/") {
		candidates = append(candidates, strings.TrimPrefix(fp, strings.TrimSuffix(cwdN, "/")+"/"))
	}
	var pat string
	switch {
	case strings.HasPrefix(spec, "//"):
		pat = spec[1:] // absolute
	case strings.HasPrefix(spec, "~/"):
		pat = "**/" + spec[2:] // home-relative: we don't know the home here; match by suffix
	case strings.HasPrefix(spec, "./"):
		pat = spec[2:]
	case strings.HasPrefix(spec, "/"):
		pat = spec[1:] // Claude resolves this against the settings dir; we match relative to cwd
	default:
		pat = spec
	}
	re := globToRegexp(pat)
	for _, c := range candidates {
		if re.MatchString(c) {
			return true
		}
		// allow deny-style "any depth" matching for relative patterns
		if !strings.HasPrefix(pat, "/") && re.MatchString(path.Base(c)) {
			return true
		}
	}
	// any-depth: try every suffix of the relative candidate
	if !strings.HasPrefix(pat, "/") {
		for _, c := range candidates {
			parts := strings.Split(strings.TrimPrefix(c, "/"), "/")
			for i := range parts {
				if re.MatchString(strings.Join(parts[i:], "/")) {
					return true
				}
			}
		}
	}
	return false
}

// ---- domains --------------------------------------------------------------

func matchDomain(spec, rawURL string) bool {
	spec = strings.TrimSpace(spec)
	if !strings.HasPrefix(spec, "domain:") {
		return false
	}
	want := strings.ToLower(strings.TrimPrefix(spec, "domain:"))
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if strings.HasPrefix(want, "*.") {
		base := want[2:]
		return host == base || strings.HasSuffix(host, "."+base)
	}
	return host == want
}
