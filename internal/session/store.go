package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
)

const (
	EventAgentStarted              = "agent.started"
	EventAgentResumed              = "agent.resumed"
	EventAgentIdle                 = "agent.idle"
	EventAgentCompleted            = "agent.completed"
	EventAgentFailed               = "agent.failed"
	EventAgentCanceled             = "agent.canceled"
	EventLLMCompleted              = "llm.completed"
	EventModelUsage                = "model.usage"
	EventSessionWaiting            = "session.waiting"
	EventSessionWoken              = "session.woken"
	EventMessageQueued             = "message.queued"
	EventMessageDelivered          = "message.delivered"
	EventMessageWaitConsumed       = "message.wait_consumed"
	EventContextCompacted          = "context.compacted"
	EventContextCompactionQueued   = "context.compaction.queued"
	EventContextCompactionFinished = "context.compaction.finished"
)

type Header struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Namespace string    `json:"namespace"`
	Model     string    `json:"model"`
	Task      string    `json:"task"`
	Workspace string    `json:"workspace"`
	StartedAt time.Time `json:"started_at"`
}

type Event struct {
	Type      string
	ID        string
	Seq       uint64
	Timestamp time.Time
	Data      map[string]any
}

func NewEvent(eventType string, data map[string]any) Event {
	return PrepareEvent(Event{Type: eventType, Data: data})
}

func PrepareEvent(event Event) Event {
	if event.ID == "" {
		event.ID = shortID()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	return event
}

func (event Event) MarshalJSON() ([]byte, error) {
	fields := make(map[string]any, len(event.Data)+4)
	for key, value := range event.Data {
		fields[key] = value
	}
	if event.Type != "" {
		fields["type"] = event.Type
	}
	if event.ID != "" {
		fields["id"] = event.ID
	}
	if event.Seq > 0 {
		fields["seq"] = event.Seq
	}
	if !event.Timestamp.IsZero() {
		fields["timestamp"] = event.Timestamp
	}
	return json.Marshal(fields)
}

func (event *Event) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var decoded Event
	decoded.Data = make(map[string]any, len(fields))
	for key, raw := range fields {
		switch key {
		case "type":
			if err := json.Unmarshal(raw, &decoded.Type); err != nil {
				return err
			}
		case "id":
			if err := json.Unmarshal(raw, &decoded.ID); err != nil {
				return err
			}
		case "seq":
			if err := json.Unmarshal(raw, &decoded.Seq); err != nil {
				return err
			}
		case "timestamp":
			if err := json.Unmarshal(raw, &decoded.Timestamp); err != nil {
				return err
			}
		default:
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			decoded.Data[key] = value
		}
	}
	*event = decoded
	return nil
}

type Summary struct {
	Header        Header
	Status        string
	Turns         int
	Final         string
	Error         string
	FinishedAt    *time.Time
	LastEventType string
	Path          string
}

type Store struct {
	root     string
	mu       sync.Mutex
	eventSeq map[string]uint64
}

func NewStore(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("session: root directory is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("session: resolve root %q: %w", root, err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("session: create root %q: %w", absolute, err)
	}
	return &Store{root: absolute, eventSeq: make(map[string]uint64)}, nil
}

func (store *Store) Root() string {
	return store.root
}

func NormalizeNamespace(namespace string) (string, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return "", fmt.Errorf("session: namespace is required")
	}
	if namespace == "." || namespace == ".." || strings.ContainsAny(namespace, `/\\:`) {
		return "", fmt.Errorf("session: invalid namespace %q", namespace)
	}
	for _, character := range namespace {
		if unicode.IsControl(character) {
			return "", fmt.Errorf("session: invalid namespace %q", namespace)
		}
	}
	return namespace, nil
}

func (store *Store) NamespaceExists(namespace string) (bool, error) {
	if store == nil {
		return false, fmt.Errorf("session: store is nil")
	}
	path, err := store.namespacePath(namespace)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("session: inspect namespace %q: %w", namespace, err)
	}
	return info.IsDir(), nil
}

func (store *Store) CreateNamespace(namespace string) error {
	if store == nil {
		return fmt.Errorf("session: store is nil")
	}
	namespace, err := NormalizeNamespace(namespace)
	if err != nil {
		return err
	}
	path := filepath.Join(store.root, namespace)
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := os.Mkdir(path, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("session: namespace %q already exists; delete it before recreating", namespace)
		}
		return fmt.Errorf("session: create namespace %q: %w", namespace, err)
	}
	return nil
}

