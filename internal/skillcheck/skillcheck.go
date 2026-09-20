// Package skillcheck keeps the published agent skill honest: the commands and
// stable error codes it mentions must exist in the shipped Client API catalog,
// and the repository symlinks must resolve to the published skill.
//
// The skill deliberately does not repeat the interface list, but it does name
// commands and error codes in its policy and recipes. Those names are the part
// that silently drifts, so they are checked here instead of by review.
package skillcheck

import (
	"regexp"
	"sort"
	"strings"

	"github.com/caiguo/lilt/internal/api"
)

// CLIOnlyWords are `lilt` subcommands the CLI handles itself and which therefore
// never appear in the Client API catalog.
var CLIOnlyWords = map[string]bool{
	"help": true, "version": true, "quit": true, "tui": true, "log": true, "serve": true,
}

var (
	backticked = regexp.MustCompile("`([^`]+)`")
	errorRow   = regexp.MustCompile(`^\|\s*` + "`" + `([a-z][a-z_]+)` + "`" + `\s*\|`)
)

// Problem is one contract violation found in a skill document.
type Problem struct {
	Line   int
	Detail string
}

// Commands returns the `lilt …` invocations a skill mentions, normalized to
// their word tokens: flags, placeholders and prose are dropped, and "a|b"
// alternatives expand. Example: "lilt favorite add|remove <ref> --json" yields
// ["favorite add", "favorite remove"].
func Commands(doc string) map[string]int {
	found := map[string]int{}
	for i, line := range strings.Split(doc, "\n") {
		for _, span := range backticked.FindAllStringSubmatch(line, -1) {
			text := strings.TrimSpace(span[1])
			text = strings.TrimPrefix(text, "./")
			if !strings.HasPrefix(text, "lilt ") {
				continue
			}
			for _, invocation := range expandInvocation(strings.TrimPrefix(text, "lilt ")) {
				if invocation == "" {
					continue
				}
				if _, ok := found[invocation]; !ok {
					found[invocation] = i + 1
				}
			}
		}
	}
	return found
}

// expandInvocation turns one command line into its concrete word tokens.
func expandInvocation(text string) []string {
	tokens := []string{}
	for _, token := range strings.Fields(text) {
		switch {
		case token == "…" || token == "...":
			continue
		case strings.HasPrefix(token, "-"):
			continue
		case strings.ContainsAny(token, "<[{\"'`"):
			continue
		}
		tokens = append(tokens, token)
	}
	if len(tokens) == 0 {
		return nil
	}
	// Expand "a|b" alternatives at any position, e.g. "favorite add|remove".
	expanded := []string{""}
	for _, token := range tokens {
		parts := strings.Split(token, "|")
		next := []string{}
		for _, prefix := range expanded {
			for _, part := range parts {
				if part == "" {
					continue
				}
				joined := part
				if prefix != "" {
					joined = prefix + " " + part
				}
				next = append(next, joined)
			}
		}
		expanded = next
	}
	return expanded
}

// ErrorCodes returns the stable error codes a skill lists in its error table.
func ErrorCodes(doc string) map[string]int {
	found := map[string]int{}
	for i, line := range strings.Split(doc, "\n") {
		if match := errorRow.FindStringSubmatch(line); match != nil {
			if _, ok := found[match[1]]; !ok {
				found[match[1]] = i + 1
			}
		}
	}
	return found
}

// CatalogCommands returns every CLI invocation the shipped catalog advertises,
// as word tokens, plus its bare command name.
func CatalogCommands(registry *api.Registry) map[string]bool {
	known := map[string]bool{}
	for _, command := range registry.Describe().Commands {
		known[command.Name] = true
		text := strings.TrimSpace(strings.TrimPrefix(command.CLI, "lilt "))
		if text == "" {
			continue
		}
		for _, invocation := range expandInvocation(text) {
			known[invocation] = true
			// A mention may stop at the group, e.g. "lilt queue" for
			// "lilt queue add|remove|move".
			fields := strings.Fields(invocation)
			if len(fields) > 1 {
				known[fields[0]] = true
			}
		}
	}
	return known
}

// Check reports every command or error code the document names but the catalog
// does not define. The result is sorted by line for a readable failure.
func Check(doc string, registry *api.Registry) []Problem {
	knownCommands := CatalogCommands(registry)
	knownErrors := map[string]bool{}
	for code := range registry.Describe().Errors {
		knownErrors[code] = true
	}

	problems := []Problem{}
	for invocation, line := range Commands(doc) {
		fields := strings.Fields(invocation)
		if knownCommands[invocation] || knownCommands[fields[0]] || CLIOnlyWords[fields[0]] {
			continue
		}
		problems = append(problems, Problem{Line: line, Detail: "unknown command: lilt " + invocation})
	}
	for code, line := range ErrorCodes(doc) {
		if knownErrors[code] {
			continue
		}
		problems = append(problems, Problem{Line: line, Detail: "unknown error code: " + code})
	}
	sort.Slice(problems, func(i, j int) bool {
		if problems[i].Line != problems[j].Line {
			return problems[i].Line < problems[j].Line
		}
		return problems[i].Detail < problems[j].Detail
	})
	return problems
}
