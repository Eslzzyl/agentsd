package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"agentsd/internal/config"
	"agentsd/internal/daemon"
)

const daemonStartupTimeout = 10 * time.Second

type daemonLaunchOptions struct {
	socket    string
	config    string
	auth      string
	state     string
	log       string
	workspace string
	binary    string
	json      bool
}

func runDaemon(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: agents daemon <start|status|stop|restart>")
	}
	switch args[0] {
	case "start":
		return runDaemonStart(args[1:])
	case "status":
		return runDaemonStatus(args[1:])
	case "stop":
		return runDaemonStop(args[1:])
	case "restart":
		return runDaemonRestart(args[1:])
	default:
		return fmt.Errorf("usage: agents daemon <start|status|stop|restart>")
	}
}

func newDaemonLaunchFlags(name string, paths config.Paths) (*flag.FlagSet, *daemonLaunchOptions) {
	workspace, _ := os.Getwd()
	options := &daemonLaunchOptions{
		socket:    filepath.Join(paths.Directory, "agentsd.sock"),
		config:    paths.ConfigFile,
		auth:      paths.AuthFile,
		state:     paths.StateFile,
		log:       paths.LogFile,
		workspace: workspace,
	}
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&options.socket, "socket", options.socket, "Unix socket path")
	flags.StringVar(&options.config, "config", options.config, "config TOML path")
	flags.StringVar(&options.auth, "auth", options.auth, "auth JSON path")
	flags.StringVar(&options.state, "state", options.state, "daemon state path")
	flags.StringVar(&options.log, "log", options.log, "daemon log path")
	flags.StringVar(&options.workspace, "workspace", options.workspace, "agent workspace")
	flags.StringVar(&options.binary, "daemon-path", "", "agentsd executable path")
	flags.BoolVar(&options.json, "json", false, "print JSON")
	return flags, options
}

func runDaemonStart(args []string) error {
	paths, err := config.DefaultPaths()
	if err != nil {
		return err
	}
	flags, options := newDaemonLaunchFlags("daemon start", paths)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("usage: agents daemon start [options]")
	}
	if err := normalizeDaemonLaunchOptions(options); err != nil {
		return err
	}
	return startDaemon(*options)
}

func runDaemonRestart(args []string) error {
	paths, err := config.DefaultPaths()
	if err != nil {
		return err
	}
	flags, options := newDaemonLaunchFlags("daemon restart", paths)
	timeout := 10 * time.Second
	force := false
	flags.DurationVar(&timeout, "timeout", timeout, "stop timeout")
	flags.BoolVar(&force, "force", false, "kill an unresponsive daemon")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("usage: agents daemon restart [options]")
	}
	if err := normalizeDaemonLaunchOptions(options); err != nil {
		return err
	}
	if err := stopDaemon(options.socket, options.state, timeout, force, false); err != nil {
		return err
	}
	return startDaemon(*options)
}

func runDaemonStatus(args []string) error {
	paths, err := config.DefaultPaths()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("daemon status", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	socket := flags.String("socket", filepath.Join(paths.Directory, "agentsd.sock"), "Unix socket path")
	statePath := flags.String("state", paths.StateFile, "daemon state path")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("usage: agents daemon status [options]")
	}
	socketPath, err := filepath.Abs(*socket)
	if err != nil {
		return err
	}
	stateFile, err := filepath.Abs(*statePath)
	if err != nil {
		return err
	}
	status, err := callDaemonStatus(socketPath, 2*time.Second)
	if err == nil {
		printDaemonStatus(status, *jsonOutput)
		return nil
	}
	if !isDaemonDialError(err) {
		return err
	}
	status = stoppedDaemonStatus(stateFile)
	printDaemonStatus(status, *jsonOutput)
	return nil
}

func runDaemonStop(args []string) error {
	paths, err := config.DefaultPaths()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("daemon stop", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	socket := flags.String("socket", filepath.Join(paths.Directory, "agentsd.sock"), "Unix socket path")
	statePath := flags.String("state", paths.StateFile, "daemon state path")
	timeout := flags.Duration("timeout", 10*time.Second, "stop timeout")
	force := flags.Bool("force", false, "kill an unresponsive daemon")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("usage: agents daemon stop [options]")
	}
	socketPath, err := filepath.Abs(*socket)
	if err != nil {
		return err
	}
	stateFile, err := filepath.Abs(*statePath)
	if err != nil {
		return err
	}
	return stopDaemon(socketPath, stateFile, *timeout, *force, *jsonOutput)
}

func normalizeDaemonLaunchOptions(options *daemonLaunchOptions) error {
	paths := []*string{
		&options.socket,
		&options.config,
		&options.auth,
		&options.state,
		&options.log,
		&options.workspace,
	}
	for _, path := range paths {
		absolute, err := filepath.Abs(*path)
		if err != nil {
			return err
		}
		*path = absolute
	}
	return nil
}

func startDaemon(options daemonLaunchOptions) error {
	status, err := callDaemonStatus(options.socket, 2*time.Second)
	if err == nil {
		if options.json {
			printDaemonStatus(status, true)
		} else {
			fmt.Printf("agentsd already running (pid %d)\n", status.PID)
		}
		return nil
	}
	if !isDaemonDialError(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(options.log), 0o700); err != nil {
		return fmt.Errorf("create daemon log directory: %w", err)
	}
	logFile, err := os.OpenFile(options.log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	if err := logFile.Chmod(0o600); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("set daemon log permissions: %w", err)
	}
	binary, err := resolveDaemonBinary(options.binary)
	if err != nil {
		_ = logFile.Close()
		return err
	}
	command := exec.Command(binary,
		"--socket", options.socket,
		"--config", options.config,
		"--auth", options.auth,
		"--state", options.state,
	)
	command.Dir = options.workspace
	command.Stdout = logFile
	command.Stderr = logFile
	configureDetachedCommand(command)
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start agentsd: %w", err)
	}
	_ = command.Process.Release()
	_ = logFile.Close()

	status, err = waitForDaemon(options.socket, daemonStartupTimeout)
	if err != nil {
		return fmt.Errorf("agentsd failed to start: %w; log: %s", err, options.log)
	}
	printDaemonStatus(status, options.json)
	return nil
}

