package server

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/blob"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

const (
	maxAttachmentBytes = 25 << 20
	maxFilenameLen     = 200
)

// Only these types are shown in the browser; everything else downloads, so an
// uploaded HTML or SVG file can never run script on our origin.
var inlineContentTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

func (s *Server) UploadAttachment(w http.ResponseWriter, r *http.Request, issueId api.IssueId) {
	team, ok := s.requireIssueAccess(w, r, issueId)
	if !ok {
		return
	}
	// A little headroom for the multipart framing around the file.
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentBytes+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "Files can be up to 25 MB.")
		} else {
			writeError(w, http.StatusBadRequest, "bad_request", "Expected a file upload.")
		}
		return
	}
	defer r.MultipartForm.RemoveAll() //nolint:errcheck // temp files

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Expected a file upload.")
		return
	}
	defer file.Close()
	if header.Size == 0 {
		writeError(w, http.StatusBadRequest, "empty_file", "That file is empty.")
		return
	}
	if header.Size > maxAttachmentBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "Files can be up to 25 MB.")
		return
	}

	// Decide the type from the bytes, not from what the client claims.
	head := make([]byte, 512)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		s.internalError(w, r, err)
		return
	}
	contentType := http.DetectContentType(head[:n])
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		s.internalError(w, r, err)
		return
	}

	ctx := r.Context()
	id := newID()
	key := team.WorkspaceID.String() + "/" + id.String()
	if err := s.blobs.Put(ctx, key, file, header.Size, contentType); err != nil {
		s.internalError(w, r, err)
		return
	}

	uploaderID := currentSession(r).User.ID
	var result api.AttachmentMutationResult
	err = db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		attachment, err := q.CreateAttachment(ctx, dbgen.CreateAttachmentParams{
			ID:          id,
			WorkspaceID: team.WorkspaceID,
			IssueID:     issueId,
			UploaderID:  &uploaderID,
			Filename:    cleanFilename(header.Filename),
			ContentType: contentType,
			Size:        header.Size,
			StorageKey:  key,
		})
		if err != nil {
			return err
		}
		result.Attachment = attachmentRecord(attachment)
		result.SyncId, err = syncer.Record(ctx, q, syncer.Change{
			WorkspaceID: team.WorkspaceID,
			Model:       api.SyncModelAttachment,
			ModelID:     id,
			Action:      syncer.Insert,
			Data:        result.Attachment,
			Team:        &team,
		})
		return err
	})
	if err != nil {
		// Don't leave bytes behind that nothing points to.
		if delErr := s.blobs.Delete(ctx, key); delErr != nil {
			s.log.Warn("delete orphaned attachment", "key", key, "err", delErr)
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) DownloadAttachment(w http.ResponseWriter, r *http.Request, attachmentId uuid.UUID) {
	attachment, _, ok := s.requireAttachment(w, r, attachmentId)
	if !ok {
		return
	}
	body, err := s.blobs.Get(r.Context(), attachment.StorageKey)
	if errors.Is(err, blob.ErrNotFound) {
		notFound(w)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	defer body.Close()

	mediaType, _, _ := mime.ParseMediaType(attachment.ContentType)
	disposition := "attachment"
	if slices.Contains(inlineContentTypes, mediaType) {
		disposition = "inline"
	}
	h := w.Header()
	h.Set("Content-Type", attachment.ContentType)
	h.Set("Content-Length", strconv.FormatInt(attachment.Size, 10))
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": attachment.Filename}))
	// Belt and braces against an upload being treated as a page.
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, max-age=3600")
	if _, err := io.Copy(w, body); err != nil {
		s.log.Warn("send attachment", "err", err)
	}
}

func (s *Server) DeleteAttachment(w http.ResponseWriter, r *http.Request, attachmentId uuid.UUID) {
	attachment, access, ok := s.requireAttachment(w, r, attachmentId)
	if !ok {
		return
	}
	isUploader := attachment.UploaderID != nil && *attachment.UploaderID == currentSession(r).User.ID
	if !isUploader && access.role != roleAdmin {
		forbidden(w)
		return
	}
	ctx := r.Context()

	var ack api.SyncAck
	err := db.InTx(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := q.DeleteAttachment(ctx, attachment.ID); err != nil {
			return err
		}
		var err error
		ack.SyncId, err = syncer.Record(ctx, q, syncer.Change{
			WorkspaceID: attachment.WorkspaceID,
			Model:       api.SyncModelAttachment,
			ModelID:     attachment.ID,
			Action:      syncer.Delete,
			Data:        attachmentRecord(attachment),
			Team:        &access.team,
		})
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// The record is gone; a leftover blob is only wasted space.
	if err := s.blobs.Delete(ctx, attachment.StorageKey); err != nil {
		s.log.Warn("delete attachment blob", "key", attachment.StorageKey, "err", err)
	}
	writeJSON(w, http.StatusOK, ack)
}

// requireAttachment loads an attachment on an issue the caller is allowed to see.
func (s *Server) requireAttachment(w http.ResponseWriter, r *http.Request, id uuid.UUID) (dbgen.Attachment, teamAccess, bool) {
	attachment, err := s.q.GetAttachment(r.Context(), id)
	if db.IsNoRows(err) {
		notFound(w)
		return attachment, teamAccess{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return attachment, teamAccess{}, false
	}
	issue, err := s.q.GetIssue(r.Context(), attachment.IssueID)
	if err != nil {
		s.internalError(w, r, err)
		return attachment, teamAccess{}, false
	}
	access, ok := s.requireTeamAccess(w, r, issue.TeamID)
	return attachment, access, ok
}

// cleanFilename reduces an uploaded file's name to something safe to store
// and to send back in a header.
func cleanFilename(raw string) string {
	// Browsers send just the name, but nothing stops a client sending a path.
	name := path.Base(strings.ReplaceAll(raw, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if runes := []rune(name); len(runes) > maxFilenameLen {
		name = string(runes[len(runes)-maxFilenameLen:]) // keep the extension
	}
	if name == "" || name == "." || name == ".." || name == "/" {
		return "file"
	}
	return name
}
