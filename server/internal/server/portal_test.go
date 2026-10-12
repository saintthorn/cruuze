package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
)

func (c *client) portal(t *testing.T, urlKey string) api.Portal {
	t.Helper()
	var out api.Portal
	c.expect(http.StatusOK, "GET", "/api/portal/"+urlKey, nil, &out)
	return out
}

func (c *client) request(workspaceID, id uuid.UUID) api.CustomerRequest {
	c.t.Helper()
	for _, r := range c.bootstrap(workspaceID).CustomerRequests {
		if r.Id == id {
			return r
		}
	}
	c.t.Fatalf("request %s is not in the bootstrap", id)
	return api.CustomerRequest{}
}

func TestCustomerPortal(t *testing.T) {
	env := newTestEnv(t)
	ada, bob, _, bobMe, ws, team := workspaceWithMember(t, env)
	anon := env.client(t)
	portalPath := "/api/portal/" + ws.UrlKey

	// Closed until an admin opens it.
	anon.expect(http.StatusNotFound, "GET", portalPath, nil, nil)
	bob.expect(http.StatusForbidden, "PATCH", "/api/workspaces/"+ws.Id.String()+"/portal", map[string]any{"enabled": true}, nil)
	var opened api.Workspace
	ada.expect(http.StatusOK, "PATCH", "/api/workspaces/"+ws.Id.String()+"/portal", map[string]any{"enabled": true, "intro": " Tell us what you need. "}, &opened)
	if !opened.PortalEnabled || opened.PortalPublicBoard || opened.PortalIntro != "Tell us what you need." {
		t.Fatalf("workspace after opening the portal: %+v", opened)
	}
	if got := anon.portal(t, ws.UrlKey); got.Name != ws.Name || got.Email != nil || len(got.Requests) != 0 || got.PublicBoard {
		t.Errorf("portal for a stranger: %+v", got)
	}
	anon.expect(http.StatusUnauthorized, "POST", portalPath+"/requests", map[string]any{"id": uuid.New(), "title": "Let me in"}, nil)

	// The sign-in link asked for from the portal leads back to it.
	anon.expect(http.StatusNoContent, "POST", "/api/auth/magic-link", map[string]any{"email": "cust@example.com", "portal": ws.UrlKey}, nil)
	message := env.mail.lastTo(t, "cust@example.com")
	if !strings.Contains(message.Text, "/auth/verify?next=%2Fportal%2F"+ws.UrlKey+"#token=") || !strings.Contains(message.Subject, ws.Name) {
		t.Errorf("portal sign-in email: %q / %q", message.Subject, message.Text)
	}
	cust := env.client(t)
	cust.expect(http.StatusOK, "POST", "/api/auth/verify", api.VerifyRequest{Token: tokenPattern.FindStringSubmatch(message.Text)[1]}, nil)

	// A customer sends a request; the team sees it, the customer sees their own.
	id := uuid.New()
	body := map[string]any{"id": id, "title": " Export to CSV ", "body": "For our accountant."}
	var sent api.PortalRequest
	cust.expect(http.StatusCreated, "POST", portalPath+"/requests", body, &sent)
	cust.expect(http.StatusOK, "POST", portalPath+"/requests", body, nil)
	cust.expect(http.StatusBadRequest, "POST", portalPath+"/requests", map[string]any{"id": uuid.New(), "title": " "}, nil)
	if sent.Title != "Export to CSV" || !sent.Mine || sent.Status != api.CustomerRequestStatusNew {
		t.Errorf("request as sent: %+v", sent)
	}
	if got := cust.portal(t, ws.UrlKey); got.Email == nil || *got.Email != "cust@example.com" || len(got.Requests) != 1 || !got.Requests[0].Mine {
		t.Errorf("portal for the customer: %+v", got)
	}
	if got := bob.request(ws.Id, id); got.RequesterEmail != "cust@example.com" || got.Title != "Export to CSV" {
		t.Errorf("request as the team sees it: %+v", got)
	}
	// A customer has no way into the workspace itself.
	cust.expect(http.StatusNotFound, "GET", "/api/workspaces/"+ws.Id.String()+"/sync/bootstrap", nil, nil)
	cust.expect(http.StatusNotFound, "PATCH", "/api/requests/"+id.String(), map[string]any{"status": "shipped"}, nil)

	// Someone else sees nothing of it: there is no public board yet.
	other, _ := env.signIn(t, "other@example.com")
	if got := other.portal(t, ws.UrlKey); len(got.Requests) != 0 {
		t.Errorf("another visitor sees %+v", got.Requests)
	}
	other.expect(http.StatusNotFound, "PUT", portalPath+"/requests/"+id.String()+"/vote", nil, nil)

	// The team answers, links an issue and puts it on the board.
	issue := ada.createIssue(team.Id, "CSV export").Issue
	bob.expect(http.StatusBadRequest, "PATCH", "/api/requests/"+id.String(), map[string]any{"status": "someday"}, nil)
	bob.expect(http.StatusBadRequest, "PATCH", "/api/requests/"+id.String(), map[string]any{"issueId": uuid.New()}, nil)
	bob.expect(http.StatusOK, "PATCH", "/api/requests/"+id.String(), map[string]any{
		"status": "planned", "public": true, "response": "On the list for this quarter.", "issueId": issue.Id,
	}, nil)
	if got := ada.request(ws.Id, id); got.Status != api.CustomerRequestStatusPlanned || !got.Public || got.IssueId == nil || *got.IssueId != issue.Id {
		t.Errorf("after triage: %+v", got)
	}
	// Public means nothing until the board is on.
	other.expect(http.StatusNotFound, "PUT", portalPath+"/requests/"+id.String()+"/vote", nil, nil)
	ada.expect(http.StatusOK, "PATCH", "/api/workspaces/"+ws.Id.String()+"/portal", map[string]any{"publicBoard": true}, nil)
	if got := anon.portal(t, ws.UrlKey); len(got.Requests) != 1 || got.Requests[0].Mine || got.Requests[0].Response == "" {
		t.Errorf("public board for a stranger: %+v", got.Requests)
	}
	var voted api.PortalRequest
	other.expect(http.StatusOK, "PUT", portalPath+"/requests/"+id.String()+"/vote", nil, &voted)
	other.expect(http.StatusOK, "PUT", portalPath+"/requests/"+id.String()+"/vote", nil, &voted)
	if voted.VoteCount != 1 || !voted.Voted || voted.Mine {
		t.Errorf("after voting twice: %+v", voted)
	}
	if got := ada.request(ws.Id, id).VoteCount; got != 1 {
		t.Errorf("votes the team sees = %d, want 1", got)
	}
	if got := other.portal(t, ws.UrlKey).Requests; len(got) != 1 || !got[0].Voted {
		t.Errorf("board for the voter: %+v", got)
	}

	// The team writes one down for a customer who emailed.
	noted := uuid.New()
	bob.expect(http.StatusCreated, "POST", "/api/workspaces/"+ws.Id.String()+"/requests", map[string]any{
		"id": noted, "title": "Dark mode", "requesterEmail": "Other@Example.com",
	}, nil)
	bob.expect(http.StatusBadRequest, "POST", "/api/workspaces/"+ws.Id.String()+"/requests", map[string]any{
		"id": uuid.New(), "title": "No address", "requesterEmail": "nobody",
	}, nil)
	// It is theirs when they come to the portal.
	if got := other.portal(t, ws.UrlKey).Requests; len(got) != 2 {
		t.Errorf("the visitor's requests and the board: %+v", got)
	}

	// Guests don't get customers' requests, at bootstrap or after.
	ada.expect(http.StatusOK, "PATCH", "/api/workspaces/"+ws.Id.String()+"/members/"+bobMe.User.Id.String(), map[string]any{"role": "guest"}, nil)
	if got := bob.bootstrap(ws.Id).CustomerRequests; len(got) != 0 {
		t.Errorf("a guest's bootstrap has %d requests", len(got))
	}
	cursor := ada.lastSyncID(ws.Id)
	ada.expect(http.StatusOK, "PATCH", "/api/requests/"+noted.String(), map[string]any{"status": "reviewing"}, nil)
	if got := only(env.fetch(t, ws.Id, bobMe.User, api.WorkspaceRoleGuest, cursor), api.SyncModelCustomerRequest); len(got) != 0 {
		t.Errorf("a guest's sync feed has requests: %v", got)
	}
	if got := only(env.fetch(t, ws.Id, bobMe.User, api.WorkspaceRoleMember, cursor), api.SyncModelCustomerRequest); len(got) != 1 {
		t.Errorf("a member's sync feed: %v", got)
	}
	bob.expect(http.StatusForbidden, "PATCH", "/api/requests/"+noted.String(), map[string]any{"status": "planned"}, nil)

	// Completing the issue ships the request; reopening it takes that back.
	boot := ada.bootstrap(ws.Id)
	done := stateOfType(t, boot, team.Id, api.WorkflowStateTypeCompleted)
	todo := stateOfType(t, boot, team.Id, api.WorkflowStateTypeUnstarted)
	ada.expect(http.StatusOK, "PATCH", "/api/issues/"+issue.Id.String(), map[string]any{"stateId": done.Id}, nil)
	if got := ada.request(ws.Id, id).Status; got != api.CustomerRequestStatusShipped {
		t.Fatalf("after completing the issue: %s", got)
	}
	if sent, err := env.srv.notifyShippedRequests(t.Context(), time.Now().Add(-time.Minute)); err != nil || sent != 0 {
		t.Errorf("notices before the delay: %d, %v", sent, err)
	}
	ada.expect(http.StatusOK, "PATCH", "/api/issues/"+issue.Id.String(), map[string]any{"stateId": todo.Id}, nil)
	if got := ada.request(ws.Id, id).Status; got != api.CustomerRequestStatusPlanned {
		t.Errorf("after reopening the issue: %s", got)
	}
	ada.expect(http.StatusOK, "PATCH", "/api/issues/"+issue.Id.String(), map[string]any{"stateId": done.Id}, nil)

	// The requester and the voter are each told, once.
	sentCount, err := env.srv.notifyShippedRequests(t.Context(), time.Now().Add(time.Minute))
	if err != nil || sentCount != 2 {
		t.Fatalf("notices: %d, %v; want 2", sentCount, err)
	}
	notice := env.mail.lastTo(t, "cust@example.com")
	if notice.Subject != "Shipped: Export to CSV" || !strings.Contains(notice.Text, "On the list for this quarter.") || !strings.Contains(notice.Text, "/portal/"+ws.UrlKey) {
		t.Errorf("notice: %q / %q", notice.Subject, notice.Text)
	}
	if env.mail.lastTo(t, "other@example.com").Subject != notice.Subject {
		t.Error("the voter wasn't told")
	}
	if again, _ := env.srv.notifyShippedRequests(t.Context(), time.Now().Add(time.Minute)); again != 0 {
		t.Errorf("%d notices sent a second time", again)
	}
	// Once people have been told, reopening the issue doesn't unship it.
	ada.expect(http.StatusOK, "PATCH", "/api/issues/"+issue.Id.String(), map[string]any{"stateId": todo.Id}, nil)
	if got := ada.request(ws.Id, id).Status; got != api.CustomerRequestStatusShipped {
		t.Errorf("after reopening a shipped, announced request: %s", got)
	}

	// Spam can be deleted; a closed portal is gone for everyone.
	ada.expect(http.StatusOK, "DELETE", "/api/requests/"+noted.String(), nil, nil)
	if got := len(ada.bootstrap(ws.Id).CustomerRequests); got != 1 {
		t.Errorf("%d requests after deleting one, want 1", got)
	}
	ada.expect(http.StatusOK, "PATCH", "/api/workspaces/"+ws.Id.String()+"/portal", map[string]any{"enabled": false}, nil)
	cust.expect(http.StatusNotFound, "GET", portalPath, nil, nil)
	cust.expect(http.StatusNotFound, "POST", portalPath+"/requests", map[string]any{"id": uuid.New(), "title": "Too late"}, nil)
}