func (store *Store) DeleteNamespace(namespace string, force ...bool) error {
	if store == nil {
		return fmt.Errorf("session: store is nil")
	}
	if len(force) > 1 {
		return fmt.Errorf("session: delete namespace accepts at most one force flag")
	}
	forceDelete := len(force) == 1 && force[0]
	namespace, err := NormalizeNamespace(namespace)
	if err != nil {
		return err
	}
	path := filepath.Join(store.root, namespace)
	store.mu.Lock()
	defer store.mu.Unlock()
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("session: namespace %q does not exist", namespace)
		}
		return fmt.Errorf("session: inspect namespace %q: %w", namespace, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("session: namespace %q is not a directory", namespace)
	}
	if forceDelete {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("session: force delete namespace %q: %w", namespace, err)
		}
		return nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("session: inspect namespace %q: %w", namespace, err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("session: namespace %q contains sessions; use --force to delete it", namespace)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("session: delete namespace %q: %w", namespace, err)
	}
	return nil
}

func (store *Store) RenameNamespace(from, to string) error {
	if store == nil {
		return fmt.Errorf("session: store is nil")
	}
	from, err := NormalizeNamespace(from)
	if err != nil {
		return err
	}
	to, err = NormalizeNamespace(to)
	if err != nil {
		return err
	}
	if from == to {
		return fmt.Errorf("session: source and target namespace are identical")
	}
	sourcePath := filepath.Join(store.root, from)
	targetPath := filepath.Join(store.root, to)
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, err := os.Stat(sourcePath); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("session: namespace %q does not exist", from)
	} else if err != nil {
		return fmt.Errorf("session: inspect namespace %q: %w", from, err)
	}
	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("session: namespace %q already exists", to)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("session: inspect namespace %q: %w", to, err)
	}
	if err := os.Rename(sourcePath, targetPath); err != nil {
		return fmt.Errorf("session: rename namespace %q to %q: %w", from, to, err)
	}
	paths, err := pathsInDirectory(targetPath)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := rewriteHeaderNamespace(path, to); err != nil {
			return fmt.Errorf("session: update namespace in %q: %w", path, err)
		}
	}
	return nil
}

func (store *Store) ListNamespaces() ([]string, error) {
	if store == nil {
		return nil, fmt.Errorf("session: store is nil")
	}
	entries, err := os.ReadDir(store.root)
	if err != nil {
		return nil, fmt.Errorf("session: list namespaces: %w", err)
	}
	namespaces := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			namespaces = append(namespaces, entry.Name())
		}
	}
	sort.Strings(namespaces)
	return namespaces, nil
}

func (store *Store) namespacePath(namespace string) (string, error) {
	namespace, err := NormalizeNamespace(namespace)
	if err != nil {
		return "", err
	}
	return filepath.Join(store.root, namespace), nil
}

