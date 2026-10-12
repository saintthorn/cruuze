package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testServer(hidden *error) *Server {
	return &Server{
		Name: "test", Title: "Test", Version: "1", Instructions: "Use echo.",
		Tools: []Tool{
			{
				Name: "echo", Title: "Echo", Description: "Says it back.", ReadOnly: true,
				Input: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}},
				Call: func(_ context.Context, _ *http.Request, args json.RawMessage) (any, error) {
					var in struct {
						Text string `json:"text"`
					}
					if err := Decode(args, &in); err != nil {
						return nil, err
					}
					if in.Text == "" {
						return nil, Fail("text is required")
					}
					return map[string]string{"said": in.Text}, nil
				},
			},
			{
				Name: "plain", Title: "Plain", Description: "Answers in text.",
				Call: func(context.Context, *http.Request, json.RawMessage) (any, error) { return "hello", nil },
			},
			{
				Name: "broken", Title: "Broken", Description: "Fails.",
				Call: func(context.Context, *http.Request, json.RawMessage) (any, error) {
					return nil, errors.New("database password is hunter2")
				},
			},
		},
		OnError: func(_ *http.Request, _ string, err error) { *hidden = err },
	}
}

func post(t *testing.T, s *Server, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body)))
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("response is not JSON: %q", rec.Body.String())
		}
	}
	return rec.Code, out
}

func TestHandshake(t *testing.T) {
	s := testServer(new(error))
	status, out := post(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	result, _ := out["result"].(map[string]any)
	if status != http.StatusOK || out["id"] != float64(1) || result["protocolVersion"] != "2025-03-26" || result["instructions"] != "Use echo." {
		t.Fatalf("initialize: %d %v", status, out)
	}
	if _, ok := result["capabilities"].(map[string]any)["tools"]; !ok {
		t.Errorf("capabilities = %v, want tools", result["capabilities"])
	}
	// A revision we don't know gets the newest we do.
	_, out = post(t, s, `{"jsonrpc":"2.0","id":"a","method":"initialize","params":{"protocolVersion":"2099-01-01"}}`)
	if got := out["result"].(map[string]any)["protocolVersion"]; got != versions[0] || out["id"] != "a" {
		t.Errorf("unknown revision answered with %v (id %v)", got, out["id"])
	}
	// Notifications are accepted and not answered.
	if status, out := post(t, s, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); status != http.StatusAccepted || out != nil {
		t.Errorf("notification: %d %v", status, out)
	}
	if _, out := post(t, s, `{"jsonrpc":"2.0","id":2,"method":"ping"}`); out["result"] == nil || out["error"] != nil {
		t.Errorf("ping: %v", out)
	}
}

func TestToolsList(t *testing.T) {
	_, out := post(t, testServer(new(error)), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := out["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("%d tools, want 3", len(tools))
	}
	echo := tools[0].(map[string]any)
	if echo["name"] != "echo" || echo["annotations"].(map[string]any)["readOnlyHint"] != true || echo["inputSchema"].(map[string]any)["type"] != "object" {
		t.Errorf("echo = %v", echo)
	}
	// A tool without arguments still has a schema.
	if schema := tools[1].(map[string]any)["inputSchema"].(map[string]any); schema["type"] != "object" {
		t.Errorf("plain's schema = %v", schema)
	}
}

func TestToolsCall(t *testing.T) {
	var hidden error
	s := testServer(&hidden)
	text := func(out map[string]any) (string, bool) {
		t.Helper()
		result, ok := out["result"].(map[string]any)
		if !ok {
			t.Fatalf("no result: %v", out)
		}
		isError, _ := result["isError"].(bool)
		return result["content"].([]any)[0].(map[string]any)["text"].(string), isError
	}

	_, out := post(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`)
	if got, isError := text(out); got != `{"said":"hi"}` || isError {
		t.Errorf("echo = %q, error %v", got, isError)
	}
	_, out = post(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"plain"}}`)
	if got, _ := text(out); got != "hello" {
		t.Errorf("plain = %q", got)
	}
	// What the assistant got wrong, it is told.
	_, out = post(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"txt":"hi"}}}`)
	if got, isError := text(out); !isError || !strings.Contains(got, "txt") {
		t.Errorf("misspelt argument: %q, error %v", got, isError)
	}
	_, out = post(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{}}}`)
	if got, isError := text(out); !isError || got != "text is required" {
		t.Errorf("missing argument: %q, error %v", got, isError)
	}
	// What went wrong inside, it is not.
	_, out = post(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"broken"}}`)
	if got, isError := text(out); !isError || strings.Contains(got, "hunter2") || hidden == nil {
		t.Errorf("internal failure: %q, error %v, logged %v", got, isError, hidden)
	}
}

func TestProtocolErrors(t *testing.T) {
	s := testServer(new(error))
	code := func(out map[string]any) float64 {
		t.Helper()
		e, ok := out["error"].(map[string]any)
		if !ok {
			t.Fatalf("no error: %v", out)
		}
		return e["code"].(float64)
	}
	if _, out := post(t, s, `{not json`); code(out) != codeParseError {
		t.Errorf("bad JSON: %v", out)
	}
	if _, out := post(t, s, `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`); code(out) != codeParseError {
		t.Errorf("a batch: %v", out)
	}
	if _, out := post(t, s, `{"jsonrpc":"1.0","id":1,"method":"ping"}`); code(out) != codeInvalidRequest {
		t.Errorf("wrong version: %v", out)
	}
	if _, out := post(t, s, `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`); code(out) != codeMethodNotFound {
		t.Errorf("unknown method: %v", out)
	}
	if _, out := post(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope"}}`); code(out) != codeInvalidParams {
		t.Errorf("unknown tool: %v", out)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "POST" {
		t.Errorf("GET: %d, Allow %q", rec.Code, rec.Header().Get("Allow"))
	}
}
