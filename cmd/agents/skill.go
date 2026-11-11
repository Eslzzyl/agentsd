package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	embeddedskill "agentsd/internal/skill"
)

const skillName = "agentsd"

func runSkill(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: agents skill install [--path dir] [--force]")
	}
	switch args[0] {
	case "install":
		return runSkillInstall(args[1:])
	default:
		return fmt.Errorf("unknown skill command %q", args[0])
	}
}

func runSkillInstall(args []string) error {
	flags := flag.NewFlagSet("skill install", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	path := flags.String("path", "", "skills root directory")
	force := flags.Bool("force", false, "overwrite an existing skill file")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("usage: agents skill install [--path dir] [--force]")
	}
	installedPath, err := installSkill(*path, *force)
	if err != nil {
		return err
	}
	fmt.Printf("skill installed to %s\n", installedPath)
	return nil
}

func installSkill(root string, force bool) (string, error) {
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("skill: determine home directory: %w", err)
		}
		root = filepath.Join(home, ".agents", "skills")
	}
	target := filepath.Join(root, skillName, "SKILL.md")
	if info, err := os.Stat(target); err == nil {
		if info.IsDir() {
			return "", fmt.Errorf("skill: target %q is a directory", target)
		}
		if !force {
			return "", fmt.Errorf("skill: target %q already exists; use --force to overwrite", target)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("skill: inspect target %q: %w", target, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("skill: create target directory: %w", err)
	}
	if err := os.WriteFile(target, embeddedskill.Content, 0o644); err != nil {
		return "", fmt.Errorf("skill: write %q: %w", target, err)
	}
	return target, nil
}
