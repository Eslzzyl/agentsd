package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

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
	flags.Parse(os.Args[1:])

	store, err := session.NewStore(paths.SessionsDirectory)
	if err != nil {
		log.Fatal(err)
	}
	manager, err := daemon.NewManager((daemon.ConfigFactory{ConfigPath: *configPath, AuthPath: *authPath}).New, store)
	if err != nil {
		log.Fatal(err)
	}
	listener, err := daemon.ListenUnix(*socketPath)
	if err != nil {
		log.Fatal(err)
	}
	webSocketSocketPath := daemon.WebSocketSocketPath(*socketPath)
	webSocketListener, err := daemon.ListenUnix(webSocketSocketPath)
	if err != nil {
		_ = listener.Close()
		_ = os.Remove(*socketPath)
		log.Fatal(err)
	}
	defer func() {
		_ = listener.Close()
		_ = webSocketListener.Close()
		_ = os.Remove(*socketPath)
		_ = os.Remove(webSocketSocketPath)
	}()

	server := daemon.NewServer(manager)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		<-signals
		_ = listener.Close()
		_ = webSocketListener.Close()
	}()
	go func() {
		_ = server.ServeWebSocket(webSocketListener)
	}()

	if err := server.Serve(listener); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
