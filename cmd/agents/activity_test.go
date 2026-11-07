package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"agentsd/internal/session"
)

func TestPrintActivityHumanOutput(t *testing.T) {
	result, err := json.Marshal(session.EventPage{
		Events: []session.Event{{
			ID:        "evt12345",
			Seq:       1,
			Type:      session.EventAgentStarted,
			Timestamp: time.Date(2026, 11, 7, 13, 0, 0, 0, time.UTC),
			Data:      map[string]any{"model": "test-model"},
		}},
		PreviousBefore: "older123",
		NextAfter:      "evt12345",
		HasPrevious:    true,
		HasMore:        true,
	})
	if err != nil {
		t.Fatal(err)
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = writer
	err = printActivity(result)
	_ = writer.Close()
	os.Stdout = original
	output, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	text := string(output)
	for _, want := range []string{"#1", "agent.started", "evt12345", "model", "--before older123", "--after evt12345"} {
		if !strings.Contains(text, want) {
			t.Fatalf("activity output = %q, want %q", text, want)
		}
	}
}
