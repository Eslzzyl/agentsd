package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Workspace resolves tool paths against one session root.
type Workspace struct {
	Root string
}

// ResolvedPath contains the absolute path and its workspace-relative display path.
type ResolvedPath struct {
	Absolute string
	Relative string
}

func NewWorkspace(root string) (Workspace, error) {
	if strings.TrimSpace(root) == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return Workspace{}, fmt.Errorf("tools: determine workspace: %w", err)
		}
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return Workspace{}, fmt.Errorf("tools: resolve workspace %q: %w", root, err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return Workspace{}, fmt.Errorf("tools: resolve workspace %q: %w", root, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return Workspace{}, fmt.Errorf("tools: stat workspace %q: %w", root, err)
	}
	if !info.IsDir() {
		return Workspace{}, fmt.Errorf("tools: workspace %q is not a directory", root)
	}
	return Workspace{Root: canonical}, nil
}

func (workspace Workspace) Resolve(input string) (ResolvedPath, error) {
	if workspace.Root == "" {
		return ResolvedPath{}, fmt.Errorf("tools: workspace root is required")
	}
	if strings.ContainsRune(input, 0) {
		return ResolvedPath{}, fmt.Errorf("tools: path contains a NUL byte")
	}
	cleanInput := strings.TrimSpace(input)
	if cleanInput == "" {
		cleanInput = "."
	}
	candidate := cleanInput
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(workspace.Root, candidate)
	}
	candidate, err := filepath.Abs(candidate)
	if err != nil {
		return ResolvedPath{}, fmt.Errorf("tools: resolve path %q: %w", input, err)
	}
	canonical, err := canonicalPath(candidate)
	if err != nil {
		return ResolvedPath{}, fmt.Errorf("tools: resolve path %q: %w", input, err)
	}
	if !isWithin(workspace.Root, canonical) {
		return ResolvedPath{}, fmt.Errorf("tools: path %q is outside workspace", input)
	}
	relative, err := filepath.Rel(workspace.Root, canonical)
	if err != nil {
		return ResolvedPath{}, fmt.Errorf("tools: make path relative %q: %w", input, err)
	}
	if relative == "." {
		relative = ""
	}
	return ResolvedPath{Absolute: canonical, Relative: filepath.ToSlash(relative)}, nil
}

func canonicalPath(candidate string) (string, error) {
	current := candidate
	missing := make([]string, 0, 2)
	for {
		_, err := os.Lstat(current)
		if err == nil {
			canonical, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for index := len(missing) - 1; index >= 0; index-- {
				canonical = filepath.Join(canonical, missing[index])
			}
			return filepath.Clean(canonical), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fs.ErrNotExist
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func isWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
