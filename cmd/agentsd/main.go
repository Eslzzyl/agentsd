package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"agentsd/internal/config"
	"agentsd/internal/daemon"
	"agentsd/internal/session"
)

func main() {
	paths, err := config.DefaultPaths()
	if err != nil {
		log.Fatal(err)
	}
	if err := paths.EnsureDirectory(); err != nil {
		log.Fatal(err)
	}
	flags := flag.NewFlagSet("agentsd", flag.ExitOnError)
	socketPath := flags.String("socket", filepath.Join(paths.Directory, "agentsd.sock"), "Unix socket path")
	configPath := flags.String("config", paths.ConfigFile, "config TOML path")
	authPath := flags.String("auth", paths.AuthFile, "auth JSON path")
	statePath := flags.String("state", paths.StateFile, "daemon state path")
	flags.Parse(os.Args[1:])

	absoluteSocketPath, err := filepath.Abs(*socketPath)
	if err != nil {
		log.Fatal(err)
	}
	absoluteStatePath, err := filepath.Abs(*statePath)
	if err != nil {
		log.Fatal(err)
	}
	workspace, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	absoluteExecutable, err := filepath.Abs(executable)
	if err != nil {
		log.Fatal(err)
	}

	store, err := session.NewStore(paths.SessionsDirectory)
	if err != nil {
		log.Fatal(err)
	}
	manager, err := daemon.NewManager((daemon.ConfigFactory{ConfigPath: *configPath, AuthPath: *authPath}).New, store)
	if err != nil {
		log.Fatal(err)
	}
	listener, err := daemon.ListenUnix(absoluteSocketPath)
	if err != nil {
		log.Fatal(err)
	}
	webSocketSocketPath := daemon.WebSocketSocketPath(absoluteSocketPath)
	webSocketListener, err := daemon.ListenUnix(webSocketSocketPath)
	if err != nil {
		_ = listener.Close()
		_ = os.Remove(absoluteSocketPath)
		log.Fatal(err)
	}

	startedAt := time.Now().UTC()
	state := daemon.DaemonState{
		PID:                 os.Getpid(),
		StartedAt:           startedAt,
		SocketPath:          absoluteSocketPath,
		WebSocketSocketPath: webSocketSocketPath,
		Workspace:           workspace,
		Executable:          absoluteExecutable,
	}
	if err := daemon.WriteDaemonState(absoluteStatePath, state); err != nil {
		_ = listener.Close()
		_ = webSocketListener.Close()
		_ = os.Remove(absoluteSocketPath)
		_ = os.Remove(webSocketSocketPath)
		log.Fatal(err)
	}
	defer func() {
		_ = listener.Close()
		_ = webSocketListener.Close()
		_ = os.Remove(absoluteSocketPath)
		_ = os.Remove(webSocketSocketPath)
		current, err := daemon.ReadDaemonState(absoluteStatePath)
		if err == nil && current.PID == os.Getpid() {
			_ = daemon.RemoveDaemonState(absoluteStatePath)
		}
	}()

	server := daemon.NewServer(manager)
	server.SetDaemonStatus(state.Status("running"))
	shutdownDone := make(chan struct{})
	var shutdownOnce sync.Once
	var shutdownErr error
	requestShutdown := func() {
		shutdownOnce.Do(func() {
			server.SetDaemonState("stopping")
			_ = listener.Close()
			_ = webSocketListener.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			shutdownErr = manager.Shutdown(ctx)
			cancel()
			close(shutdownDone)
		})
	}
	server.SetShutdownHandler(requestShutdown)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		<-signals
		requestShutdown()
	}()
	go func() {
		_ = server.ServeWebSocket(webSocketListener)
	}()

	serveErr := server.Serve(listener)
	exitCode := 0
	if serveErr != nil {
		fmt.Fprintln(os.Stderr, serveErr)
		exitCode = 1
	}
	requestShutdown()
	<-shutdownDone
	if shutdownErr != nil {
		fmt.Fprintln(os.Stderr, shutdownErr)
		exitCode = 1
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}
