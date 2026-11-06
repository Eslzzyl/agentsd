package main

import (
	"errors"
	"net"
	"os"
	"strings"
	"testing"
)

func TestFriendlyConnectionError(t *testing.T) {
	const socket = "/tmp/agentsd.sock"

	err := friendlyConnectionError(socket, &net.OpError{
		Op:  "dial",
		Net: "unix",
		Err: os.ErrNotExist,
	})

	for _, want := range []string{
		"Unable to connect to the agentsd daemon.",
		`agentsd --socket "/tmp/agentsd.sock"`,
		"Socket path: /tmp/agentsd.sock",
		"Connection details: file does not exist",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want errors.Is(..., os.ErrNotExist)", err)
	}
}

func TestFriendlyConnectionErrorLeavesNonDialErrorsUnchanged(t *testing.T) {
	want := errors.New("server error")
	if got := friendlyConnectionError("/tmp/agentsd.sock", want); got != want {
		t.Fatalf("error = %v, want the original error", got)
	}
}
