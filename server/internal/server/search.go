package server

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db/dbgen"
)

// maxSearchWords caps how many words of a search are used.
const maxSearchWords = 12

func (s *Server) SearchIssues(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId, params api.SearchIssuesParams) {
	role, ok := s.requireWorkspaceRole(w, r, workspaceId)
	if !ok {
		return
	}
	out := api.SearchResults{Results: []api.SearchResult{}}
	query := searchQuery(params.Q)
	if query == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	ctx := r.Context()

	// The same teams the caller's bootstrap covers.
	teams, err := s.q.ListTeamsForUser(ctx, dbgen.ListTeamsForUserParams{
		WorkspaceID: workspaceId,
		UserID:      currentSession(r).User.ID,
		MembersOnly: role == roleGuest,
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	teamIDs := make([]uuid.UUID, 0, len(teams))
	for _, t := range teams {
		teamIDs = append(teamIDs, t.Team.ID)
	}
	rows, err := s.q.SearchIssues(ctx, dbgen.SearchIssuesParams{Query: query, TeamIds: teamIDs})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, row := range rows {
		out.Results = append(out.Results, api.SearchResult{
			IssueId:    row.IssueID,
			Identifier: fmt.Sprintf("%s-%d", row.TeamKey, row.Number),
			Title:      row.Title,
			StateId:    row.StateID,
			Archived:   row.Archived,
			Snippet:    row.Snippet,
			InComment:  row.InComment,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

var searchWord = regexp.MustCompile(`[\p{L}\p{N}]+`)

// searchQuery turns what someone typed into a tsquery that requires every
// word, each matching as a prefix so results appear while typing. Only
// letters and digits survive, so the input can't inject tsquery syntax.
func searchQuery(raw string) string {
	words := searchWord.FindAllString(raw, maxSearchWords)
	for i, word := range words {
		words[i] = word + ":*"
	}
	return strings.Join(words, " & ")
}
