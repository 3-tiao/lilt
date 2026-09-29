package skillcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/3-tiao/lilt/internal/api"
)

// publishedSkill is the skill users install; the repository links to it rather
// than keeping a copy (AGENTS.md).
const publishedSkill = "../../skills/music-control/SKILL.md"

func readSkill(t *testing.T) string {
	t.Helper()
	contents, err := os.ReadFile(publishedSkill)
	if err != nil {
		t.Fatalf("read the published skill: %v", err)
	}
	return string(contents)
}

// Every command and error code the skill names must exist in the shipped
// catalog, so a renamed command or a retired code cannot survive in policy text.
func TestPublishedSkillMatchesTheCatalog(t *testing.T) {
	problems := Check(readSkill(t), api.NewRegistry())
	if len(problems) == 0 {
		return
	}
	for _, problem := range problems {
		t.Errorf("SKILL.md:%d: %s", problem.Line, problem.Detail)
	}
}

// The skill must stay a trigger-and-policy document: it names the CLI it drives
// and does not grow an interface copy that would drift from `lilt api --json`.
func TestPublishedSkillStaysPolicyOnly(t *testing.T) {
	doc := readSkill(t)
	if !strings.Contains(doc, "name: music-control") {
		t.Error("SKILL.md lost its frontmatter name")
	}
	if !strings.Contains(doc, "description:") {
		t.Error("SKILL.md lost its frontmatter description")
	}
	if !strings.Contains(doc, "lilt api --json") {
		t.Error("SKILL.md must point at `lilt api --json` as the interface source of truth")
	}
	for _, forbidden := range []string{
		"LILT_SOCKET", "activity.sqlite3", "internal/server", "state.json",
	} {
		if strings.Contains(doc, forbidden) {
			t.Errorf("SKILL.md leaks an implementation detail: %q", forbidden)
		}
	}
	if regexp.MustCompile(`\p{Han}`).MatchString(doc) {
		t.Error("published SKILL.md should be English-only")
	}
}

// The safety policies an agent must not lose during a slimming pass. They are
// the reason the skill exists: the interface itself lives in `lilt api --json`.
func TestPublishedSkillStatesTheSafetyPolicies(t *testing.T) {
	doc := readSkill(t)
	for _, policy := range []struct{ name, fragment string }{
		{"never drive the human TUI", "Do not drive `lilt tui`"},
		{"read capabilities before choosing a source", "First read `lilt sources --json`"},
		{"honor an explicit source", "do not substitute another source"},
		{"do not authorize without an explicit request", "without an explicit request"},
		{"do not silently downgrade unsupported forms", "do not retry without that option"},
		{"do not replay unknown outcomes", "never replay the mutation"},
		{"do not alter the queue without consent", "unless asked"},
		{"confirm state after a mutation", "After each playback or queue mutation, read `lilt status --json`"},
	} {
		if !strings.Contains(doc, policy.fragment) {
			t.Errorf("SKILL.md lost the policy %q (%q)", policy.name, policy.fragment)
		}
	}
}

// The repository links point at the published skill instead of copying it.
func TestSkillSymlinksResolveToThePublishedSkill(t *testing.T) {
	published, err := filepath.Abs(publishedSkill)
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{
		"../../.agents/skills/music-control/SKILL.md",
		"../../.opencode/skills/music-control/SKILL.md",
	} {
		resolved, err := filepath.EvalSymlinks(link)
		if err != nil {
			t.Errorf("%s: %v", link, err)
			continue
		}
		absolute, err := filepath.Abs(resolved)
		if err != nil {
			t.Fatal(err)
		}
		if absolute != published {
			t.Errorf("%s resolves to %s, want %s", link, absolute, published)
		}
	}
}

// The parser itself: commands are normalized and "a|b" alternatives expand.
func TestCommandExtractionNormalizesInvocations(t *testing.T) {
	doc := "Use `lilt favorite add|remove <ref> --json`, then `lilt history stats <ref>... --json`.\n" +
		"Run `./lilt sources --json`; never run `lilt tui`.\n" +
		"| `playback_error` | x |\n| `not_a_real_code` | y |\n"
	commands := Commands(doc)
	for _, want := range []string{"favorite add", "favorite remove", "history stats", "sources", "tui"} {
		if _, ok := commands[want]; !ok {
			t.Errorf("missing command %q in %v", want, commands)
		}
	}
	codes := ErrorCodes(doc)
	if _, ok := codes["playback_error"]; !ok {
		t.Errorf("missing error code in %v", codes)
	}
	if _, ok := codes["not_a_real_code"]; !ok {
		t.Errorf("error table row was not parsed: %v", codes)
	}
}

// An unknown command or code is reported, with its line.
func TestCheckReportsDrift(t *testing.T) {
	doc := "line one\nUse `lilt does-not-exist --json`.\n| `not_a_code` | x |\n"
	problems := Check(doc, api.NewRegistry())
	if len(problems) != 2 {
		t.Fatalf("problems = %+v, want two", problems)
	}
	if problems[0].Line != 2 || !strings.Contains(problems[0].Detail, "does-not-exist") {
		t.Errorf("first problem = %+v", problems[0])
	}
	if problems[1].Line != 3 || !strings.Contains(problems[1].Detail, "not_a_code") {
		t.Errorf("second problem = %+v", problems[1])
	}
}