func (store *Store) SessionPath(namespace, id string) (string, error) {
	namespace, err := NormalizeNamespace(namespace)
	if err != nil {
		return "", err
	}
	id, err = normalizeID(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(store.root, namespace, id+".jsonl"), nil
}

func (store *Store) Create(header Header) (Summary, error) {
	if store == nil {
		return Summary{}, fmt.Errorf("session: store is nil")
	}
	namespace, err := NormalizeNamespace(header.Namespace)
	if err != nil {
		return Summary{}, err
	}
	if header.ID == "" {
		header.ID = uuid.NewString()
	} else {
		header.ID, err = normalizeID(header.ID)
		if err != nil {
			return Summary{}, err
		}
	}
	if header.StartedAt.IsZero() {
		header.StartedAt = time.Now().UTC()
	}
	header.Type = "session.header"
	header.Namespace = namespace
	path, err := store.SessionPath(namespace, header.ID)
	if err != nil {
		return Summary{}, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Summary{}, fmt.Errorf("session: create namespace directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Summary{}, fmt.Errorf("session: create %q: %w", path, err)
	}
	defer file.Close()
	if err := writeJSONLine(file, header); err != nil {
		return Summary{}, fmt.Errorf("session: write header: %w", err)
	}
	if err := file.Sync(); err != nil {
		return Summary{}, fmt.Errorf("session: sync header: %w", err)
	}
	return Summary{Header: header, Status: "running", LastEventType: header.Type, Path: path}, nil
}

func (store *Store) Append(namespace, id string, event Event) error {
	_, err := store.AppendWithResult(namespace, id, event)
	return err
}

func (store *Store) AppendWithResult(namespace, id string, event Event) (Event, error) {
	if store == nil {
		return Event{}, fmt.Errorf("session: store is nil")
	}
	if strings.TrimSpace(event.Type) == "" {
		return Event{}, fmt.Errorf("session: event type is required")
	}
	event = PrepareEvent(event)
	path, err := store.SessionPath(namespace, id)
	if err != nil {
		return Event{}, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	lastSeq, err := store.nextEventSeqLocked(path)
	if err != nil {
		return Event{}, err
	}
	event.Seq = lastSeq + 1
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Event{}, fmt.Errorf("session: open %q for append: %w", path, err)
	}
	defer file.Close()
	if err := writeJSONLine(file, event); err != nil {
		return Event{}, fmt.Errorf("session: append event: %w", err)
	}
	if err := file.Sync(); err != nil {
		return Event{}, fmt.Errorf("session: sync event: %w", err)
	}
	store.eventSeq[path] = event.Seq
	return event, nil
}

func (store *Store) AppendEvent(namespace, id, eventType string, data map[string]any) error {
	return store.Append(namespace, id, NewEvent(eventType, data))
}

func (store *Store) AppendEventWithResult(namespace, id, eventType string, data map[string]any) (Event, error) {
	return store.AppendWithResult(namespace, id, NewEvent(eventType, data))
}

type EventQuery struct {
	After   string
	Before  string
	FromSeq uint64
	ToSeq   uint64
	Limit   int
	Tail    bool
}

type EventPage struct {
	Events         []Event `json:"events"`
	PreviousBefore string  `json:"previous_before,omitempty"`
	NextAfter      string  `json:"next_after,omitempty"`
	HasPrevious    bool    `json:"has_previous"`
	HasMore        bool    `json:"has_more"`
}

func (store *Store) ReadEvents(namespace, id, after string) ([]Event, error) {
	page, err := store.ReadEventPage(namespace, id, after, 0)
	if err != nil {
		return nil, err
	}
	return page.Events, nil
}

func (store *Store) ReadEventPage(namespace, id, after string, limit int) (EventPage, error) {
	return store.QueryEventPage(namespace, id, EventQuery{
		After: after,
		Limit: limit,
	})
}

func (store *Store) QueryEventPage(namespace, id string, query EventQuery) (EventPage, error) {
	if store == nil {
		return EventPage{}, fmt.Errorf("session: store is nil")
	}
	if query.Limit < 0 {
		return EventPage{}, fmt.Errorf("session: event limit cannot be negative")
	}
	if query.FromSeq > 0 && query.ToSeq > 0 && query.FromSeq > query.ToSeq {
		return EventPage{}, fmt.Errorf("session: event sequence range is invalid")
	}
	path, err := store.SessionPath(namespace, id)
	if err != nil {
		return EventPage{}, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	events, err := readEventsFile(path)
	if err != nil {
		return EventPage{}, err
	}

	start, end := 0, len(events)
	if query.After != "" {
		index, ok := findEventIndex(events, query.After)
		if !ok {
			return EventPage{}, fmt.Errorf("session: event cursor %q not found", query.After)
		}
		start = index + 1
	}
	if query.Before != "" {
		index, ok := findEventIndex(events, query.Before)
		if !ok {
			return EventPage{}, fmt.Errorf("session: event cursor %q not found", query.Before)
		}
		end = index
	}
	if query.FromSeq > 0 {
		if index := firstEventAtOrAfter(events, query.FromSeq); index > start {
			start = index
		}
	}
	if query.ToSeq > 0 {
		if index := firstEventAbove(events, query.ToSeq); index < end {
			end = index
		}
	}
	if start > end {
		start = end
	}

	page := EventPage{Events: make([]Event, 0)}
	takeTail := query.Tail || query.Before != "" && query.After == ""
	selectedStart, selectedEnd := start, end
	if takeTail && query.Limit > 0 && selectedEnd-selectedStart > query.Limit {
		selectedStart = selectedEnd - query.Limit
		page.HasPrevious = true
		page.PreviousBefore = events[selectedStart].ID
	}
	if !takeTail && query.Limit > 0 && selectedEnd-selectedStart > query.Limit {
		selectedEnd = selectedStart + query.Limit
		page.HasMore = true
		page.NextAfter = events[selectedEnd-1].ID
	}
	page.Events = append(page.Events, events[selectedStart:selectedEnd]...)
	return page, nil
}

func readEventsFile(path string) ([]Event, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("session: open %q: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("session: read header %q: %w", path, err)
		}
		return nil, fmt.Errorf("session: empty header in %q", path)
	}
	events := make([]Event, 0)
	var sequence uint64
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("session: decode event %q: %w", path, err)
		}
		sequence = assignEventSeq(event.Seq, sequence)
		event.Seq = sequence
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("session: read events %q: %w", path, err)
	}
	return events, nil
}

