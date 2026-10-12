package server

import (
	"encoding/json"
	"strings"
	"testing"

	"cruuze/server/internal/api"
)

func TestRerunnableRequestKeepsNoCredentials(t *testing.T) {
	requests := map[string]string{
		"github": `{"source":"github","github":{"repo":"acme/widgets","token":"ghp_secret","baseUrl":"https://github.example.com/api/v3"},"teams":["1"]}`,
		"linear": `{"source":"linear","linear":{"apiKey":"lin_api_secret"},"userMap":{"u1":""}}`,
		"jira":   `{"source":"jira","jira":{"baseUrl":"https://acme.atlassian.net","email":"ada@example.com","token":"jira_secret"},"teams":["10000"],"teamMap":{"10000":"0190a1b2-0000-7000-8000-000000000001"}}`,
	}
	for name, body := range requests {
		var req api.ImportRequest
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		kept := string(rerunnableRequest(req))
		if strings.Contains(kept, "secret") {
			t.Errorf("%s: kept request holds a credential: %s", name, kept)
		}
		// Sent back with the credential, it is the request it came from.
		var again api.ImportRequest
		if err := json.Unmarshal([]byte(kept), &again); err != nil {
			t.Fatalf("%s: kept request doesn't decode: %v", name, err)
		}
		if again.Source != req.Source {
			t.Errorf("%s: source = %q", name, again.Source)
		}
	}

	var jira api.ImportRequest
	_ = json.Unmarshal([]byte(requests["jira"]), &jira)
	kept := string(rerunnableRequest(jira))
	for _, want := range []string{"https://acme.atlassian.net", "ada@example.com", `"10000"`} {
		if !strings.Contains(kept, want) {
			t.Errorf("jira: kept request %s lacks %s", kept, want)
		}
	}

	// A file can't be imported again without the file.
	var csv api.ImportRequest
	_ = json.Unmarshal([]byte(`{"source":"csv","csv":{"text":"Title\nFix it","name":"export"}}`), &csv)
	if kept := string(rerunnableRequest(csv)); kept != "{}" {
		t.Errorf("csv: kept %s, want nothing", kept)
	}
}
