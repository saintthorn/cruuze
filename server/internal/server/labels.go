package server

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

const maxLabelNameLen = 50

var colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

var errLabelNameTaken = &apiError{http.StatusConflict, "label_name_taken", "A label with that name already exists."}

func (s *Server) CreateLabel(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	if !s.requireEditor(w, r, workspaceId) {
		return
	}
	var req api.CreateLabelRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Id == uuid.Nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Label ID is required.")
		return
	}
	name, apiErr := cleanLabelName(req.Name)
	if apiErr == nil {
		apiErr = validateColor(req.Color)
	}
	if apiErr != nil {
		s.fail(w, r, apiErr)
		return
	}
	ctx := r.Context()

	// existing answers a retry of a create that already went through.
	existing := func(q *dbgen.Queries) (*api.LabelMutationResult, error) {
		label, err := q.GetLabel(ctx, req.Id)
		if db.IsNoRows(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if label.WorkspaceID != workspaceId {
			return nil, &apiError{http.StatusConflict, "id_taken", "That label ID is already in use."}
		}
		syncID, err := q.GetLastSyncID(ctx, workspaceId)
		if err != nil {
			return nil, err
		}
		return &api.LabelMutationResult{Label: labelRecord(label), SyncId: syncID}, nil
	}

	if found, err := existing(s.q); err != nil {
		s.fail(w, r, err)
		return
	} else if found != nil {
		writeJSON(w, http.StatusOK, found)
		return
	}

	var result api.LabelMutationResult
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		label, err := q.CreateLabel(ctx, dbgen.CreateLabelParams{
			ID:          req.Id,
			WorkspaceID: workspaceId,
			Name:        name,
			Color:       strings.ToLower(req.Color),
		})
		if err != nil {
			return err
		}
		result.Label = labelRecord(label)
		result.SyncId, err = recordLabel(ctx, q, label, syncer.Insert)
		return err
	})
	if db.IsUniqueViolation(err) {
		// Either a concurrent retry created this label, or the name is taken.
		if found, lookupErr := existing(s.q); lookupErr == nil && found != nil {
			writeJSON(w, http.StatusOK, found)
			return
		}
		err = errLabelNameTaken
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) UpdateLabel(w http.ResponseWriter, r *http.Request, labelId uuid.UUID) {
	label, ok := s.requireLabel(w, r, labelId)
	if !ok {
		return
	}
	var req api.UpdateLabelRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	params := dbgen.UpdateLabelParams{ID: labelId}
	if req.Name != nil {
		name, apiErr := cleanLabelName(*req.Name)
		if apiErr != nil {
			s.fail(w, r, apiErr)
			return
		}
		params.Name = &name
	}
	if req.Color != nil {
		if apiErr := validateColor(*req.Color); apiErr != nil {
			s.fail(w, r, apiErr)
			return
		}
		color := strings.ToLower(*req.Color)
		params.Color = &color
	}
	ctx := r.Context()

	result := api.LabelMutationResult{Label: labelRecord(label)}
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if params.Name == nil && params.Color == nil {
			var err error
			result.SyncId, err = q.GetLastSyncID(ctx, label.WorkspaceID)
			return err
		}
		updated, err := q.UpdateLabel(ctx, params)
		if err != nil {
			return err
		}
		result.Label = labelRecord(updated)
		result.SyncId, err = recordLabel(ctx, q, updated, syncer.Update)
		return err
	})
	if db.IsUniqueViolation(err) {
		err = errLabelNameTaken
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) DeleteLabel(w http.ResponseWriter, r *http.Request, labelId uuid.UUID) {
	label, ok := s.requireLabel(w, r, labelId)
	if !ok {
		return
	}
	ctx := r.Context()

	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		// Issues aren't logged individually: clients drop the label from
		// their issues when they see it deleted.
		err := q.RemoveLabelFromIssues(ctx, dbgen.RemoveLabelFromIssuesParams{LabelID: labelId, WorkspaceID: label.WorkspaceID})
		if err != nil {
			return err
		}
		if err := q.DeleteLabel(ctx, labelId); err != nil {
			return err
		}
		ack.SyncId, err = recordLabel(ctx, q, label, syncer.Delete)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ack)
}

// requireLabel loads a label the caller may manage.
func (s *Server) requireLabel(w http.ResponseWriter, r *http.Request, labelID uuid.UUID) (dbgen.Label, bool) {
	label, err := s.q.GetLabel(r.Context(), labelID)
	if db.IsNoRows(err) {
		notFound(w)
		return label, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return label, false
	}
	return label, s.requireEditor(w, r, label.WorkspaceID)
}

func recordLabel(ctx context.Context, q *dbgen.Queries, label dbgen.Label, action string) (int64, error) {
	return syncer.Record(ctx, q, syncer.Change{
		WorkspaceID: label.WorkspaceID,
		Model:       api.SyncModelLabel,
		ModelID:     label.ID,
		Action:      action,
		Data:        labelRecord(label),
	})
}

func cleanLabelName(raw string) (string, *apiError) {
	name := strings.TrimSpace(raw)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxLabelNameLen {
		return "", badRequest("invalid_name", "Label name must be 1-50 characters.")
	}
	return name, nil
}

func validateColor(color string) *apiError {
	if !colorPattern.MatchString(color) {
		return badRequest("invalid_color", "Color must be a hex value like #5e6ad2.")
	}
	return nil
}
