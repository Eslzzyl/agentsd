package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
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
	case "inspect":
		err = runInspect(os.Args[2:])
	case "kill":
		err = runKill(os.Args[2:])
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
	model := flags.String("model", "", "model ID")
	task := flags.String("task", "", "agent task")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	_ = jsonOutput
	params, err := json.Marshal(struct {
		Model string `json:"model"`
		Task  string `json:"task"`
	}{Model: *model, Task: *task})
	if err != nil {
		return err
	}
	return callAndPrint(*socket, daemon.Request{Method: "spawn", Params: params})
}

func runPS(args []string) error {
	flags := flag.NewFlagSet("ps", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	socket := flags.String("socket", defaultSocket(), "Unix socket path")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	_ = jsonOutput
	return callAndPrint(*socket, daemon.Request{Method: "ps"})
}

func runInspect(args []string) error {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	socket := flags.String("socket", defaultSocket(), "Unix socket path")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	_ = jsonOutput
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: agents inspect [--socket path] <id>")
	}
	params, err := json.Marshal(struct {
		ID string `json:"id"`
	}{ID: flags.Arg(0)})
	if err != nil {
		return err
	}
	return callAndPrint(*socket, daemon.Request{Method: "inspect", Params: params})
}

func runKill(args []string) error {
	flags := flag.NewFlagSet("kill", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	socket := flags.String("socket", defaultSocket(), "Unix socket path")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	_ = jsonOutput
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: agents kill [--socket path] <id>")
	}
	params, err := json.Marshal(struct {
		ID string `json:"id"`
	}{ID: flags.Arg(0)})
	if err != nil {
		return err
	}
	return callAndPrint(*socket, daemon.Request{Method: "kill", Params: params})
}

func callAndPrint(socket string, request daemon.Request) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := daemon.Call(ctx, socket, request)
	if err != nil {
		return err
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, result, "", "  "); err != nil {
		return err
	}
	fmt.Println(formatted.String())
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
	fmt.Fprintln(os.Stderr, "usage: agents <spawn|ps|inspect|kill> [options]")
	os.Exit(2)
}
