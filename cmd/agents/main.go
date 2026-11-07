package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentsd/internal/config"
	"agentsd/internal/daemon"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "spawn":
		err = runSpawn(os.Args[2:])
	case "ps":
		err = runPS(os.Args[2:])
	case "kill":
		err = runKill(os.Args[2:])
	case "compact":
		err = runCompact(os.Args[2:])
	case "send":
		err = runSend(os.Args[2:])
	case "inbox":
		err = runInbox(os.Args[2:])
	case "activity":
		err = runActivity(os.Args[2:])
	case "namespace":
		err = runNamespace(os.Args[2:])
	case "daemon":
		err = runDaemon(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runSpawn(args []string) error {
	flags := flag.NewFlagSet("spawn", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	socket := flags.String("socket", defaultSocket(), "Unix socket path")
	namespace := flags.String("n", "", "namespace (required)")
	flags.StringVar(namespace, "namespace", "", "namespace (required)")
	model := flags.String("model", "", "model ID")
	task := flags.String("task", "", "agent task")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*namespace) == "" {
		return fmt.Errorf("namespace is required")
	}
	params, err := json.Marshal(struct {
		Namespace string `json:"namespace"`
		Model     string `json:"model"`
		Task      string `json:"task"`
	}{Namespace: *namespace, Model: *model, Task: *task})
	if err != nil {
		return err
	}
	return callAndPrint(*socket, daemon.Request{Method: "spawn", Params: params}, *jsonOutput)
}

func runPS(args []string) error {
	flags := flag.NewFlagSet("ps", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	socket := flags.String("socket", defaultSocket(), "Unix socket path")
	namespace := flags.String("n", "", "namespace (required unless -A)")
	flags.StringVar(namespace, "namespace", "", "namespace (required unless -A)")
	var allNamespaces bool
	flags.BoolVar(&allNamespaces, "A", false, "list all namespaces")
	flags.BoolVar(&allNamespaces, "all-namespaces", false, "list all namespaces")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !allNamespaces && strings.TrimSpace(*namespace) == "" {
		return fmt.Errorf("namespace is required; use -A to list all namespaces")
	}
	params, err := json.Marshal(struct {
		Namespace     string `json:"namespace"`
		AllNamespaces bool   `json:"all_namespaces"`
	}{Namespace: *namespace, AllNamespaces: allNamespaces})
	if err != nil {
		return err
	}
	return callAndPrint(*socket, daemon.Request{Method: "ps", Params: params}, *jsonOutput)
}

func runInspect(args []string) error {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	socket := flags.String("socket", defaultSocket(), "Unix socket path")
	namespace := flags.String("n", "", "namespace (required)")
	flags.StringVar(namespace, "namespace", "", "namespace (required)")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: agents inspect [--socket path] [-n namespace] <id>")
	}
	if strings.TrimSpace(*namespace) == "" && !strings.Contains(flags.Arg(0), "/") {
		return fmt.Errorf("namespace is required")
	}
	params, err := json.Marshal(struct {
		Namespace string `json:"namespace"`
		ID        string `json:"id"`
	}{Namespace: *namespace, ID: flags.Arg(0)})
	if err != nil {
		return err
	}
	return callAndPrint(*socket, daemon.Request{Method: "inspect", Params: params}, *jsonOutput)
}

func runKill(args []string) error {
	flags := flag.NewFlagSet("kill", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	socket := flags.String("socket", defaultSocket(), "Unix socket path")
	namespace := flags.String("n", "", "namespace (required)")
	flags.StringVar(namespace, "namespace", "", "namespace (required)")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: agents kill [--socket path] [-n namespace] <id>")
	}
	if strings.TrimSpace(*namespace) == "" && !strings.Contains(flags.Arg(0), "/") {
		return fmt.Errorf("namespace is required")
	}
	params, err := json.Marshal(struct {
		Namespace string `json:"namespace"`
		ID        string `json:"id"`
	}{Namespace: *namespace, ID: flags.Arg(0)})
	if err != nil {
		return err
	}
	return callAndPrint(*socket, daemon.Request{Method: "kill", Params: params}, *jsonOutput)
}
func runCompact(args []string) error {
	flags := flag.NewFlagSet("compact", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	socket := flags.String("socket", defaultSocket(), "Unix socket path")
	namespace := flags.String("n", "", "namespace (required)")
	flags.StringVar(namespace, "namespace", "", "namespace (required)")
	wait := flags.Bool("wait", false, "wait for compaction to finish")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: agents compact [--socket path] [-n namespace] [--wait] <id>")
	}
	if strings.TrimSpace(*namespace) == "" && !strings.Contains(flags.Arg(0), "/") {
		return fmt.Errorf("namespace is required")
	}
	params, err := json.Marshal(struct {
		Namespace string `json:"namespace"`
		ID        string `json:"id"`
		Wait      bool   `json:"wait"`
	}{Namespace: *namespace, ID: flags.Arg(0), Wait: *wait})
	if err != nil {
		return err
	}
	timeout := 10 * time.Second
	if *wait {
		timeout = 5 * time.Minute
	}
	return callAndPrintWithTimeout(
		*socket,
		daemon.Request{Method: "session.compact", Params: params},
		*jsonOutput,
		timeout,
	)
}

func runSend(args []string) error {
	flags := flag.NewFlagSet("send", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	socket := flags.String("socket", defaultSocket(), "Unix socket path")
	namespace := flags.String("n", "", "namespace (required)")
	flags.StringVar(namespace, "namespace", "", "namespace (required)")
	mode := flags.String("mode", "steer", "message mode: steer or follow-up")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() < 2 {
		return fmt.Errorf("usage: agents send [--socket path] [-n namespace] [--mode steer|follow-up] <id> <message>")
	}
	id := flags.Arg(0)
	if strings.TrimSpace(*namespace) == "" && !strings.Contains(id, "/") {
		return fmt.Errorf("namespace is required")
	}
	params, err := json.Marshal(struct {
		Namespace string `json:"namespace"`
		ID        string `json:"id"`
		Mode      string `json:"mode"`
		Content   string `json:"content"`
	}{
		Namespace: *namespace,
		ID:        id,
		Mode:      *mode,
		Content:   strings.Join(flags.Args()[1:], " "),
	})
	if err != nil {
		return err
	}
	return callAndPrint(*socket, daemon.Request{Method: "session.send", Params: params}, *jsonOutput)
}

func runInbox(args []string) error {
	flags := flag.NewFlagSet("inbox", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	socket := flags.String("socket", defaultSocket(), "Unix socket path")
	namespace := flags.String("n", "", "namespace (required)")
	flags.StringVar(namespace, "namespace", "", "namespace (required)")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: agents inbox [--socket path] [-n namespace] <id>")
	}
	id := flags.Arg(0)
	if strings.TrimSpace(*namespace) == "" && !strings.Contains(id, "/") {
		return fmt.Errorf("namespace is required")
	}
	params, err := json.Marshal(struct {
		Namespace string `json:"namespace"`
		ID        string `json:"id"`
	}{Namespace: *namespace, ID: id})
	if err != nil {
		return err
	}
	return callAndPrint(*socket, daemon.Request{Method: "session.inbox", Params: params}, *jsonOutput)
}

func runActivity(args []string) error {
	flags := flag.NewFlagSet("activity", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	socket := flags.String("socket", defaultSocket(), "Unix socket path")
	namespace := flags.String("n", "", "namespace (required)")
	flags.StringVar(namespace, "namespace", "", "namespace (required)")
	follow := flags.Bool("follow", false, "follow live events over WebSocket")
	after := flags.String("after", "", "resume after an event ID")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: agents activity [--socket path] [-n namespace] [--follow] [--after event-id] <id>")
	}
	id := flags.Arg(0)
	if strings.TrimSpace(*namespace) == "" && !strings.Contains(id, "/") {
		return fmt.Errorf("namespace is required")
	}
	if *follow {
		return followActivity(*socket, *namespace, id, *after, *jsonOutput)
	}
	params, err := json.Marshal(struct {
		Namespace string `json:"namespace"`
		ID        string `json:"id"`
		After     string `json:"after"`
	}{Namespace: *namespace, ID: id, After: *after})
	if err != nil {
		return err
	}
	return callAndPrint(*socket, daemon.Request{Method: "session.events", Params: params}, *jsonOutput)
}

func followActivity(socket, namespace, id, after string, jsonOutput bool) error {
	ctx := context.Background()
	connection, _, err := daemon.DialWebSocket(ctx, daemon.WebSocketSocketPath(socket))
	if err != nil {
		return fmt.Errorf("connect to event stream: %w", err)
	}
	defer connection.Close()
	if err := connection.WriteJSON(map[string]any{
		"type":       "subscribe",
		"namespace":  namespace,
		"session_id": id,
		"after":      after,
	}); err != nil {
		return fmt.Errorf("subscribe to event stream: %w", err)
	}
	for {
		var raw json.RawMessage
		if err := connection.ReadJSON(&raw); err != nil {
			return fmt.Errorf("read event stream: %w", err)
		}
		if jsonOutput {
			fmt.Println(string(raw))
			continue
		}
		var frame struct {
			Type  string          `json:"type"`
			Event json.RawMessage `json:"event"`
			Error string          `json:"error"`
		}
		if err := json.Unmarshal(raw, &frame); err != nil {
			return err
		}
		switch frame.Type {
		case "event":
			fmt.Println(string(frame.Event))
		case "error", "subscription_closed":
			return errors.New(frame.Error)
		}
	}
}

func runNamespace(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: agents namespace <create|delete|rename|list>")
	}
	switch args[0] {
	case "create":
		return runNamespaceCreate(args[1:])
	case "delete":
		return runNamespaceDelete(args[1:])
	case "rename":
		return runNamespaceRename(args[1:])
	case "list":
		return runNamespaceList(args[1:])
	default:
		return fmt.Errorf("usage: agents namespace <create|delete|rename|list>")
	}
}

func runNamespaceCreate(args []string) error {
	flags := flag.NewFlagSet("namespace create", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	socket := flags.String("socket", defaultSocket(), "Unix socket path")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: agents namespace create [--socket path] <name>")
	}
	params, err := json.Marshal(map[string]string{"name": flags.Arg(0)})
	if err != nil {
		return err
	}
	return callAndPrint(*socket, daemon.Request{Method: "namespace.create", Params: params}, *jsonOutput)
}

func runNamespaceDelete(args []string) error {
	flags := flag.NewFlagSet("namespace delete", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	socket := flags.String("socket", defaultSocket(), "Unix socket path")
	jsonOutput := flags.Bool("json", false, "print JSON")
	force := flags.Bool("force", false, "delete namespace and all sessions")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: agents namespace delete [--socket path] [--force] <name>")
	}
	params, err := json.Marshal(struct {
		Name  string `json:"name"`
		Force bool   `json:"force"`
	}{Name: flags.Arg(0), Force: *force})
	if err != nil {
		return err
	}
	return callAndPrint(*socket, daemon.Request{Method: "namespace.delete", Params: params}, *jsonOutput)
}

func runNamespaceRename(args []string) error {
	flags := flag.NewFlagSet("namespace rename", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	socket := flags.String("socket", defaultSocket(), "Unix socket path")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 2 {
		return fmt.Errorf("usage: agents namespace rename [--socket path] <from> <to>")
	}
	params, err := json.Marshal(struct {
		From string `json:"from"`
		To   string `json:"to"`
	}{From: flags.Arg(0), To: flags.Arg(1)})
	if err != nil {
		return err
	}
	return callAndPrint(*socket, daemon.Request{Method: "namespace.rename", Params: params}, *jsonOutput)
}

func runNamespaceList(args []string) error {
	flags := flag.NewFlagSet("namespace list", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	socket := flags.String("socket", defaultSocket(), "Unix socket path")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("usage: agents namespace list [--socket path]")
	}
	return callAndPrint(*socket, daemon.Request{Method: "namespace.list"}, *jsonOutput)
}

func callAndPrint(socket string, request daemon.Request, jsonOutput bool) error {
	return callAndPrintWithTimeout(socket, request, jsonOutput, 10*time.Second)
}

func callAndPrintWithTimeout(
	socket string,
	request daemon.Request,
	jsonOutput bool,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	result, err := daemon.Call(ctx, socket, request)
	if err != nil {
		return friendlyConnectionError(socket, err)
	}
	if request.Method == "ps" && !jsonOutput {
		return printPS(result)
	}
	if request.Method == "namespace.list" && !jsonOutput {
		return printNamespaces(result)
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, result, "", "  "); err != nil {
		return err
	}
	fmt.Println(formatted.String())
	return nil
}

func friendlyConnectionError(socket string, err error) error {
	var opErr *net.OpError
	if !errors.As(err, &opErr) || opErr.Op != "dial" {
		return err
	}
	return fmt.Errorf(
		"Unable to connect to the agentsd daemon.\n\nStart the daemon first:\n  agents daemon start --socket %q\n\nSocket path: %s\nConnection details: %w",
		socket,
		socket,
		opErr.Err,
	)
}

func printPS(result []byte) error {
	var agents []daemon.AgentSummary
	if err := json.Unmarshal(result, &agents); err != nil {
		return err
	}
	fmt.Printf("%-8s %-16s %-10s %-24s %s\n", "ID", "NAMESPACE", "STATUS", "MODEL", "TASK")
	for _, agent := range agents {
		id := agent.ID
		if len(id) > 8 {
			id = id[:8]
		}
		fmt.Printf("%-8s %-16s %-10s %-24s %s\n", id, agent.Namespace, agent.Status, agent.Model, agent.Task)
	}
	return nil
}

func printNamespaces(result []byte) error {
	var namespaces []string
	if err := json.Unmarshal(result, &namespaces); err != nil {
		return err
	}
	for _, namespace := range namespaces {
		fmt.Println(namespace)
	}
	return nil
}

func defaultSocket() string {
	paths, err := config.DefaultPaths()
	if err != nil {
		return filepath.Join(os.Getenv("HOME"), ".agentsd", "agentsd.sock")
	}
	return filepath.Join(paths.Directory, "agentsd.sock")
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: agents <spawn|ps|inspect|kill|compact|send|inbox|activity|namespace|daemon> [options]")
	os.Exit(2)
}
