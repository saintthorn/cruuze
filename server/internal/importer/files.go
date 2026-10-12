package importer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"

	"github.com/google/uuid"

	"cruuze/server/internal/blob"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
)

// maxFileBytes is the app's own limit on an attachment.
const maxFileBytes = 25 << 20

// pendingFiles are an issue's attachments, waiting to be copied once the
// issue itself is safely written.
type pendingFiles struct {
	issue       uuid.UUID
	attachments []Attachment
}

// CopyFiles makes the loader bring attachments across: it reads each from
// the source and keeps it in store. Without this, attachments are left as
// links to the source.
func (l *Loader) CopyFiles(store blob.Store, source Source) {
	l.store, l.source = store, source
}

// fileWorkers is how many issues have their files copied at once. Each file
// is held in memory on its way through, so this also bounds that.
const fileWorkers = 4

// fileCopier copies attachments in the background while the import reads
// on: downloads are slow, and nothing else waits on them.
type fileCopier struct {
	queue chan pendingFiles
	done  sync.WaitGroup
	// mu guards err, closed, and the loader's record of copied attachments.
	mu     sync.Mutex
	err    error
	closed bool
}

// copyFiles hands the attachments of the issues just loaded to the copiers.
// It is called once the bundle's transaction is over: a file that can't be
// fetched is no reason to lose an issue. It waits when the copiers are
// behind, and reports what stopped them, if anything did.
func (l *Loader) copyFiles(ctx context.Context) error {
	pending := l.files
	l.files = nil
	if l.store == nil || l.source == nil || len(pending) == 0 {
		return nil
	}
	c := &l.copier
	if c.queue == nil {
		c.queue = make(chan pendingFiles, 4*fileWorkers)
		for range fileWorkers {
			c.done.Add(1)
			go func() {
				defer c.done.Done()
				for p := range c.queue {
					if err := l.copyIssueFiles(ctx, p); err != nil {
						c.mu.Lock()
						if c.err == nil {
							c.err = err
						}
						c.mu.Unlock()
					}
				}
			}()
		}
	}
	for _, p := range pending {
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		if err != nil {
			return err
		}
		select {
		case c.queue <- p:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// WaitFiles waits for the attachments still being copied and reports what
// stopped the copying, if anything did. Nothing can be loaded after it.
func (l *Loader) WaitFiles() error {
	c := &l.copier
	c.mu.Lock()
	if c.queue != nil && !c.closed {
		c.closed = true
		close(c.queue)
	}
	c.mu.Unlock()
	c.done.Wait()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// copyIssueFiles copies one issue's attachments and points its text at the
// copies.
func (l *Loader) copyIssueFiles(ctx context.Context, p pendingFiles) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	copies := make([]fileCopy, 0, len(p.attachments))
	for _, a := range p.attachments {
		made, err := l.copyFile(ctx, p.issue, a)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			l.warn("Some files couldn't be copied and were left as links to the source.")
			continue
		}
		if made != nil {
			copies = append(copies, *made)
		}
	}
	return l.pointAtCopies(ctx, p.issue, copies)
}

// fileCopy is an attachment and the copy made of it.
type fileCopy struct {
	source Attachment
	id     uuid.UUID
	image  bool
}

// imageTypes are the types the app shows in the page rather than offering
// as a download.
var imageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

func (l *Loader) copyFile(ctx context.Context, issueID uuid.UUID, a Attachment) (*fileCopy, error) {
	if a.ID == "" || a.URL == "" {
		return nil, nil
	}
	q := dbgen.New(l.pool)
	l.copier.mu.Lock()
	id, ok := l.mapped[kindAttachment][a.ID]
	l.copier.mu.Unlock()
	if ok {
		// Copied by an earlier import.
		existing, err := q.GetAttachment(ctx, id)
		if err != nil {
			return nil, err
		}
		return &fileCopy{source: a, id: id, image: containsString(imageTypes, existing.ContentType)}, nil
	}
	body, _, err := l.source.Open(ctx, a)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(body, maxFileBytes+1))
	body.Close()
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	if len(data) > maxFileBytes {
		l.warn("Some files are larger than 25 MB and were left as links to the source.")
		return nil, nil
	}
	// The type is decided from the bytes, as for an upload.
	contentType := http.DetectContentType(data)
	id = newID()
	key := l.workspaceID.String() + "/" + id.String()
	if err := l.store.Put(ctx, key, bytes.NewReader(data), int64(len(data)), contentType); err != nil {
		return nil, err
	}
	err = db.InTx(ctx, l.pool, func(q *dbgen.Queries) error {
		_, err := q.CreateAttachment(ctx, dbgen.CreateAttachmentParams{
			ID: id, WorkspaceID: l.workspaceID, IssueID: issueID, Filename: fileName(a),
			ContentType: contentType, Size: int64(len(data)), StorageKey: key,
		})
		if err != nil {
			return err
		}
		q.UpsertImportItems(ctx, []dbgen.UpsertImportItemsParams{l.item(kindAttachment, a.ID, id, true, nil)}).Exec(func(_ int, e error) { err = e })
		return err
	})
	if err != nil {
		_ = l.store.Delete(ctx, key)
		return nil, err
	}
	l.copier.mu.Lock()
	l.mapped[kindAttachment][a.ID] = id
	l.copier.mu.Unlock()
	l.inc("files")
	return &fileCopy{source: a, id: id, image: containsString(imageTypes, contentType)}, nil
}

// fileName is a safe name for an attachment: its own, or the end of its address.
func fileName(a Attachment) string {
	name := strings.TrimSpace(a.Filename)
	if name == "" {
		name = path.Base(strings.SplitN(a.URL, "?", 2)[0])
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	if name == "" || name == "." {
		name = "file"
	}
	return clip(name, 200)
}

var wikiFileImage = regexp.MustCompile(`!\[\]\(([^)\s]+)\)`)

// pointAtCopies rewrites an issue's description and comments so that what
// referred to a file at the source refers to the copy here.
func (l *Loader) pointAtCopies(ctx context.Context, issueID uuid.UUID, copies []fileCopy) error {
	if len(copies) == 0 {
		return nil
	}
	rewrite := func(text string) string {
		for _, c := range copies {
			address := "/api/attachments/" + c.id.String()
			name := fileName(c.source)
			link := fmt.Sprintf("[%s](%s)", name, address)
			if c.image {
				link = "!" + link
			}
			// A link or image by the file's address at the source.
			text = strings.ReplaceAll(text, c.source.URL, address)
			// What the Jira converters leave where a file was embedded.
			text = strings.ReplaceAll(text, "_(attached file: "+c.source.Filename+")_", link)
			text = wikiFileImage.ReplaceAllStringFunc(text, func(match string) string {
				if wikiFileImage.FindStringSubmatch(match)[1] == c.source.Filename {
					return link
				}
				return match
			})
		}
		return text
	}
	return db.InTx(ctx, l.pool, func(q *dbgen.Queries) error {
		issue, err := q.GetIssue(ctx, issueID)
		if db.IsNoRows(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if text := rewrite(issue.Description); text != issue.Description {
			if err := q.SetIssueDescriptionQuietly(ctx, dbgen.SetIssueDescriptionQuietlyParams{ID: issueID, Description: text}); err != nil {
				return err
			}
		}
		comments, err := q.ListCommentsForIssue(ctx, issueID)
		if err != nil {
			return err
		}
		for _, c := range comments {
			if text := rewrite(c.Body); text != c.Body {
				if err := q.UpdateImportedComment(ctx, dbgen.UpdateImportedCommentParams{ID: c.ID, Body: text}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