func findEventIndex(events []Event, id string) (int, bool) {
	for index, event := range events {
		if event.ID == id {
			return index, true
		}
	}
	return 0, false
}

func firstEventAtOrAfter(events []Event, sequence uint64) int {
	for index, event := range events {
		if event.Seq >= sequence {
			return index
		}
	}
	return len(events)
}

func firstEventAbove(events []Event, sequence uint64) int {
	for index, event := range events {
		if event.Seq > sequence {
			return index
		}
	}
	return len(events)
}

func (store *Store) nextEventSeqLocked(path string) (uint64, error) {
	if store.eventSeq == nil {
		store.eventSeq = make(map[string]uint64)
	}
	if sequence, ok := store.eventSeq[path]; ok {
		return sequence, nil
	}
	sequence, err := readLastEventSeq(path)
	if err != nil {
		return 0, err
	}
	store.eventSeq[path] = sequence
	return sequence, nil
}

func readLastEventSeq(path string) (uint64, error) {
	events, err := readEventsFile(path)
	if err != nil {
		return 0, err
	}
	if len(events) == 0 {
		return 0, nil
	}
	return events[len(events)-1].Seq, nil
}

func assignEventSeq(eventSeq, previous uint64) uint64 {
	if eventSeq > previous {
		return eventSeq
	}
	return previous + 1
}

func (store *Store) Read(namespace, id string) (Summary, error) {
	if store == nil {
		return Summary{}, fmt.Errorf("session: store is nil")
	}
	path, err := store.SessionPath(namespace, id)
	if err != nil {
		return Summary{}, err
	}
	return readSummary(path)
}

func (store *Store) List(namespace string) ([]Summary, error) {
	if store == nil {
		return nil, fmt.Errorf("session: store is nil")
	}
	paths, err := store.sessionPaths(namespace)
	if err != nil {
		return nil, err
	}
	result := make([]Summary, 0, len(paths))
	for _, path := range paths {
		summary, err := readSummary(path)
		if err != nil {
			return nil, err
		}
		result = append(result, summary)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Header.StartedAt.Equal(result[right].Header.StartedAt) {
			return result[left].Header.ID < result[right].Header.ID
		}
		return result[left].Header.StartedAt.Before(result[right].Header.StartedAt)
	})
	return result, nil
}

func (store *Store) Recover() ([]Summary, error) {
	summaries, err := store.List("")
	if err != nil {
		return nil, err
	}
	for index := range summaries {
		if isTerminal(summaries[index].LastEventType) {
			continue
		}
		finishedAt := time.Now().UTC()
		err := store.AppendEvent(summaries[index].Header.Namespace, summaries[index].Header.ID, EventAgentFailed, map[string]any{
			"error":       "daemon exited unexpectedly",
			"finished_at": finishedAt,
		})
		if err != nil {
			return nil, err
		}
		summaries[index], err = store.Read(summaries[index].Header.Namespace, summaries[index].Header.ID)
		if err != nil {
			return nil, err
		}
	}
	return summaries, nil
}

func (store *Store) sessionPaths(namespace string) ([]string, error) {
	if namespace != "" {
		namespace, err := NormalizeNamespace(namespace)
		if err != nil {
			return nil, err
		}
		return pathsInDirectory(filepath.Join(store.root, namespace))
	}
	root := store.root
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("session: list namespaces: %w", err)
	}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		namespacePaths, err := pathsInDirectory(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, err
		}
		paths = append(paths, namespacePaths...)
	}
	return paths, nil
}

