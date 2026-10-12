package importer

import (
	"testing"

	"github.com/google/uuid"
)

func TestPeopleMatch(t *testing.T) {
	ada, bob, bob2, grace := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	people := NewPeople(ada)
	people.Add(ada, "ada@example.com", "Ada Lovelace", "ada-gh")
	people.Add(bob, "bob@example.com", "Bob Builder", "")
	people.Add(bob2, "bob2@example.com", "bob  builder", "")
	people.Add(grace, "grace@example.com", "Grace Hopper", "")

	cases := []struct {
		name string
		user User
		want *uuid.UUID
	}{
		{"by email, whatever the name", User{ID: "1", Email: "GRACE@example.com", Name: "G. Hopper"}, &grace},
		{"by GitHub username", User{ID: "2", GitHubLogin: "Ada-GH"}, &ada},
		// Jira Cloud hides email addresses: the token's owner is still known.
		{"the person running the import", User{ID: "3", Name: "Spencer", Self: true}, &ada},
		{"by a name one member has", User{ID: "4", Name: "grace  HOPPER"}, &grace},
		{"not by a name two members share", User{ID: "5", Name: "Bob Builder"}, nil},
		{"a stranger", User{ID: "6", Name: "Carol Shaw"}, nil},
		{"nobody without a name", User{ID: "7"}, nil},
	}
	for _, c := range cases {
		got := people.Match(c.user)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
