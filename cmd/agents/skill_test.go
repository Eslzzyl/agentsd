package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	embeddedskill "agentsd/internal/skill"
)

func TestInstallSkillWritesEmbeddedContent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")

	installedPath, err := installSkill(root, false)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(root, skillName, "SKILL.md")
	if installedPath != wantPath {
		t.Fatalf("installed path = %q, want %q", installedPath, wantPath)
	}
	got, err := os.ReadFile(installedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, embeddedskill.Content) {
		t.Fatalf("installed content differs from embedded content")
	}
}

func TestInstallSkillRequiresForceForExistingFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	target := filepath.Join(root, skillName, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := installSkill(root, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("install error = %v, want --force guidance", err)
	}
	if _, err := installSkill(root, true); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, embeddedskill.Content) {
		t.Fatalf("forced content differs from embedded content")
	}
}
