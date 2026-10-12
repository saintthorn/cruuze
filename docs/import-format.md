# The import format

Cruuze imports from GitHub, Linear, Jira and CSV files directly (Settings →
Import). For anything else — another tracker, a spreadsheet with its own
logic, a database — write a JSON file in this format and import it as a
"JSON file", or post it to the API:

```sh
curl -X POST https://your-host/api/workspaces/<workspace id>/imports \
  -H "Authorization: Bearer cruuze_..." -H "Content-Type: application/json" \
  -d '{"source": "bundle", "bundle": '"$(cat export.json)"'}'
```

The key must belong to a workspace admin. The answer is the import, which
runs in the background; `GET /api/imports/<id>` says how it is going.

## What an import does

- **Issues keep their numbers** when their team is new or empty, so `ENG-12`
  stays `ENG-12`. In a team that already has issues they get new numbers.
- **Dates and history are kept**: created, updated, started and completed
  times, and every status an issue has been in. Insights and forecasts work
  from the first day.
- **Importing again updates.** Everything is remembered by its `id` within
  the file's `origin`. Run the same origin again and issues that changed at
  the source are updated, new ones are added, and nothing is duplicated. An
  issue changed in both places takes the source's version.
- **People are matched by email** to workspace members. Anyone unmatched is
  left out of the assignee and author fields and credited by name in the text.
- **It is quiet**: no notification emails, no webhooks. Open browsers reload
  the workspace when it finishes.
- **It can be undone** from Settings → Import, which deletes what the import
  created and leaves what it only matched or updated.

## The file

```json
{
  "origin": "trello:roadmap",
  "label": "Roadmap board",
  "users": [{ "id": "u1", "email": "ada@example.com", "name": "Ada Lovelace" }],
  "teams": [
    {
      "id": "t1",
      "key": "ROAD",
      "name": "Roadmap",
      "states": [
        { "id": "s-todo", "name": "To do", "kind": "unstarted" },
        { "id": "s-doing", "name": "Doing", "kind": "started" },
        { "id": "s-done", "name": "Done", "kind": "completed" }
      ]
    }
  ],
  "labels": [{ "id": "l1", "name": "Bug", "color": "#eb5757" }],
  "projects": [
    {
      "id": "p1",
      "name": "Launch",
      "status": "started",
      "targetDate": "2027-01-31",
      "milestones": [{ "id": "m1", "name": "Beta", "targetDate": "2026-12-01" }]
    }
  ],
  "cycles": [{ "id": "c7", "team": "t1", "number": 7, "startsAt": "2026-10-05", "endsAt": "2026-10-18" }],
  "issues": [
    {
      "id": "card-41",
      "team": "t1",
      "number": 41,
      "title": "Fix login",
      "description": "Markdown.",
      "state": "s-done",
      "priority": 2,
      "assignee": "u1",
      "creator": "u1",
      "labels": ["l1"],
      "dueDate": "2026-10-20",
      "project": "p1",
      "milestone": "m1",
      "cycle": "c7",
      "parent": "card-12",
      "createdAt": "2026-09-01T10:00:00Z",
      "updatedAt": "2026-09-10T10:00:00Z",
      "history": [
        { "state": "s-todo", "at": "2026-09-01T10:00:00Z" },
        { "state": "s-doing", "at": "2026-09-02T10:00:00Z" },
        { "state": "s-done", "at": "2026-09-10T10:00:00Z" }
      ],
      "comments": [{ "id": "cm1", "author": "u1", "body": "Looking.", "createdAt": "2026-09-02T11:00:00Z" }],
      "relations": [{ "type": "blocks", "issue": "card-42" }],
      "url": "https://trello.com/c/abc"
    }
  ]
}
```

Only `origin`, one team, and each issue's `id`, `team` and `title` are
required. Things refer to each other by `id`; ids only need to be unique
within their kind and the file's origin. Dates are `YYYY-MM-DD`, times are
RFC 3339.

| Field | Notes |
|---|---|
| `origin` | A short name for where the data came from. It scopes the ids, and importing the same origin again updates the earlier import. |
| `teams[].key` | Up to 7 letters and digits, starting with a letter. A team with that key is reused if it exists; otherwise it is created. |
| `teams[].states[].kind` | One of `triage`, `backlog`, `unstarted`, `started`, `completed`, `canceled`. A status with the same name in the team is reused. Leave `states` out to use the team's own. A new team is given a status for any of unstarted, started, completed and canceled it lacks. |
| `issues[].number` | Kept in a new or empty team. Leave out to number in order. |
| `issues[].state` | A state id of the issue's team. Without it the issue is completed if it has a `completedAt` (or a closed history), else open. |
| `issues[].priority` | `0` none, `1` urgent, `2` high, `3` medium, `4` low. |
| `issues[].labels` | Label ids, or plain names (created as needed). |
| `issues[].history` | Every status the issue has been in, oldest first, beginning with the one it was created in. Without it, the history is pieced together from `createdAt`, `startedAt` and `completedAt`. |
| `issues[].startedAt`, `completedAt` | Taken from `history` when there is one. |
| `issues[].archived` | `true` sends it straight to the archive. Closed issues older than the team's archive setting go there too. |
| `issues[].relations[].type` | `blocks` (this blocks the other), `blocked_by`, `duplicate` (this is a duplicate of the other), `related`. |
| `issues[].url` | Linked from the description as "Imported from …". |
| `comments` (top level) | `[{ "issue": "<issue id>", "id": …, "author": …, "body": …, "createdAt": … }]`, for comments listed apart from their issues. |

A file may be up to 64 MB. Larger exports can be split by team: each file
uses the same `origin`, and parents and relations may point at issues in a
file imported earlier.

## From the tracker itself

| Source | What comes across |
|---|---|
| GitHub Issues | A repository becomes a team. Issues, comments, labels, assignees. Milestones become projects. "Closed as not planned" is canceled. Pull requests are left out. People are matched by the GitHub username they gave under Settings → Account. |
| Linear | Teams with their statuses, people (by email), labels, projects with milestones, cycles, sub-issues, relations, comments, status history, and files pasted into text. |
| Jira Cloud, Server, Data Center | A project becomes a team with the project's statuses. Sprints become cycles; an epic is the parent of its issues; issue type and components become labels. Descriptions and comments are converted to Markdown. Attachments are copied. Jira Cloud hides email addresses, so people are matched by hand in the wizard. |
| CSV | A column of titles is enough. Keys (`ENG-12`), statuses, priorities, people, labels, dates, parents, projects and teams are read when present, under the headings Linear, Jira and GitHub use. |

Credentials entered in the wizard are used for that import and are not
stored. An import that is interrupted (a restart, a rate limit that didn't
lift) can be run again and continues from where it stopped.

A Jira Server or GitHub Enterprise on a private network can only be read if
whoever runs Cruuze allows it with `IMPORT_ALLOW_PRIVATE=true`: otherwise
importers, like webhooks, refuse to connect to private addresses.
