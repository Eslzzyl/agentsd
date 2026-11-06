package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
)

type Server struct {
	manager *Manager
}

func NewServer(manager *Manager) *Server {
	return &Server{manager: manager}
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
