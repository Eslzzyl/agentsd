package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"net"
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
			Model string `json:"model"`
			Task  string `json:"task"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return server.manager.Spawn(params.Model, params.Task)
	case "ps":
		return server.manager.List(), nil
	case "inspect":
		var params struct {
			ID string `json:"id"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return server.manager.Inspect(params.ID)
	case "kill":
		var params struct {
			ID string `json:"id"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return server.manager.Kill(params.ID)
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