func stopDaemon(socketPath, statePath string, timeout time.Duration, force, jsonOutput bool) error {
	status, err := callDaemonStatus(socketPath, 2*time.Second)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if _, err := daemon.Call(ctx, socketPath, daemon.Request{Method: "daemon.shutdown"}); err != nil {
			return err
		}
		if err := waitForDaemonStopped(socketPath, statePath, timeout); err != nil {
			return err
		}
		printDaemonStatus(daemon.DaemonStatus{State: "stopped", PID: status.PID}, jsonOutput)
		return nil
	}
	if !isDaemonDialError(err) {
		return err
	}
	if !force {
		if _, stateErr := daemon.ReadDaemonState(statePath); errors.Is(stateErr, os.ErrNotExist) {
			printDaemonStatus(daemon.DaemonStatus{State: "stopped"}, jsonOutput)
			return nil
		}
		return fmt.Errorf("agentsd is not responding at %s; use --force", socketPath)
	}
	if err := forceStopDaemon(socketPath, statePath); err != nil {
		return err
	}
	printDaemonStatus(daemon.DaemonStatus{State: "stopped"}, jsonOutput)
	return nil
}

func callDaemonStatus(socketPath string, timeout time.Duration) (daemon.DaemonStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	result, err := daemon.Call(ctx, socketPath, daemon.Request{Method: "daemon.status"})
	if err != nil {
		return daemon.DaemonStatus{}, err
	}
	var status daemon.DaemonStatus
	if err := json.Unmarshal(result, &status); err != nil {
		return daemon.DaemonStatus{}, err
	}
	return status, nil
}

func waitForDaemon(socketPath string, timeout time.Duration) (daemon.DaemonStatus, error) {
	deadline := time.Now().Add(timeout)
	for {
		status, err := callDaemonStatus(socketPath, time.Second)
		if err == nil && status.State == "running" {
			return status, nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return daemon.DaemonStatus{}, err
			}
			return daemon.DaemonStatus{}, fmt.Errorf("daemon state is %q", status.State)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitForDaemonStopped(socketPath, statePath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		_, err := callDaemonStatus(socketPath, 500*time.Millisecond)
		if err != nil && isDaemonDialError(err) {
			if _, stateErr := daemon.ReadDaemonState(statePath); errors.Is(stateErr, os.ErrNotExist) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("agentsd did not stop within %s", timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func forceStopDaemon(socketPath, statePath string) error {
	state, err := daemon.ReadDaemonState(statePath)
	if err != nil {
		return fmt.Errorf("read daemon state: %w", err)
	}
	if state.SocketPath != "" && state.SocketPath != socketPath {
		return fmt.Errorf("daemon state belongs to socket %s", state.SocketPath)
	}
	if state.PID <= 1 || state.Executable == "" {
		return fmt.Errorf("daemon state does not contain a valid process identity")
	}
	command, err := inspectProcessCommand(state.PID)
	if err != nil {
		return fmt.Errorf("inspect daemon process: %w", err)
	}
	if !strings.Contains(command, filepath.Base(state.Executable)) {
		return fmt.Errorf("refusing to kill pid %d: process is %q", state.PID, command)
	}
	process, err := os.FindProcess(state.PID)
	if err != nil {
		return fmt.Errorf("find daemon process: %w", err)
	}
	if err := process.Kill(); err != nil {
		return fmt.Errorf("kill daemon process: %w", err)
	}
	_ = daemon.RemoveDaemonState(statePath)
	_ = os.Remove(socketPath)
	_ = os.Remove(daemon.WebSocketSocketPath(socketPath))
	return nil
}

func inspectProcessCommand(pid int) (string, error) {
	if runtime.GOOS == "windows" {
		output, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
		return strings.TrimSpace(string(output)), err
	}
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	return strings.TrimSpace(string(output)), err
}

func resolveDaemonBinary(explicit string) (string, error) {
	if explicit != "" {
		path, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("agentsd executable %q: %w", path, err)
		}
		return path, nil
	}
	if executable, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(executable), "agentsd")
		if runtime.GOOS == "windows" {
			sibling += ".exe"
		}
		if info, statErr := os.Stat(sibling); statErr == nil && info.Mode().IsRegular() {
			return sibling, nil
		}
	}
	path, err := exec.LookPath("agentsd")
	if err != nil {
		return "", fmt.Errorf("find agentsd executable: %w", err)
	}
	return path, nil
}

func stoppedDaemonStatus(statePath string) daemon.DaemonStatus {
	state, err := daemon.ReadDaemonState(statePath)
	if err != nil {
		return daemon.DaemonStatus{State: "stopped"}
	}
	return state.Status("stopped")
}

func printDaemonStatus(status daemon.DaemonStatus, jsonOutput bool) {
	if jsonOutput {
		encoded, err := json.MarshalIndent(status, "", "  ")
		if err == nil {
			fmt.Println(string(encoded))
		}
		return
	}
	if status.State == "stopped" {
		fmt.Println("agentsd stopped")
		return
	}
	fmt.Printf("agentsd %s (pid %d, socket %s)\n", status.State, status.PID, status.SocketPath)
}

func isDaemonDialError(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}
