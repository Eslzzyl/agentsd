package tools

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type walkOptions struct {
	IncludeHidden bool
	Gitignore     bool
}

type ignoreRule struct {
	Pattern string
	Negated bool
}

func walkWorkspace(ctx context.Context, workspace Workspace, root string, options walkOptions, visit func(string, string, fs.DirEntry) error) error {
	rules := loadIgnoreRules(workspace.Root, options.Gitignore)
	return filepath.WalkDir(root, func(absolute string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative := relativeDisplay(workspace, absolute)
		if relative == "." {
			relative = ""
		}
		if relative != "" && !options.IncludeHidden && strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if relative != "" && ignoredPath(relative, rules) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return visit(absolute, relative, entry)
	})
}

func loadIgnoreRules(root string, enabled bool) []ignoreRule {
	if !enabled {
		return nil
	}
	file, err := os.Open(filepath.Join(root, ".gitignore"))
	if err != nil {
		return nil
	}
	defer file.Close()
	rules := make([]ignoreRule, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		negated := strings.HasPrefix(line, "!")
		if negated {
			line = strings.TrimPrefix(line, "!")
		}
		line = strings.TrimPrefix(line, "/")
		if line != "" {
			rules = append(rules, ignoreRule{Pattern: filepath.ToSlash(line), Negated: negated})
		}
	}
	return rules
}

func ignoredPath(relative string, rules []ignoreRule) bool {
	if relative == ".git" || strings.HasPrefix(relative, ".git/") {
		return true
	}
	ignored := false
	for _, rule := range rules {
		matched := globMatch(rule.Pattern, relative) || globMatch(rule.Pattern, path.Base(relative))
		if matched {
			ignored = !rule.Negated
		}
	}
	return ignored
}

func globBase(pattern string) string {
	normalized := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(pattern)), "./")
	parts := strings.Split(normalized, "/")
	base := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.ContainsAny(part, "*?[") {
			break
		}
		base = append(base, part)
	}
	if len(base) == 0 {
		return "."
	}
	return strings.Join(base, "/")
}

func globMatch(pattern, candidate string) bool {
	pattern = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(pattern)), "./")
	candidate = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(candidate)), "./")
	if pattern == "" || candidate == "" {
		return pattern == candidate
	}
	patternParts := strings.Split(pattern, "/")
	candidateParts := strings.Split(candidate, "/")
	return matchGlobParts(patternParts, candidateParts)
}

func matchGlobParts(pattern, candidate []string) bool {
	if len(pattern) == 0 {
		return len(candidate) == 0
	}
	if pattern[0] == "**" {
		return matchGlobParts(pattern[1:], candidate) || (len(candidate) > 0 && matchGlobParts(pattern, candidate[1:]))
	}
	if len(candidate) == 0 {
		return false
	}
	matched, err := path.Match(pattern[0], candidate[0])
	return err == nil && matched && matchGlobParts(pattern[1:], candidate[1:])
}

func resolveGlobRoot(workspace Workspace, pattern string) (string, error) {
	base := globBase(pattern)
	resolved, err := workspace.Resolve(base)
	if err != nil {
		return "", fmt.Errorf("tools: resolve glob root %q: %w", base, err)
	}
	return resolved.Absolute, nil
}
