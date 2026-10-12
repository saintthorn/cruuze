// Package mcp serves the Model Context Protocol over HTTP: enough of it for
// an assistant to list a server's tools and call them. It is the Streamable
// HTTP transport without streams: every request gets one JSON answer.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
)

// versions are the protocol revisions this server speaks, newest first. A
// client that asks for one of them gets it; any other gets the newest.
var versions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

const maxRequestBytes = 1 << 20

// JSON-RPC error codes.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Tool is something the assistant can do.
type Tool struct {
	Name string
	// Title is the name people see.
	Title       string
	Description string
	// Input is the JSON Schema of the arguments; nil means none are taken.
	Input map[string]any
	// ReadOnly says the tool changes nothing, so clients needn't ask first.
	ReadOnly bool
	// Call runs the tool. What it returns is sent to the assistant: a string
	// as it is, anything else as JSON. An *Error is shown to the assistant
	// so it can correct itself; any other error is reported and hidden.
	Call func(ctx context.Context, r *http.Request, args json.RawMessage) (any, error)
}

// Error is a failure the assistant should read: a status that doesn't
// exist, a missing argument. It is the tool's answer, not a protocol error.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

// Fail makes a tool error with the given message.
func Fail(message string) *Error { return &Error{Message: message} }

// Server answers MCP requests for a fixed set of tools.
type Server struct {
	Name    string
	Title   string
	Version string
	// Instructions tell the assistant how the tools fit together.
	Instructions string
	Tools        []Tool
	// OnError is told about failures that were hidden from the assistant.
	OnError func(r *http.Request, tool string, err error)
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callResult struct {
	Content []content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// No server-initiated stream to open, and no session to end.
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "this MCP endpoint takes POST only", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		write(w, response{ID: json.RawMessage("null"), Error: &rpcError{codeParseError, "request too large or unreadable"}})
		return
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		write(w, response{ID: json.RawMessage("null"), Error: &rpcError{codeParseError, "not a JSON-RPC message (batches are not supported)"}})
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		id := req.ID
		if id == nil {
			id = json.RawMessage("null")
		}
		write(w, response{ID: id, Error: &rpcError{codeInvalidRequest, "not a JSON-RPC 2.0 request"}})
		return
	}
	// A message without an ID is a notification (or a response): nothing to answer.
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	result, rpcErr := s.handle(r, req)
	write(w, response{ID: req.ID, Result: result, Error: rpcErr})
}

func write(w http.ResponseWriter, res response) {
	res.JSONRPC = "2.0"
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res) //nolint:errcheck // client went away
}

func (s *Server) handle(r *http.Request, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		version := versions[0]
		if slices.Contains(versions, params.ProtocolVersion) {
			version = params.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": s.Name, "title": s.Title, "version": s.Version},
			"instructions":    s.Instructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		tools := make([]map[string]any, 0, len(s.Tools))
		for _, tool := range s.Tools {
			input := tool.Input
			if input == nil {
				input = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tools = append(tools, map[string]any{
				"name":        tool.Name,
				"title":       tool.Title,
				"description": tool.Description,
				"inputSchema": input,
				"annotations": map[string]any{
					"title":        tool.Title,
					"readOnlyHint": tool.ReadOnly,
					// Nothing here deletes, and nothing reaches beyond this server.
					"destructiveHint": false,
					"openWorldHint":   false,
				},
			})
		}
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &rpcError{codeInvalidParams, "tools/call needs a name and arguments"}
		}
		i := slices.IndexFunc(s.Tools, func(t Tool) bool { return t.Name == params.Name })
		if i < 0 {
			return nil, &rpcError{codeInvalidParams, "unknown tool: " + params.Name}
		}
		if len(params.Arguments) == 0 || string(params.Arguments) == "null" {
			params.Arguments = json.RawMessage("{}")
		}
		return s.call(r, s.Tools[i], params.Arguments), nil
	default:
		return nil, &rpcError{codeMethodNotFound, "method not found: " + req.Method}
	}
}

func (s *Server) call(r *http.Request, tool Tool, args json.RawMessage) callResult {
	out, err := tool.Call(r.Context(), r, args)
	if err != nil {
		var toolErr *Error
		if !errors.As(err, &toolErr) {
			if s.OnError != nil {
				s.OnError(r, tool.Name, err)
			}
			toolErr = Fail("Something went wrong on the server. It has been logged; trying again may work.")
		}
		return callResult{Content: []content{{Type: "text", Text: toolErr.Message}}, IsError: true}
	}
	text, ok := out.(string)
	if !ok {
		encoded, err := json.Marshal(out)
		if err != nil {
			if s.OnError != nil {
				s.OnError(r, tool.Name, err)
			}
			return callResult{Content: []content{{Type: "text", Text: "The result could not be encoded."}}, IsError: true}
		}
		text = string(encoded)
	}
	return callResult{Content: []content{{Type: "text", Text: text}}}
}

// Decode reads a tool's arguments into v, refusing ones it doesn't know so
// a misspelt argument is reported instead of ignored.
func Decode(args json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return Fail("Invalid arguments: " + err.Error())
	}
	return nil
}
