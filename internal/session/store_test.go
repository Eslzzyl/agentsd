package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStoreCreatesNamespaceSessionAndReadsTerminalState(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(Header{
		Namespace: "frontend-app",
		Model:     "test-model",
		Task:      "build",
		Workspace: "/workspace/project",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uuid.Parse(created.Header.ID); err != nil {
		t.Fatalf("session ID = %q: %v", created.Header.ID, err)
	}
	path, err := store.SessionPath("frontend-app", created.Header.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(store.Root(), "frontend-app", created.Header.ID+".jsonl"); path != want {
		t.Fatalf("session path = %q, want %q", path, want)
	}
	if err := store.AppendEvent("frontend-app", created.Header.ID, EventAgentStarted, map[string]any{"turn": 1}); err != nil {
		t.Fatal(err)
	}
	finishedAt := time.Now().UTC()
	if err := store.AppendEvent("frontend-app", created.Header.ID, EventAgentCompleted, map[string]any{
		"finished_at": finishedAt,
		"turns":       2,
		"final":       "done",
	}); err != nil {
		t.Fatal(err)
	}

	read, err := store.Read("frontend-app", created.Header.ID)
	if err != nil {
		t.Fatal(err)
	}
	if read.Status != "completed" || read.Turns != 2 || read.Final != "done" || read.FinishedAt == nil {
		t.Fatalf("summary = %#v", read)
	}
	all, err := store.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Header.Namespace != "frontend-app" {
		t.Fatalf("all sessions = %#v", all)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	lines := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var value map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		lines++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if lines != 3 {
		t.Fatalf("JSONL line count = %d, want 3", lines)
	}
}

func TestStoreRecoverMarksIncompleteSessionFailed(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(Header{Namespace: "recovery", Task: "interrupted"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent("recovery", created.Header.ID, EventAgentStarted, nil); err != nil {
		t.Fatal(err)
	}

	recovered, err := store.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].Status != "failed" || recovered[0].Error != "daemon exited unexpectedly" {
		t.Fatalf("recovered = %#v", recovered)
	}
	again, err := store.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 1 || again[0].LastEventType != EventAgentFailed {
		t.Fatalf("second recovery = %#v", again)
	}
}

func TestNormalizeNamespaceRejectsPathComponents(t *testing.T) {
	for _, namespace := range []string{"../escape", `frontend\\app`, ".", ".."} {
		if _, err := NormalizeNamespace(namespace); err == nil {
			t.Fatalf("namespace %q was accepted", namespace)
		}
	}
	if _, err := NormalizeNamespace(""); err == nil {
		t.Fatal("empty namespace was accepted")
	}
}

func TestStoreManagesNamespaces(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateNamespace("empty"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateNamespace("empty"); err == nil {
		t.Fatal("duplicate namespace was accepted")
	}
	if err := store.DeleteNamespace("empty"); err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(Header{Namespace: "old", Task: "rename"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent("old", created.Header.ID, EventAgentCompleted, map[string]any{"final": "done"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateNamespace("new"); err != nil {
		t.Fatal(err)
	}
	if err := store.RenameNamespace("old", "new"); err == nil {
		t.Fatal("rename to an existing namespace was accepted")
	}
	if err := store.DeleteNamespace("old"); err == nil {
		t.Fatal("deletion of a namespace with sessions was accepted")
	}
	if err := store.DeleteNamespace("new"); err != nil {
		t.Fatal(err)
	}
	if err := store.RenameNamespace("old", "renamed"); err != nil {
		t.Fatal(err)
	}
	summary, err := store.Read("renamed", created.Header.ID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Header.Namespace != "renamed" || summary.Status != "completed" {
		t.Fatalf("renamed summary = %#v", summary)
	}

	if err := store.DeleteNamespace("renamed", true); err != nil {
		t.Fatal(err)
	}
	exists, err := store.NamespaceExists("renamed")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("forced namespace deletion left the namespace")
	}
}
