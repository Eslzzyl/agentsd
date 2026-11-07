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

func TestStoreReadsEventsAfterCursor(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(Header{Namespace: "events", Task: "stream"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.AppendEventWithResult("events", created.Header.ID, EventAgentStarted, map[string]any{"turn": 1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AppendEventWithResult("events", created.Header.ID, EventAgentIdle, map[string]any{"turn": 2})
	if err != nil {
		t.Fatal(err)
	}
	all, err := store.ReadEvents("events", created.Header.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != first.ID || all[0].Seq != 1 || all[1].ID != second.ID || all[1].Seq != 2 {
		t.Fatalf("events = %#v", all)
	}
	afterFirst, err := store.ReadEvents("events", created.Header.ID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterFirst) != 1 || afterFirst[0].ID != second.ID {
		t.Fatalf("events after first = %#v", afterFirst)
	}
	page, err := store.ReadEventPage("events", created.Header.ID, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].ID != first.ID || page.Events[0].Seq != 1 || !page.HasMore || page.NextAfter != first.ID {
		t.Fatalf("first page = %#v", page)
	}
	page, err = store.ReadEventPage("events", created.Header.ID, page.NextAfter, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].ID != second.ID || page.Events[0].Seq != 2 || page.HasMore {
		t.Fatalf("second page = %#v", page)
	}
	tailPage, err := store.QueryEventPage("events", created.Header.ID, EventQuery{Tail: true, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(tailPage.Events) != 1 || tailPage.Events[0].ID != second.ID || !tailPage.HasPrevious || tailPage.PreviousBefore != second.ID {
		t.Fatalf("tail page = %#v", tailPage)
	}
	beforePage, err := store.QueryEventPage("events", created.Header.ID, EventQuery{
		Before: second.ID,
		Limit:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(beforePage.Events) != 1 || beforePage.Events[0].ID != first.ID || beforePage.HasPrevious {
		t.Fatalf("before page = %#v", beforePage)
	}
	sequencePage, err := store.QueryEventPage("events", created.Header.ID, EventQuery{
		FromSeq: 1,
		ToSeq:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sequencePage.Events) != 1 || sequencePage.Events[0].ID != first.ID {
		t.Fatalf("sequence page = %#v", sequencePage)
	}
	if _, err := store.ReadEvents("events", created.Header.ID, "missing"); err == nil {
		t.Fatal("missing cursor was accepted")
	}
}
func TestStoreAssignsSequencesToLegacyEvents(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(Header{Namespace: "legacy", Task: "compatibility"})
	if err != nil {
		t.Fatal(err)
	}
	path, err := store.SessionPath("legacy", created.Header.ID)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONLine(file, Event{ID: "legacy1", Type: EventAgentStarted}); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	page, err := store.ReadEventPage("legacy", created.Header.ID, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].Seq != 1 {
		t.Fatalf("legacy page = %#v", page)
	}
	appended, err := store.AppendEventWithResult("legacy", created.Header.ID, EventAgentIdle, nil)
	if err != nil {
		t.Fatal(err)
	}
	if appended.Seq != 2 {
		t.Fatalf("appended sequence = %d, want 2", appended.Seq)
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
