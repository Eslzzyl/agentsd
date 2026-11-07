package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"

	"agentsd/internal/session"
	"agentsd/internal/tools"
)

type Server struct {
	manager      *Manager
	lifecycleMu  sync.RWMutex
	status       DaemonStatus
	shutdownOnce sync.Once
	shutdownFunc func()
}

func NewServer(manager *Manager) *Server {
	return &Server{
		manager: manager,
		status:  DaemonStatus{State: "running"},
	}
}

func (server *Server) SetDaemonStatus(status DaemonStatus) {
	if status.State == "" {
		status.State = "running"
	}
	server.lifecycleMu.Lock()
	server.status = status
	server.lifecycleMu.Unlock()
}

func (server *Server) SetDaemonState(state string) {
	server.lifecycleMu.Lock()
	server.status.State = state
	server.lifecycleMu.Unlock()
}

func (server *Server) SetShutdownHandler(handler func()) {
	server.lifecycleMu.Lock()
	server.shutdownFunc = handler
	server.lifecycleMu.Unlock()
}

func (server *Server) daemonStatus() DaemonStatus {
	server.lifecycleMu.RLock()
	defer server.lifecycleMu.RUnlock()
	return server.status
}

func (server *Server) requestShutdown() error {
	server.lifecycleMu.RLock()
	handler := server.shutdownFunc
	server.lifecycleMu.RUnlock()
	if handler == nil {
		return errors.New("daemon: shutdown is unavailable")
	}
	server.shutdownOnce.Do(func() {
		go handler()
	})
	return nil
}

func (server *Server) Serve(listener net.Listener) error {
	for {
		connection, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go server.handleConnection(connection)
	}
}

func (server *Server) handleConnection(connection net.Conn) {
	defer connection.Close()
	decoder := json.NewDecoder(connection)
	encoder := json.NewEncoder(connection)
	for {
		var request Request
		if err := decoder.Decode(&request); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			_ = encoder.Encode(rpcResponse{Error: err.Error()})
			return
		}
		result, err := server.handleRequest(request)
		if err != nil {
			_ = encoder.Encode(rpcResponse{Error: err.Error()})
			continue
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			_ = encoder.Encode(rpcResponse{Error: err.Error()})
			continue
		}
		if err := encoder.Encode(rpcResponse{Result: encoded}); err != nil {
			return
		}
	}
}

func (server *Server) handleRequest(request Request) (any, error) {
	switch request.Method {
	case "daemon.status":
		return server.daemonStatus(), nil
	case "daemon.shutdown":
		if err := server.requestShutdown(); err != nil {
			return nil, err
		}
		return map[string]string{"state": "stopping"}, nil
	case "spawn":
		var params struct {
			Namespace string `json:"namespace"`
			Model     string `json:"model"`
			Task      string `json:"task"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return server.manager.SpawnInNamespace(params.Namespace, params.Model, params.Task)
	case "namespace.create":
		var params struct {
			Name string `json:"name"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		if err := server.manager.CreateNamespace(params.Name); err != nil {
			return nil, err
		}
		return NamespaceResult{Name: params.Name}, nil
	case "namespace.delete":
		var params struct {
			Name  string `json:"name"`
			Force bool   `json:"force"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		if err := server.manager.DeleteNamespace(params.Name, params.Force); err != nil {
			return nil, err
		}
		return NamespaceResult{Name: params.Name}, nil
	case "namespace.rename":
		var params struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		if err := server.manager.RenameNamespace(params.From, params.To); err != nil {
			return nil, err
		}
		return NamespaceRenameResult{From: params.From, To: params.To}, nil
	case "namespace.list":
		return server.manager.ListNamespaces()
	case "ps":
		var params struct {
			Namespace     string `json:"namespace"`
			AllNamespaces bool   `json:"all_namespaces"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		if params.AllNamespaces {
			return server.manager.ListAll(), nil
		}
		if strings.TrimSpace(params.Namespace) == "" {
			return nil, errors.New("session: namespace is required")
		}
		return server.manager.ListInNamespace(params.Namespace), nil
	case "inspect":
		var params struct {
			Namespace string `json:"namespace"`
			ID        string `json:"id"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return server.manager.InspectInNamespace(params.Namespace, params.ID)
	case "session.compact":
		var params struct {
			Namespace string `json:"namespace"`
			ID        string `json:"id"`
			Wait      bool   `json:"wait"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return server.manager.CompactInNamespace(params.Namespace, params.ID, params.Wait)
	case "session.send":
		var params struct {
			Namespace string            `json:"namespace"`
			ID        string            `json:"id"`
			Mode      tools.MessageMode `json:"mode"`
			Content   string            `json:"content"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return server.manager.SendInNamespace(params.Namespace, params.ID, params.Content, params.Mode)
	case "session.inbox":
		var params struct {
			Namespace string `json:"namespace"`
			ID        string `json:"id"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return server.manager.InboxInNamespace(params.Namespace, params.ID)
	case "session.events":
		var params struct {
			Namespace string `json:"namespace"`
			ID        string `json:"id"`
			After     string `json:"after"`
			Before    string `json:"before"`
			FromSeq   uint64 `json:"from_seq"`
			ToSeq     uint64 `json:"to_seq"`
			Limit     int    `json:"limit"`
			Tail      bool   `json:"tail"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return server.manager.QueryEventPageInNamespace(params.Namespace, params.ID, session.EventQuery{
			After:   params.After,
			Before:  params.Before,
			FromSeq: params.FromSeq,
			ToSeq:   params.ToSeq,
			Limit:   params.Limit,
			Tail:    params.Tail,
		})
	case "kill":
		var params struct {
			Namespace string `json:"namespace"`
			ID        string `json:"id"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return server.manager.KillInNamespace(params.Namespace, params.ID)
	default:
		return nil, errors.New("daemon: unknown method " + request.Method)
	}
}

func decodeParams(data json.RawMessage, destination any) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, destination)
}
