package importer

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCSVLinearExport(t *testing.T) {
	text := "\xEF\xBB\xBFID,Team,Title,Description,Status,Estimate,Priority,Project,Creator,Assignee,Labels,Created,Updated,Completed,Canceled,Parent issue,Due Date\n" +
		`ENG-12,Engineering,Fix login,"It fails, sometimes.",Done,3,Urgent,Launch,bob@example.com,ada@example.com,"Bug, Auth",2026-09-01T10:00:00.000Z,2026-09-10T10:00:00.000Z,2026-09-10T10:00:00.000Z,,,2026-09-30` + "\n" +
		`ENG-13,Engineering,Add SSO,,In Progress,,No priority,Launch,Bob,,,Wed Sep 02 2026 10:00:00 GMT+0000 (Coordinated Universal Time),,,,ENG-12,` + "\n" +
		`ENG-14,Engineering,Dropped idea,,Duplicate,,Low,,,,,2026-09-03T10:00:00.000Z,,,2026-09-04T10:00:00.000Z,,` + "\n" +
		`DES-2,Design,New logo,,Todo,,,,,,,2026-09-05T10:00:00.000Z,,,,,` + "\n" +
		`,,,,,,,,,,,,,,,,` + "\n"
	source, err := FromCSV(text, "Linear Export", "")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := source.Preview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if preview.Origin != "csv:linear export" || len(preview.Teams) != 2 || preview.Teams[0].Key != "ENG" || preview.Teams[0].Issues != 3 || preview.Teams[1].Name != "Design" {
		t.Fatalf("preview = %+v", preview)
	}
	all, _ := collect(t, source)

	var states []string
	for _, s := range all.Teams[0].States {
		states = append(states, s.Name+":"+s.Kind)
	}
	if got := strings.Join(states, ", "); got != "Done:completed, In Progress:started, Duplicate:canceled" {
		t.Errorf("ENG states = %s", got)
	}
	if len(all.Issues) != 4 {
		t.Fatalf("%d issues", len(all.Issues))
	}
	login, sso, dropped := all.Issues[0], all.Issues[1], all.Issues[2]
	if login.ID != "ENG/ENG-12" || login.Number != 12 || login.Team != "ENG" || login.Description != "It fails, sometimes." ||
		login.Priority != 1 || login.DueDate != "2026-09-30" ||
		login.Assignee != "ada@example.com" || login.Creator != "bob@example.com" || strings.Join(login.Labels, "|") != "Bug|Auth" ||
		login.Project != "project:launch" || login.CompletedAt == nil || login.CreatedAt.Format(time.DateOnly) != "2026-09-01" {
		t.Errorf("ENG-12 = %+v", login)
	}
	// A JavaScript date string is read too; a parent is named by its key.
	if sso.CreatedAt.Format(time.RFC3339) != "2026-09-02T10:00:00Z" || sso.Parent != "ENG/ENG-12" || sso.Creator != "bob" || sso.Priority != 0 || sso.State != "ENG:in progress" {
		t.Errorf("ENG-13 = %+v", sso)
	}
	// The "Canceled" column is a closing date like "Completed".
	if dropped.CompletedAt == nil || dropped.CompletedAt.Format(time.DateOnly) != "2026-09-04" || dropped.Priority != 4 {
		t.Errorf("ENG-14 = %+v", dropped)
	}
	// People with an address can be matched by it; a bare name can't.
	emails := map[string]string{}
	for _, u := range all.Users {
		emails[u.ID] = u.Email
	}
	if emails["ada@example.com"] != "ada@example.com" || emails["bob"] != "" || len(all.Users) != 3 {
		t.Errorf("users = %+v", all.Users)
	}
	if len(all.Projects) != 1 || len(all.Labels) != 2 {
		t.Errorf("projects %+v, labels %+v", all.Projects, all.Labels)
	}
}

func TestCSVJiraExportAndPlainSheets(t *testing.T) {
	// Jira repeats the Labels column and says which project a row is in.
	jira := "Summary,Issue key,Issue id,Issue Type,Status,Project key,Project name,Priority,Resolved,Created,Labels,Labels,Reporter\n" +
		"Tune the cache,PLAT-7,10007,Bug,Closed,PLAT,Platform,Major,12/Sep/26 10:30 AM,01/Sep/26 9:00 AM,perf,backend,zed\n" +
		"Write docs,PLAT-8,10008,Task,Open,PLAT,Platform,Minor,,02/Sep/26 9:00 AM,,,zed\n"
	source, err := FromCSV(jira, "jira", "")
	if err != nil {
		t.Fatal(err)
	}
	all, _ := collect(t, source)
	if len(all.Teams) != 1 || all.Teams[0].Key != "PLAT" || all.Teams[0].Name != "Platform" {
		t.Fatalf("teams = %+v", all.Teams)
	}
	cache := all.Issues[0]
	if cache.ID != "PLAT/PLAT-7" || cache.Number != 7 || cache.Priority != 2 || strings.Join(cache.Labels, ",") != "Bug,perf,backend" ||
		cache.CompletedAt == nil || cache.CompletedAt.Format("2006-01-02 15:04") != "2026-09-12 10:30" || cache.CreatedAt.Format(time.DateOnly) != "2026-09-01" {
		t.Errorf("PLAT-7 = %+v", cache)
	}
	// "Closed" is a finished status: the rows in it have a resolution date.
	if all.Teams[0].States[0].Kind != KindCompleted || all.Teams[0].States[1].Kind != KindUnstarted {
		t.Errorf("states = %+v", all.Teams[0].States)
	}

	// A plain sheet: titles only, into the team named.
	plain, err := FromCSV("Title\nFirst thing\nSecond thing\n", "", "Operations")
	if err != nil {
		t.Fatal(err)
	}
	all, _ = collect(t, plain)
	if len(all.Teams) != 1 || all.Teams[0].Key != "OPERATI" || len(all.Issues) != 2 || all.Issues[0].Number != 0 || !strings.HasPrefix(all.Issues[0].ID, "OPERATI/row:") || all.Issues[0].ID == all.Issues[1].ID {
		t.Errorf("plain sheet: teams %+v, issues %+v", all.Teams, all.Issues)
	}
	// The same rows give the same IDs, so a second import updates the first.
	againSource, _ := FromCSV("Title\nFirst thing\nSecond thing\n", "", "Operations")
	again, _ := collect(t, againSource)
	if again.Issues[0].ID != all.Issues[0].ID {
		t.Error("row IDs differ between two readings of the same file")
	}

	for name, bad := range map[string]string{"empty": "", "heading only": "Title\n", "no titles": "Status\nDone\n", "blank titles": "Title,Status\n,Done\n"} {
		if _, err := FromCSV(bad, "", ""); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
