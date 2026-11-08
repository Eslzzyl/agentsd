package instructions

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxDownwardDepth = 3
	maxDownwardFiles = 100
)

var ignoredDirectoryNames = map[string]struct{}{
	".git":         {},
	".hg":          {},
	".svn":         {},
	"node_modules": {},
	"vendor":       {},
	"dist":         {},
	"build":        {},
	"target":       {},
	".cache":       {},
}

type File struct {
	Path    string
	Content string
	Depth   int
}

type DiscoveredInstructions struct {
	Workspace string
	Upward    []File
	Downward  []string
}

func Discover(workspace string) (*DiscoveredInstructions, error) {
	absoluteWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("instructions: resolve workspace: %w", err)
	}
	info, err := os.Stat(absoluteWorkspace)
	if err != nil {
		return nil, fmt.Errorf("instructions: stat workspace: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("instructions: workspace %q is not a directory", absoluteWorkspace)
	}

	upward, err := discoverUpward(absoluteWorkspace)
	if err != nil {
		return nil, err
	}
	downward, err := discoverDownward(absoluteWorkspace)
	if err != nil {
		return nil, err
	}
	return &DiscoveredInstructions{
		Workspace: absoluteWorkspace,
		Upward:    upward,
		Downward:  downward,
	}, nil
}

func FormatPrompt(instructions *DiscoveredInstructions) string {
	if instructions == nil {
		return ""
	}
	if len(instructions.Upward) == 0 && len(instructions.Downward) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("<project-context>\n")
	if len(instructions.Upward) > 0 {
		builder.WriteString("<repo-rules>\n")
		builder.WriteString("MUST follow these context files for all tasks:\n")
		for _, file := range instructions.Upward {
			builder.WriteString(`<file path="`)
			builder.WriteString(html.EscapeString(file.Path))
			builder.WriteString("\">\n")
			builder.WriteString(file.Content)
			if !strings.HasSuffix(file.Content, "\n") {
				builder.WriteByte('\n')
			}
			builder.WriteString("</file>\n")
		}
		builder.WriteString("</repo-rules>\n")
	}
	if len(instructions.Downward) > 0 {
		builder.WriteString("<dir-context>\n")
		builder.WriteString("Before changes in these directories, MUST read:\n")
		for _, path := range instructions.Downward {
			builder.WriteString("- ")
			builder.WriteString(path)
			builder.WriteByte('\n')
		}
		builder.WriteString("</dir-context>\n")
	}
	if len(instructions.Upward) > 0 || len(instructions.Downward) > 0 {
		builder.WriteString("Context files above auto-loaded. NEVER grep/glob for AGENTS.md; relevant files already in context.\n")
	}
	builder.WriteString("</project-context>")
	return builder.String()
}

func discoverUpward(workspace string) ([]File, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	} else {
		home, err = filepath.Abs(home)
		if err != nil {
			home = ""
		}
	}

	directories := make([]string, 0, 8)
	current := workspace
	for {
		directories = append(directories, current)
		gitRoot, err := hasGitMarker(current)
		if err != nil {
			return nil, err
		}
		if gitRoot || current == home {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	files := make([]File, 0, len(directories))
	for index := len(directories) - 1; index >= 0; index-- {
		directory := directories[index]
		path := filepath.Join(directory, "AGENTS.md")
		content, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("instructions: read %q: %w", path, err)
		}
		if strings.TrimSpace(string(content)) == "" {
			continue
		}
		files = append(files, File{
			Path:    path,
			Content: string(content),
			Depth:   len(directories) - 1 - index,
		})
	}
	return files, nil
}

func discoverDownward(workspace string) ([]string, error) {
	type directory struct {
		path  string
		depth int
	}
	queue := []directory{{path: workspace, depth: 0}}
	files := make([]string, 0, 16)
	for len(queue) > 0 && len(files) < maxDownwardFiles {
		current := queue[0]
		queue = queue[1:]
		entries, err := os.ReadDir(current.path)
		if err != nil {
			return nil, fmt.Errorf("instructions: scan %q: %w", current.path, err)
		}
		for _, entry := range entries {
			if entry.Name() == "AGENTS.md" && current.depth > 0 && !entry.IsDir() {
				relative, err := filepath.Rel(workspace, filepath.Join(current.path, entry.Name()))
				if err != nil {
					return nil, fmt.Errorf("instructions: make relative path: %w", err)
				}
				files = append(files, filepath.ToSlash(relative))
				if len(files) == maxDownwardFiles {
					break
				}
				continue
			}
			if current.depth >= maxDownwardDepth || !entry.IsDir() {
				continue
			}
			if _, ignored := ignoredDirectoryNames[entry.Name()]; ignored {
				continue
			}
			queue = append(queue, directory{
				path:  filepath.Join(current.path, entry.Name()),
				depth: current.depth + 1,
			})
		}
	}
	sort.Strings(files)
	return files, nil
}

func hasGitMarker(directory string) (bool, error) {
	_, err := os.Stat(filepath.Join(directory, ".git"))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("instructions: inspect git marker in %q: %w", directory, err)
}
