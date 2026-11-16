package github

import (
	"testing"
	"time"
)

const rowsFixture = `{
  "search": {
    "nodes": [
      {
        "number": 1,
        "title": "Direct request",
        "url": "https://github.com/acme/app/pull/1",
        "isDraft": false,
        "updatedAt": "2026-11-10T10:00:00Z",
        "additions": 12,
        "deletions": 3,
        "author": { "login": "bob" },
        "reviewRequests": { "nodes": [
          { "requestedReviewer": { "__typename": "User", "login": "me" } },
          { "requestedReviewer": { "__typename": "User", "login": "carol" } }
        ] }
      },
      {
        "number": 2,
        "title": "Team request",
        "url": "https://github.com/acme/app/pull/2",
        "isDraft": false,
        "updatedAt": "2026-11-12T10:00:00Z",
        "additions": 40,
        "deletions": 0,
        "author": { "login": "dave" },
        "reviewRequests": { "nodes": [
          { "requestedReviewer": { "__typename": "Team", "combinedSlug": "acme/mobile", "name": "Mobile" } }
        ] }
      },
      {
        "number": 3,
        "title": "Other team request",
        "url": "https://github.com/acme/app/pull/3",
        "isDraft": false,
        "updatedAt": "2026-11-09T10:00:00Z",
        "additions": 7,
        "deletions": 7,
        "author": { "login": "erin" },
        "reviewRequests": { "nodes": [
          { "requestedReviewer": { "__typename": "Team", "combinedSlug": "acme/docs", "name": "Docs" } }
        ] }
      },
      {
        "number": 4,
        "title": "Draft",
        "url": "https://github.com/acme/app/pull/4",
        "isDraft": true,
        "updatedAt": "2026-11-11T10:00:00Z",
        "additions": 150,
        "deletions": 20,
        "author": null,
        "reviewRequests": { "nodes": [
          { "requestedReviewer": { "__typename": "User", "login": "me" } }
        ] }
      },
      {
        "number": 5,
        "title": "User and team request",
        "url": "https://github.com/acme/app/pull/5",
        "isDraft": false,
        "updatedAt": "2026-11-12T12:00:00Z",
        "additions": 1,
        "deletions": 2,
        "author": { "login": "bob" },
        "reviewRequests": { "nodes": [
          { "requestedReviewer": { "__typename": "User", "login": "me" } },
          { "requestedReviewer": { "__typename": "Team", "combinedSlug": "acme/mobile", "name": "Mobile" } },
          { "requestedReviewer": { "__typename": "Bot" } }
        ] }
      }
    ]
  },
  "repositoryOwner": {
    "teams": { "nodes": [ { "combinedSlug": "acme/mobile" }, { "combinedSlug": "acme/sdk" } ] }
  }
}`

func fixtureRows(t *testing.T) []Row {
	t.Helper()
	rows, err := ToRows([]byte(rowsFixture), "me")
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func fixtureRow(t *testing.T, number int) Row {
	t.Helper()
	for _, row := range fixtureRows(t) {
		if row.Number == number {
			return row
		}
	}
	t.Fatalf("no row %d", number)
	return Row{}
}

func equal[T comparable](t *testing.T, got, want []T) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestDirectRequestIsLabeledYou(t *testing.T) {
	equal(t, fixtureRow(t, 1).Reasons, []string{"you"})
}

func TestLoginIgnoresCase(t *testing.T) {
	rows, err := ToRows([]byte(rowsFixture), "ME")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Number == 1 {
			equal(t, row.Reasons, []string{"you"})
			return
		}
	}
	t.Fatal("no row 1")
}

func TestRequestToMyTeamIsLabeledWithTeamName(t *testing.T) {
	equal(t, fixtureRow(t, 2).Reasons, []string{"Mobile"})
}

func TestRequestToOtherTeamHasNoLabel(t *testing.T) {
	equal(t, fixtureRow(t, 3).Reasons, []string{})
}

func TestDraftIsMarked(t *testing.T) {
	draft := fixtureRow(t, 4)
	if !draft.IsDraft || draft.Author != "ghost" {
		t.Fatalf("got draft %v author %q", draft.IsDraft, draft.Author)
	}
	if fixtureRow(t, 1).IsDraft {
		t.Fatal("row 1 is not a draft")
	}
}

func TestUserAndTeamRequestHaveBothLabels(t *testing.T) {
	equal(t, fixtureRow(t, 5).Reasons, []string{"you", "Mobile"})
}

func TestRowsAreSortedNewestFirst(t *testing.T) {
	var numbers []int
	for _, row := range fixtureRows(t) {
		numbers = append(numbers, row.Number)
	}
	equal(t, numbers, []int{5, 2, 4, 1, 3})
}

func TestFieldsAreDecoded(t *testing.T) {
	first := fixtureRow(t, 1)
	want := Row{
		Number:    1,
		Title:     "Direct request",
		URL:       "https://github.com/acme/app/pull/1",
		UpdatedAt: time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC),
		Additions: 12,
		Deletions: 3,
		Author:    "bob",
	}
	if first.Number != want.Number || first.Title != want.Title || first.URL != want.URL ||
		!first.UpdatedAt.Equal(want.UpdatedAt) || first.Additions != want.Additions ||
		first.Deletions != want.Deletions || first.Author != want.Author {
		t.Fatalf("got %+v", first)
	}
}

func TestUserOwnedRepositoryHasNoTeams(t *testing.T) {
	rows, err := ToRows([]byte(`{ "search": { "nodes": [] }, "repositoryOwner": {} }`), "me")
	if err != nil || len(rows) != 0 {
		t.Fatalf("got %v, %v", rows, err)
	}
}
