package importer

import (
	"context"
	"errors"
	"strings"
)

// issuesPerBundle is how many issues a source hands the loader at a time:
// each bundle is one transaction.
const issuesPerBundle = 200

// fileSource is an uploaded file in the import format.
type fileSource struct {
	file  File
	teams []string // source team IDs to import; empty means all
}

// FromFile makes a source of an uploaded import file.
func FromFile(file File, teams []string) (Source, error) {
	file.Origin = strings.TrimSpace(file.Origin)
	if file.Origin == "" || len(file.Origin) > 100 {
		return nil, userError(`The file needs an "origin": a short name for where the data came from, such as "trello:roadmap".`)
	}
	if len(file.Teams) == 0 {
		return nil, userError(`The file has no "teams". Every issue belongs to one.`)
	}
	return &fileSource{file: file, teams: teams}, nil
}

func (s *fileSource) wanted(teamID string) bool {
	return len(s.teams) == 0 || containsString(s.teams, teamID)
}

func containsString(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

func (s *fileSource) Preview(context.Context) (Preview, error) {
	label := s.file.Label
	if label == "" {
		label = s.file.Origin
	}
	p := Preview{Origin: s.file.Origin, Label: label, Teams: []PreviewTeam{}, Users: append([]User{}, s.file.Users...)}
	count := map[string]int{}
	for _, issue := range s.file.Issues {
		count[issue.Team]++
	}
	for _, team := range s.file.Teams {
		p.Teams = append(p.Teams, PreviewTeam{ID: team.ID, Key: team.Key, Name: team.Name, Issues: count[team.ID]})
	}
	return p, nil
}

func (s *fileSource) Fetch(ctx context.Context, emit func(Bundle) error, progress func(string)) error {
	head := Bundle{Users: s.file.Users, Labels: s.file.Labels, Projects: s.file.Projects}
	for _, team := range s.file.Teams {
		if s.wanted(team.ID) {
			head.Teams = append(head.Teams, team)
		}
	}
	for _, cycle := range s.file.Cycles {
		if s.wanted(cycle.Team) {
			head.Cycles = append(head.Cycles, cycle)
		}
	}
	progress("Reading the file")
	if err := emit(head); err != nil {
		return err
	}
	var batch []Issue
	for _, issue := range s.file.Issues {
		if !s.wanted(issue.Team) {
			continue
		}
		batch = append(batch, issue)
		if len(batch) == issuesPerBundle {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := emit(Bundle{Issues: batch}); err != nil {
				return err
			}
			batch = nil
		}
	}
	if len(batch) > 0 {
		return emit(Bundle{Issues: batch})
	}
	return nil
}

func (s *fileSource) Open(context.Context, Attachment) (ReadCloser, string, error) {
	return nil, "", errors.New("files in an uploaded import are not fetched")
}

// uploadsInText finds addresses of uploaded files in Markdown, for sources
// that keep files only as links in the text. host is the address prefix
// such files have.
func uploadsInText(prefix string, texts ...string) []Attachment {
	var found []Attachment
	seen := map[string]bool{}
	for _, text := range texts {
		for rest := text; ; {
			i := strings.Index(rest, prefix)
			if i < 0 {
				break
			}
			end := i
			for end < len(rest) && !strings.ContainsRune(" \n\t)\"'>]", rune(rest[end])) {
				end++
			}
			address := rest[i:end]
			rest = rest[end:]
			if seen[address] {
				continue
			}
			seen[address] = true
			name := address[strings.LastIndex(address, "/")+1:]
			if q := strings.IndexByte(name, '?'); q >= 0 {
				name = name[:q]
			}
			found = append(found, Attachment{ID: address, Filename: name, URL: address})
		}
	}
	return found
}
