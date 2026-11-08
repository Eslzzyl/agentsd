package instructions

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDiscoverLoadsAncestorsInRootToWorkspaceOrder(t *testing.T) {
	repo := t.TempDir()
	workspace := filepath.Join(repo, "packages", "api")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: /tmp/worktree"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(repo, "AGENTS.md"):              "root\n",
		filepath.Join(repo, "packages", "AGENTS.md"):  "packages\n",
		filepath.Join(workspace, "AGENTS.md"):         "api\n",
		filepath.Join(repo, "packages", "ignored.md"): "ignored\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := Discover(workspace)
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{
		filepath.Join(repo, "AGENTS.md"),
		filepath.Join(repo, "packages", "AGENTS.md"),
		filepath.Join(workspace, "AGENTS.md"),
	}
	paths := make([]string, 0, len(got.Upward))
	for _, file := range got.Upward {
		paths = append(paths, file.Path)
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("upward paths = %#v, want %#v", paths, wantPaths)
	}
	if len(got.Downward) != 0 {
		t.Fatalf("downward = %#v, want empty", got.Downward)
	}
}

func TestDiscoverIndexesDeepFilesWithoutInjectingTheirContents(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		filepath.Join(workspace, "one", "AGENTS.md"),
		filepath.Join(workspace, "one", "two", "AGENTS.md"),
		filepath.Join(workspace, "one", "two", "three", "AGENTS.md"),
		filepath.Join(workspace, "one", "two", "three", "four", "AGENTS.md"),
		filepath.Join(workspace, "node_modules", "pkg", "AGENTS.md"),
		filepath.Join(workspace, "vendor", "pkg", "AGENTS.md"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("deep secret: "+path), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := Discover(workspace)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"one/AGENTS.md",
		"one/two/AGENTS.md",
		"one/two/three/AGENTS.md",
	}
	if !reflect.DeepEqual(got.Downward, want) {
		t.Fatalf("downward = %#v, want %#v", got.Downward, want)
	}
	formatted := FormatPrompt(got)
	if strings.Contains(formatted, "deep secret:") {
		t.Fatalf("deep file contents were injected: %q", formatted)
	}
}

func TestDiscoverDownwardFileLimit(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxDownwardFiles+1; index++ {
		path := filepath.Join(workspace, "dir", filepath.Base(filepath.Join("x", string(rune('a'+index%26))+string(rune('0'+index/26)))), "AGENTS.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("deep"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Discover(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Downward) != maxDownwardFiles {
		t.Fatalf("downward count = %d, want %d", len(got.Downward), maxDownwardFiles)
	}
}

func TestFormatPrompt(t *testing.T) {
	prompt := FormatPrompt(&DiscoveredInstructions{
		Upward:   []File{{Path: `/tmp/a&b/AGENTS.md`, Content: "Use <tools>."}},
		Downward: []string{"pkg/AGENTS.md"},
	})
	for _, want := range []string{
		"<repo-rules>",
		`<file path="/tmp/a&amp;b/AGENTS.md">`,
		"Use <tools>.",
		"<dir-context>",
		"Before changes in these directories, MUST read:",
		"- pkg/AGENTS.md",
		"Context files above auto-loaded. NEVER grep/glob for AGENTS.md; relevant files already in context.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}