func pathsInDirectory(directory string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("session: list %q: %w", directory, err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		paths = append(paths, filepath.Join(directory, entry.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}

func rewriteHeaderNamespace(path, namespace string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	headerEnd := bytes.IndexByte(data, '\n')
	if headerEnd < 0 {
		return fmt.Errorf("session header line is missing")
	}
	var header Header
	if err := json.Unmarshal(data[:headerEnd], &header); err != nil {
		return err
	}
	header.Namespace = namespace
	encodedHeader, err := json.Marshal(header)
	if err != nil {
		return err
	}
	updated := make([]byte, 0, len(encodedHeader)+1+len(data)-headerEnd-1)
	updated = append(updated, encodedHeader...)
	updated = append(updated, '\n')
	updated = append(updated, data[headerEnd+1:]...)
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".namespace-rename-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(updated); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func readSummary(path string) (Summary, error) {
	file, err := os.Open(path)
	if err != nil {
		return Summary{}, fmt.Errorf("session: open %q: %w", path, err)
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	firstLine, err := reader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return Summary{}, fmt.Errorf("session: read header %q: %w", path, err)
	}
	firstLine = bytes.TrimSpace(firstLine)
	if len(firstLine) == 0 {
		return Summary{}, fmt.Errorf("session: empty header in %q", path)
	}
	var header Header
	if err := json.Unmarshal(firstLine, &header); err != nil {
		return Summary{}, fmt.Errorf("session: decode header %q: %w", path, err)
	}
	if header.Type != "session.header" || header.ID == "" || header.Namespace == "" {
		return Summary{}, fmt.Errorf("session: invalid header in %q", path)
	}
	lastLine, err := readLastLine(file)
	if err != nil {
		return Summary{}, fmt.Errorf("session: read final event %q: %w", path, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(lastLine, &fields); err != nil {
		return Summary{}, fmt.Errorf("session: decode final event %q: %w", path, err)
	}
	var eventType string
	if err := json.Unmarshal(fields["type"], &eventType); err != nil || eventType == "" {
		return Summary{}, fmt.Errorf("session: final event type is missing in %q", path)
	}
	summary := Summary{Header: header, Status: statusForEvent(eventType), LastEventType: eventType, Path: path}
	_ = json.Unmarshal(fields["turns"], &summary.Turns)
	_ = json.Unmarshal(fields["final"], &summary.Final)
	_ = json.Unmarshal(fields["error"], &summary.Error)
	var finishedAt time.Time
	if err := json.Unmarshal(fields["finished_at"], &finishedAt); err == nil && !finishedAt.IsZero() {
		summary.FinishedAt = &finishedAt
	}
	return summary, nil
}

func readLastLine(file *os.File) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	end := info.Size()
	for end > 0 {
		var last [1]byte
		if _, err := file.ReadAt(last[:], end-1); err != nil {
			return nil, err
		}
		if last[0] != '\n' {
			break
		}
		end--
	}
	if end == 0 {
		return nil, fmt.Errorf("file has no content")
	}
	const chunkSize int64 = 4096
	for scanEnd := end; scanEnd > 0; {
		start := scanEnd - chunkSize
		if start < 0 {
			start = 0
		}
		chunk := make([]byte, scanEnd-start)
		if _, err := file.ReadAt(chunk, start); err != nil {
			return nil, err
		}
		for index := len(chunk) - 1; index >= 0; index-- {
			if chunk[index] != '\n' {
				continue
			}
			lineStart := start + int64(index) + 1
			line := make([]byte, end-lineStart)
			if _, err := file.ReadAt(line, lineStart); err != nil {
				return nil, err
			}
			return bytes.TrimSpace(line), nil
		}
		scanEnd = start
	}
	line := make([]byte, end)
	if _, err := file.ReadAt(line, 0); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(line), nil
}

func normalizeID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\\`) {
		return "", fmt.Errorf("session: invalid session ID %q", id)
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return "", fmt.Errorf("session: invalid session ID %q: %w", id, err)
	}
	return parsed.String(), nil
}

func shortID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
}

func statusForEvent(eventType string) string {
	switch eventType {
	case EventAgentIdle:
		return "idle"
	case EventAgentCompleted:
		return "completed"
	case EventAgentFailed:
		return "failed"
	case EventAgentCanceled:
		return "canceled"
	case EventSessionWaiting:
		return "waiting"
	default:
		return "running"
	}
}

func isTerminal(eventType string) bool {
	return eventType == EventAgentIdle || eventType == EventAgentCompleted || eventType == EventAgentFailed || eventType == EventAgentCanceled
}

func writeJSONLine(writer io.Writer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	_, err = writer.Write(encoded)
	return err
}
