package importer

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"cruuze/server/internal/blob"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

// Undo removes what an import created: its issues with their comments and
// files, and the teams, statuses, labels, projects, milestones and cycles it
// made, where nothing else has come to use them since. What the import only
// matched (a label that already existed) or updated (an issue from an
// earlier import) stays as it is.
func Undo(ctx context.Context, pool *pgxpool.Pool, blobs blob.Store, imp dbgen.Import) (dbgen.Import, error) {
	var storageKeys []string
	err := db.InTx(ctx, pool, func(q *dbgen.Queries) error {
		items, err := q.ListItemsCreatedByImport(ctx, &imp.ID)
		if err != nil {
			return err
		}
		created := map[string][]uuid.UUID{}
		all := make([]uuid.UUID, 0, len(items))
		for _, item := range items {
			created[item.Kind] = append(created[item.Kind], item.RecordID)
			all = append(all, item.RecordID)
		}
		issues := created[kindIssue]

		// Their files leave the blob store once the rows are gone for good.
		attachments, err := q.ListAttachmentsOfIssues(ctx, issues)
		if err != nil {
			return err
		}
		for _, a := range attachments {
			storageKeys = append(storageKeys, a.StorageKey)
			all = append(all, a.ID)
		}
		// Comments other imports added to these issues go with them, so
		// their mappings must too.
		comments, err := q.ListCommentIDsOfIssues(ctx, issues)
		if err != nil {
			return err
		}
		all = append(all, comments...)
		if err := q.DeleteImportItemsForRecords(ctx, dbgen.DeleteImportItemsForRecordsParams{WorkspaceID: imp.WorkspaceID, RecordIds: all}); err != nil {
			return err
		}

		ws := imp.WorkspaceID
		// Issues first: the rest is only removed where no issue uses it.
		steps := []func() error{
			func() error {
				return q.DeleteCommentsByIDs(ctx, dbgen.DeleteCommentsByIDsParams{WorkspaceID: ws, Ids: created[kindComment]})
			},
			func() error {
				return q.DeleteIssuesByIDs(ctx, dbgen.DeleteIssuesByIDsParams{WorkspaceID: ws, Ids: issues})
			},
			func() error {
				return q.DeleteCyclesByIDs(ctx, dbgen.DeleteCyclesByIDsParams{WorkspaceID: ws, Ids: created[kindCycle]})
			},
			func() error {
				return q.DeleteMilestonesByIDs(ctx, dbgen.DeleteMilestonesByIDsParams{WorkspaceID: ws, Ids: created[kindMilestone]})
			},
			func() error {
				return q.DeleteProjectsByIDs(ctx, dbgen.DeleteProjectsByIDsParams{WorkspaceID: ws, Ids: created[kindProject]})
			},
			func() error {
				return q.DeleteUnusedLabelsByIDs(ctx, dbgen.DeleteUnusedLabelsByIDsParams{WorkspaceID: ws, Ids: created[kindLabel]})
			},
			func() error {
				return q.DeleteUnusedStatesByIDs(ctx, dbgen.DeleteUnusedStatesByIDsParams{WorkspaceID: ws, Ids: created[kindState]})
			},
			func() error {
				return q.DeleteEmptyTeamsByIDs(ctx, dbgen.DeleteEmptyTeamsByIDsParams{WorkspaceID: ws, Ids: created[kindTeam]})
			},
		}
		for _, step := range steps {
			if err := step(); err != nil {
				return err
			}
		}
		if imp, err = q.MarkImportUndone(ctx, imp.ID); err != nil {
			return err
		}
		return syncer.ResetAll(ctx, q, ws)
	})
	if err != nil {
		return imp, err
	}
	for _, key := range storageKeys {
		_ = blobs.Delete(ctx, key) // an orphaned file is harmless; the rows are gone
	}
	return imp, nil
}
