package importer

import (
	"strings"

	"github.com/google/uuid"
)

// People says which member of the workspace a source user is. The wizard
// shows the same matches the import will make, so both ask here.
//
// A source user is matched, in order: by email; by GitHub username; as the
// person running the import, when the credentials used are their own; and
// by name, when exactly one member has that name. Trackers often keep email
// addresses to themselves (Jira Cloud does), which is what the last two are
// for.
type People struct {
	actor  uuid.UUID
	emails map[string]uuid.UUID
	github map[string]uuid.UUID
	// names maps a normalized name to the one member who has it, or to
	// uuid.Nil when more than one does.
	names map[string]uuid.UUID
}

// NewPeople starts an empty register for an import run by actor.
func NewPeople(actor uuid.UUID) *People {
	return &People{actor: actor, emails: map[string]uuid.UUID{}, github: map[string]uuid.UUID{}, names: map[string]uuid.UUID{}}
}

// Add registers a member.
func (p *People) Add(id uuid.UUID, email, name, githubLogin string) {
	if email != "" {
		p.emails[strings.ToLower(email)] = id
	}
	if githubLogin != "" {
		p.github[strings.ToLower(githubLogin)] = id
	}
	if key := nameKey(name); key != "" {
		if other, taken := p.names[key]; taken && other != id {
			p.names[key] = uuid.Nil
		} else {
			p.names[key] = id
		}
	}
}

// Match returns the member a source user is, or nil for nobody.
func (p *People) Match(u User) *uuid.UUID {
	if id, ok := p.emails[strings.ToLower(u.Email)]; ok && u.Email != "" {
		return &id
	}
	if id, ok := p.github[strings.ToLower(u.GitHubLogin)]; ok && u.GitHubLogin != "" {
		return &id
	}
	if u.Self && p.actor != uuid.Nil {
		actor := p.actor
		return &actor
	}
	if id, ok := p.names[nameKey(u.Name)]; ok && id != uuid.Nil {
		return &id
	}
	return nil
}

// nameKey is a name without case or spacing differences.
func nameKey(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}
