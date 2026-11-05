package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const DefaultOutputMaxBytes = 64 << 10

// ArtifactRef identifies a complete output stored outside the model message.
type ArtifactRef struct {
	ID     string
	Path   string
	Bytes  int
	SHA256 string
}

// ArtifactStore persists large tool outputs for later inspection.
type ArtifactStore struct {
	Root string
	mu   sync.Mutex
}

func NewArtifactStore(root string) (*ArtifactStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("tools: artifact root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("tools: resolve artifact root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("tools: create artifact root: %w", err)
	}
	return &ArtifactStore{Root: absolute}, nil
}

func (store *ArtifactStore) Save(name string, data []byte) (ArtifactRef, error) {
	if store == nil {
		return ArtifactRef{}, fmt.Errorf("tools: artifact store is nil")
	}
	digest := sha256.Sum256(data)
	id := hex.EncodeToString(digest[:])
	path := filepath.Join(store.Root, id+".artifact")

	store.mu.Lock()
	defer store.mu.Unlock()
	if _, err := os.Stat(path); err == nil {
		return ArtifactRef{ID: id, Path: path, Bytes: len(data), SHA256: id}, nil
	} else if !os.IsNotExist(err) {
		return ArtifactRef{}, fmt.Errorf("tools: inspect artifact %q: %w", path, err)
	}

	temporary, err := os.CreateTemp(store.Root, ".artifact-*")
	if err != nil {
		return ArtifactRef{}, fmt.Errorf("tools: create artifact: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return ArtifactRef{}, fmt.Errorf("tools: set artifact permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return ArtifactRef{}, fmt.Errorf("tools: write artifact: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return ArtifactRef{}, fmt.Errorf("tools: sync artifact: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return ArtifactRef{}, fmt.Errorf("tools: close artifact: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		if _, statErr := os.Stat(path); statErr == nil {
			return ArtifactRef{ID: id, Path: path, Bytes: len(data), SHA256: id}, nil
		}
		return ArtifactRef{}, fmt.Errorf("tools: publish artifact: %w", err)
	}
	return ArtifactRef{ID: id, Path: path, Bytes: len(data), SHA256: id}, nil
}

// LimitedOutput is the model-facing view of complete output.
type LimitedOutput struct {
	Text      string
	Bytes     int
	Truncated bool
	Artifact  *ArtifactRef
}

// OutputLimiter caps model-visible output and optionally stores complete data.
type OutputLimiter struct {
	MaxBytes int
	Store    *ArtifactStore
}

func (limiter OutputLimiter) Apply(name string, data []byte) (LimitedOutput, error) {
	limit := limiter.MaxBytes
	if limit <= 0 {
		limit = DefaultOutputMaxBytes
	}
	result := LimitedOutput{Bytes: len(data)}
	if len(data) <= limit {
		result.Text = string(data)
		return result, nil
	}
	result.Text = string(data[:limit])
	result.Truncated = true
	if limiter.Store != nil {
		artifact, err := limiter.Store.Save(name, data)
		if err != nil {
			return LimitedOutput{}, err
		}
		result.Artifact = &artifact
	}
	return result, nil
}
