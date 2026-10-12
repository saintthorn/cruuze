package server

import (
	"net/http"
	"testing"

	"cruuze/server/internal/api"
)

func TestLabelsAndIssueProperties(t *testing.T) {
	env := newTestEnv(t)
	ada, adaMe := env.signIn(t, "ada@example.com")
	ws := ada.createWorkspace("Acme", "acme")
	base := "/api/workspaces/" + ws.Id.String()
	team := ada.bootstrap(ws.Id).Teams[0]

	// Create, with idempotent retry.
	bugReq := api.CreateLabelRequest{Id: newID(), Name: " Bug ", Color: "#EB5757"}
	var bug, retried api.LabelMutationResult
	ada.expect(http.StatusCreated, "POST", base+"/labels", bugReq, &bug)
	if bug.Label.Name != "Bug" || bug.Label.Color != "#eb5757" {
		t.Errorf("label = %+v, want trimmed name and lowercase color", bug.Label)
	}
	ada.expect(http.StatusOK, "POST", base+"/labels", bugReq, &retried)
	if retried.Label.Id != bug.Label.Id {
		t.Errorf("retry returned a different label: %+v", retried.Label)
	}
	// Names are unique ignoring case; colors must be hex.
	ada.expect(http.StatusConflict, "POST", base+"/labels", api.CreateLabelRequest{Id: newID(), Name: "bug", Color: "#000000"}, nil)
	ada.expect(http.StatusBadRequest, "POST", base+"/labels", api.CreateLabelRequest{Id: newID(), Name: "Feature", Color: "red"}, nil)
	ada.expect(http.StatusBadRequest, "POST", base+"/labels", api.CreateLabelRequest{Id: newID(), Name: "  ", Color: "#000000"}, nil)

	var feature api.LabelMutationResult
	ada.expect(http.StatusCreated, "POST", base+"/labels", api.CreateLabelRequest{Id: newID(), Name: "Feature", Color: "#5e6ad2"}, &feature)
	ada.expect(http.StatusConflict, "PATCH", "/api/labels/"+feature.Label.Id.String(), api.UpdateLabelRequest{Name: &bug.Label.Name}, nil)

	if boot := ada.bootstrap(ws.Id); len(boot.Labels) != 2 || boot.Labels[0].Name != "Bug" {
		t.Errorf("bootstrap labels = %+v, want Bug and Feature", boot.Labels)
	}

	// An issue created with every property.
	issuesPath := "/api/teams/" + team.Id.String() + "/issues"
	var created api.IssueMutationResult
	ada.expect(http.StatusCreated, "POST", issuesPath, map[string]any{
		"id": newID(), "title": "Crash on save", "priority": 1, "dueDate": "2026-12-24",
		"labelIds": []any{bug.Label.Id, bug.Label.Id}, "assigneeId": adaMe.User.Id,
	}, &created)
	issue := created.Issue
	if len(issue.LabelIds) != 1 || issue.LabelIds[0] != bug.Label.Id || issue.DueDate == nil || issue.DueDate.Format("2006-01-02") != "2026-12-24" || issue.Priority != 1 {
		t.Errorf("created issue = %+v", issue)
	}
	issuePath := "/api/issues/" + issue.Id.String()

	// Labels are added and removed, not replaced.
	var relabeled api.IssueMutationResult
	ada.expect(http.StatusOK, "PATCH", issuePath, map[string]any{"addLabelIds": []any{feature.Label.Id, bug.Label.Id}}, &relabeled)
	if len(relabeled.Issue.LabelIds) != 2 || relabeled.Issue.LabelIds[1] != feature.Label.Id {
		t.Errorf("after adding a label: %v", relabeled.Issue.LabelIds)
	}
	var unlabeled api.IssueMutationResult
	ada.expect(http.StatusOK, "PATCH", issuePath, map[string]any{"removeLabelIds": []any{bug.Label.Id}}, &unlabeled)
	if len(unlabeled.Issue.LabelIds) != 1 || unlabeled.Issue.LabelIds[0] != feature.Label.Id {
		t.Errorf("after removing a label: %v", unlabeled.Issue.LabelIds)
	}
	// Due dates survive unrelated updates and clear with null.
	if unlabeled.Issue.DueDate == nil {
		t.Errorf("due date was lost by a label update: %+v", unlabeled.Issue)
	}
	var cleared api.IssueMutationResult
	ada.expect(http.StatusOK, "PATCH", issuePath, map[string]any{"dueDate": nil}, &cleared)
	if cleared.Issue.DueDate != nil || len(cleared.Issue.LabelIds) != 1 {
		t.Errorf("after clearing: %+v", cleared.Issue)
	}
	var rescheduled api.IssueMutationResult
	ada.expect(http.StatusOK, "PATCH", issuePath, map[string]any{"dueDate": "2027-01-15"}, &rescheduled)
	if rescheduled.Issue.DueDate.Format("2006-01-02") != "2027-01-15" {
		t.Errorf("after rescheduling: %+v", rescheduled.Issue)
	}

	for name, body := range map[string]map[string]any{
		"unknown label": {"addLabelIds": []any{newID()}},
		"bad date":      {"dueDate": "next week"},
	} {
		if got := ada.do("PATCH", issuePath, body, nil); got != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, got)
		}
	}
	ada.expect(http.StatusBadRequest, "POST", issuesPath, map[string]any{"id": newID(), "title": "x", "labelIds": []any{newID()}}, nil)

	// Renaming and deleting are logged; deleting takes the label off issues.
	before := ada.bootstrap(ws.Id).LastSyncId
	newName := "Enhancement"
	ada.expect(http.StatusOK, "PATCH", "/api/labels/"+feature.Label.Id.String(), api.UpdateLabelRequest{Name: &newName}, nil)
	var ack api.SyncAck
	ada.expect(http.StatusOK, "DELETE", "/api/labels/"+feature.Label.Id.String(), nil, &ack)
	if got := models(env.fetch(t, ws.Id, adaMe.User, api.WorkspaceRoleAdmin, before)); len(got) != 2 || got[0] != "U:Label" || got[1] != "D:Label" {
		t.Errorf("label log = %v, want an update then a delete", got)
	}
	after := ada.bootstrap(ws.Id)
	if len(after.Labels) != 1 || len(after.Issues) != 1 || len(after.Issues[0].LabelIds) != 0 || after.LastSyncId != ack.SyncId {
		t.Errorf("after deleting the label: %d labels, issue labels %v", len(after.Labels), after.Issues[0].LabelIds)
	}
	ada.expect(http.StatusNotFound, "DELETE", "/api/labels/"+feature.Label.Id.String(), nil, nil)

	// Guests see labels but can't manage them; outsiders see nothing.
	guest := api.WorkspaceRoleGuest
	ada.expect(http.StatusCreated, "POST", base+"/invites", api.CreateInviteRequest{Email: "gus@example.com", Role: &guest}, nil)
	gus, _ := env.signIn(t, "gus@example.com")
	if boot := gus.bootstrap(ws.Id); len(boot.Labels) != 1 {
		t.Errorf("guest sees %d labels, want 1", len(boot.Labels))
	}
	gus.expect(http.StatusForbidden, "POST", base+"/labels", api.CreateLabelRequest{Id: newID(), Name: "Mine", Color: "#000000"}, nil)
	gus.expect(http.StatusForbidden, "DELETE", "/api/labels/"+bug.Label.Id.String(), nil, nil)
	eve, _ := env.signIn(t, "eve@example.com")
	eve.expect(http.StatusNotFound, "POST", base+"/labels", api.CreateLabelRequest{Id: newID(), Name: "Mine", Color: "#000000"}, nil)
	eve.expect(http.StatusNotFound, "PATCH", "/api/labels/"+bug.Label.Id.String(), api.UpdateLabelRequest{Name: &newName}, nil)
}

func TestRelabelIssue(t *testing.T) {
	a, b, c := newID(), newID(), newID()
	got := relabelIssue([]api.TeamId{a, b}, []api.TeamId{c, a}, []api.TeamId{b})
	if len(got) != 2 || got[0] != a || got[1] != c {
		t.Errorf("relabelIssue = %v, want [a c]", got)
	}
	if got := relabelIssue(nil, nil, []api.TeamId{a}); got == nil || len(got) != 0 {
		t.Errorf("relabelIssue with nothing left = %#v, want an empty non-nil slice", got)
	}
	// Removing wins over adding the same label in one request.
	if got := relabelIssue(nil, []api.TeamId{a}, []api.TeamId{a}); len(got) != 0 {
		t.Errorf("add+remove of the same label = %v, want none", got)
	}
}
