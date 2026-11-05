package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// FileSnapshot describes the file state observed before a tool mutation.
type FileSnapshot struct {
	Path    string
	SHA256  string
	Bytes   int64
	Mode    fs.FileMode
	ModTime time.Time
}

func SnapshotFile(path string) (FileSnapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		return FileSnapshot{}, fmt.Errorf("tools: open %q for snapshot: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return FileSnapshot{}, fmt.Errorf("tools: stat %q for snapshot: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return FileSnapshot{}, fmt.Errorf("tools: %q is not a regular file", path)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return FileSnapshot{}, fmt.Errorf("tools: hash %q: %w", path, err)
	}
	return FileSnapshot{
		Path:    path,
		SHA256:  hex.EncodeToString(hash.Sum(nil)),
		Bytes:   info.Size(),
		Mode:    info.Mode().Perm(),
		ModTime: info.ModTime(),
	}, nil
}

func (snapshot FileSnapshot) Verify() error {
	current, err := SnapshotFile(snapshot.Path)
	if err != nil {
		return err
	}
	if current.SHA256 != snapshot.SHA256 {
		return fmt.Errorf("tools: file %q changed since snapshot", snapshot.Path)
	}
	return nil
}

func FileSHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func WriteFileAtomic(path string, data []byte, mode fs.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("tools: create parent directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".agentsd-write-*")
	if err != nil {
		return fmt.Errorf("tools: create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode.Perm()); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("tools: set temporary file mode: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("tools: write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("tools: sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("tools: close temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("tools: replace %q: %w", path, err)
	}
	return nil
}
