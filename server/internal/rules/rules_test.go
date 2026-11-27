package rules

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func intp(v int) *int { return &v }

func TestParseSampleFile(t *testing.T) {
	data, err := os.ReadFile("testdata/sheepdog.yml")
	if err != nil {
		t.Fatal(err)
	}
	rules, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	want := Rules{
		Lanes: []LaneRule{
			{
				Name:            "safe",
				Authors:         []string{"dependabot[bot]"},
				Paths:           []string{"package.json", "package-lock.json"},
				MaxChangedLines: intp(50),
				MaxScore:        intp(2),
				NoMajorBumps:    true,
				RequireChecks:   true,
				Action:          ActionApproveAndAutomerge,
			},
			{
				Name:            "docs",
				Paths:           []string{"docs/**", "README.md", "CHANGELOG.md"},
				MaxChangedLines: intp(100),
				MaxScore:        intp(2),
				RequireChecks:   true,
				Action:          ActionApproveAndAutomerge,
			},
			{
				Name:            "quick_look",
				MaxChangedLines: intp(300),
				MaxScore:        intp(4),
				Action:          ActionSuggest,
			},
		},
		NeverAutomatic: []string{".github/**", "src/sentry.ts", "app.json", "metro.config.js"},
	}
	if !reflect.DeepEqual(rules, want) {
		t.Fatalf("got %+v\nwant %+v", rules, want)
	}
}

func TestParseDefaults(t *testing.T) {
	rules, err := Parse([]byte("version: 1\nlanes:\n  b:\n    add_labels: [ready]\n  a:\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := Rules{Lanes: []LaneRule{
		{Name: "b", Action: ActionSuggest, AddLabels: []string{"ready"}},
		{Name: "a", Action: ActionSuggest},
	}}
	if !reflect.DeepEqual(rules, want) {
		t.Fatalf("got %+v, want %+v", rules, want)
	}
	if _, err := Parse([]byte("version: 1\n")); err != nil {
		t.Fatalf("a file without lanes: %v", err)
	}
}

func TestParseClassification(t *testing.T) {
	cases := map[string]ClassificationRule{
		"version: 1\n":                                                 {},
		"version: 1\nclassification:\n":                                {},
		"version: 1\nclassification:\n  model: a/b\n":                  {Model: "a/b"},
		"version: 1\nclassification: {model: a/b, ci: true}\n":         {Model: "a/b", CI: true},
		"version: 1\nclassification:\n  model: a/b\n  ci: false\n":     {Model: "a/b"},
		"version: 1\nclassification:\n  model: a/b\n  comment: true\n": {Model: "a/b", Comment: true},
	}
	for data, want := range cases {
		rules, err := Parse([]byte(data))
		if err != nil || rules.Classification != want {
			t.Errorf("%q: got %+v, %v, want %+v", data, rules.Classification, err, want)
		}
	}
}

func TestParseReview(t *testing.T) {
	cases := map[string]Rules{
		"version: 1\n":                            {},
		"version: 1\nreview:\n":                   {},
		"version: 1\nreview:\n  model: r/model\n": {Review: ReviewRule{Model: "r/model"}},
		"version: 1\nreview: {model: r/m}\nclassification: {model: c/m}\n": {Review: ReviewRule{Model: "r/m"}, Classification: ClassificationRule{Model: "c/m"}},
		"version: 1\nreview:\n  comment: true\n":                           {Review: ReviewRule{Comment: true}},
		"version: 1\nreview:\n  model: r/m\n  comment: false\n":            {Review: ReviewRule{Model: "r/m"}},
	}
	for data, want := range cases {
		rules, err := Parse([]byte(data))
		if err != nil || rules.Review != want.Review || rules.Classification != want.Classification {
			t.Errorf("%q: got %+v, %v, want %+v", data, rules, err, want)
		}
	}
	for data, message := range map[string]string{
		"version: 1\nreview:\n  model: ''\n":        "review needs a model or comment: true",
		"version: 1\nreview:\n  comment: false\n":   "review needs a model or comment: true",
		"version: 1\nreview:\n  ci: true\n":         "field ci not found",
		"version: 1\nreview:\n  model: a\n  x: 1\n": "field x not found",
	} {
		if _, err := Parse([]byte(data)); err == nil || !strings.Contains(err.Error(), message) {
			t.Errorf("%q: got %v, want %q", data, err, message)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name, data, want string
	}{
		{"empty", "", "empty"},
		{"unknown top key", "version: 1\nlane: {}\n", `field lane not found`},
		{"unknown lane key", "version: 1\nlanes:\n  safe:\n    max_lines: 3\n", `line 4: lane "safe" has an unknown key "max_lines"`},
		{"unknown never_automatic key", "version: 1\nnever_automatic:\n  path: [a]\n", `field path not found`},
		{"version 2", "version: 2\nlanes: {}\n", "version must be 1, not 2"},
		{"no version", "lanes: {}\n", "version must be 1, not 0"},
		{"unknown action", "version: 1\nlanes:\n  safe:\n    action: merge\n", `lane "safe": unknown action "merge"`},
		{"manual lane", "version: 1\nlanes:\n  manual:\n    max_score: 1\n", `the lane name "manual" is reserved`},
		{"duplicate lane", "version: 1\nlanes:\n  a: {}\n  a: {}\n", `the lane "a" is defined twice`},
		{"lanes as list", "version: 1\nlanes: [a]\n", "lanes must be a mapping"},
		{"lane as list", "version: 1\nlanes:\n  a: [x]\n", `lane "a" must be a mapping`},
		{"bad type", "version: 1\nlanes:\n  a:\n    max_score: high\n", `lane "a"`},
		{"invalid lane glob", "version: 1\nlanes:\n  docs:\n    paths: [\"docs/[a\"]\n", `lane "docs": paths: invalid glob "docs/[a"`},
		{"invalid never glob", "version: 1\nnever_automatic:\n  paths: [\"{a\"]\n", `never_automatic.paths: invalid glob "{a"`},
		{"empty glob", "version: 1\nnever_automatic:\n  paths: [\"\"]\n", `invalid glob ""`},
		{"comment without model", "version: 1\nclassification:\n  comment: true\n", `classification.comment needs a classification.model`},
		{"unknown classification key", "version: 1\nclassification:\n  model: a/b\n  models: [c]\n", `field models not found`},
		{"empty model", "version: 1\nclassification:\n  model: \" \"\n", "classification.model must not be empty"},
		{"classification without model", "version: 1\nclassification: {}\n", "classification.model must not be empty"},
		{"ci without model", "version: 1\nclassification:\n  ci: true\n", "classification.ci needs a classification.model"},
		{"ci not a bool", "version: 1\nclassification:\n  model: a/b\n  ci: maybe\n", "cannot unmarshal"},
		{"unquoted bracket in a flow list", "version: 1\nlanes:\n  safe:\n    authors: [dependabot[bot]]\n", `did not find expected ',' or ']'`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.data))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error with %q", err, c.want)
			}
		})
	}
}

func TestConditions(t *testing.T) {
	base := PR{
		Author:      "dependabot",
		AuthorIsBot: true,
		Title:       "Bump expo from 57.0.1 to 57.2.0",
		Additions:   10,
		Deletions:   5,
		Files:       []string{"package.json", "package-lock.json"},
		TotalFiles:  2,
		HeadSHA:     "abc",
		CheckState:  "SUCCESS",
	}
	current := &Classification{HeadSHA: "abc", Score: 2}
	cases := []struct {
		name           string
		lane           LaneRule
		edit           func(*PR)
		classification *Classification
		want           bool
	}{
		{"no conditions", LaneRule{}, nil, nil, true},
		{"authors graphql login", LaneRule{Authors: []string{"dependabot[bot]"}}, nil, nil, true},
		{"authors rest login", LaneRule{Authors: []string{"dependabot[bot]"}}, func(pr *PR) { pr.Author = "dependabot[bot]" }, nil, true},
		{"authors plain list", LaneRule{Authors: []string{"Dependabot"}}, func(pr *PR) { pr.Author = "dependabot[bot]" }, nil, true},
		{"authors miss", LaneRule{Authors: []string{"dependabot[bot]"}}, func(pr *PR) { pr.Author = "alice" }, nil, false},
		{"authors bot entry needs a bot", LaneRule{Authors: []string{"renovate[bot]"}}, func(pr *PR) { pr.Author = "renovate"; pr.AuthorIsBot = false }, nil, false},
		{"authors plain entry matches a person", LaneRule{Authors: []string{"alice"}}, func(pr *PR) { pr.Author = "Alice"; pr.AuthorIsBot = false }, nil, true},
		{"paths match", LaneRule{Paths: []string{"package.json", "package-lock.json"}}, nil, nil, true},
		{"paths miss", LaneRule{Paths: []string{"package.json"}}, nil, nil, false},
		{"paths double star", LaneRule{Paths: []string{"docs/**"}}, func(pr *PR) { pr.Files = []string{"docs/a.md", "docs/guide/deep/b.md"} }, nil, true},
		{"paths single star stays in one directory", LaneRule{Paths: []string{"docs/*"}}, func(pr *PR) { pr.Files = []string{"docs/guide/b.md"} }, nil, false},
		{"max_changed_lines at limit", LaneRule{MaxChangedLines: intp(15)}, nil, nil, true},
		{"max_changed_lines over", LaneRule{MaxChangedLines: intp(14)}, nil, nil, false},
		{"max_score at limit", LaneRule{MaxScore: intp(2)}, nil, current, true},
		{"max_score over", LaneRule{MaxScore: intp(1)}, nil, current, false},
		{"max_score without classification", LaneRule{MaxScore: intp(10)}, nil, nil, false},
		{"max_score of an older head", LaneRule{MaxScore: intp(10)}, nil, &Classification{HeadSHA: "old", Score: 1}, false},
		{"no_major_bumps minor", LaneRule{NoMajorBumps: true}, nil, nil, true},
		{"no_major_bumps with v prefix", LaneRule{NoMajorBumps: true}, func(pr *PR) { pr.Title = "chore(deps): bump sentry-cocoa FROM v8.1.0 TO v8.2.0" }, nil, true},
		{"no_major_bumps major", LaneRule{NoMajorBumps: true}, func(pr *PR) { pr.Title = "Bump expo from 57.0.1 to 58.0.0" }, nil, false},
		{"no_major_bumps without versions", LaneRule{NoMajorBumps: true}, func(pr *PR) { pr.Title = "Bump the npm group with 3 updates" }, nil, false},
		{"require_checks green", LaneRule{RequireChecks: true}, nil, nil, true},
		{"require_checks failing", LaneRule{RequireChecks: true}, func(pr *PR) { pr.CheckState = "FAILURE" }, nil, false},
		{"require_checks pending", LaneRule{RequireChecks: true}, func(pr *PR) { pr.CheckState = "PENDING" }, nil, false},
		{"require_checks without checks", LaneRule{RequireChecks: true}, func(pr *PR) { pr.CheckState = "" }, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pr := base
			pr.Files = append([]string{}, base.Files...)
			if c.edit != nil {
				c.edit(&pr)
			}
			c.lane.Name = "lane"
			c.lane.Action = ActionSuggest
			got := Evaluate(Rules{Lanes: []LaneRule{c.lane}}, pr, c.classification)
			if (got.Name == "lane") != c.want {
				t.Fatalf("got lane %q, want match %v", got.Name, c.want)
			}
		})
	}
}

func sampleRules(t *testing.T) Rules {
	t.Helper()
	data, err := os.ReadFile("testdata/sheepdog.yml")
	if err != nil {
		t.Fatal(err)
	}
	rules, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

func TestEvaluateSample(t *testing.T) {
	rules := sampleRules(t)
	dependabot := PR{
		Author:      "dependabot",
		AuthorIsBot: true,
		Title:       "Bump axios from 1.7.2 to 1.7.4",
		Additions:   8,
		Deletions:   8,
		Files:       []string{"package.json", "package-lock.json"},
		TotalFiles:  2,
		HeadSHA:     "head",
		CheckState:  "SUCCESS",
	}
	current := &Classification{HeadSHA: "head", Score: 1}

	manyFiles := make([]string, MaxFiles)
	for i := range manyFiles {
		manyFiles[i] = "docs/page.md"
	}

	cases := []struct {
		name           string
		edit           func(*PR)
		classification *Classification
		want           Lane
	}{
		{
			name:           "safe dependabot update",
			classification: current,
			want: Lane{Name: "safe", Action: ActionApproveAndAutomerge, Notes: []string{
				"Author dependabot", "Changes only lane paths", "16 changed lines", "Score 1", "No major update", "Checks are green",
			}},
		},
		{
			name: "missing classification",
			want: Lane{Name: Manual, Action: ActionSuggest, Notes: []string{"Needs a classification of the current commit"}, NeedsClassification: true},
		},
		{
			name:           "classification of an older commit",
			classification: &Classification{HeadSHA: "older", Score: 1},
			want:           Lane{Name: Manual, Action: ActionSuggest, Notes: []string{"Needs a classification of the current commit"}, NeedsClassification: true},
		},
		{
			name:           "major update",
			edit:           func(pr *PR) { pr.Title = "Bump expo from 57.0.1 to 58.0.0" },
			classification: current,
			want:           Lane{Name: "quick_look", Action: ActionSuggest, Notes: []string{"Major update from 57 to 58"}},
		},
		{
			name:           "checks not green",
			edit:           func(pr *PR) { pr.CheckState = "PENDING" },
			classification: current,
			want:           Lane{Name: "quick_look", Action: ActionSuggest, Notes: []string{"Checks are not green"}},
		},
		{
			name:           "never_automatic wins over a low score",
			edit:           func(pr *PR) { pr.Author = "alice"; pr.Files = []string{"README.md", "src/sentry.ts"} },
			classification: current,
			want:           Lane{Name: Manual, Action: ActionSuggest, Notes: []string{"Changes src/sentry.ts"}},
		},
		{
			name:           "change to the rules file",
			edit:           func(pr *PR) { pr.Files = []string{".github/sheepdog.yml"} },
			classification: current,
			want:           Lane{Name: Manual, Action: ActionSuggest, Notes: []string{"Changes .github/sheepdog.yml"}},
		},
		{
			name: "more than 100 files",
			edit: func(pr *PR) {
				pr.Author = "alice"
				pr.Files = manyFiles
				pr.TotalFiles = MaxFiles + 1
			},
			classification: current,
			want:           Lane{Name: Manual, Action: ActionSuggest, Notes: []string{"Changes more than 100 files"}},
		},
		{
			name: "docs lane",
			edit: func(pr *PR) {
				pr.Author = "alice"
				pr.Title = "Fix typo"
				pr.Files = []string{"docs/setup/ios.md", "README.md"}
			},
			classification: current,
			want: Lane{Name: "docs", Action: ActionApproveAndAutomerge, Notes: []string{
				"Changes only lane paths", "16 changed lines", "Score 1", "Checks are green",
			}},
		},
		{
			name: "suggest lane notes come from the closest action lane",
			edit: func(pr *PR) {
				pr.Author = "alice"
				pr.Title = "Add a screen"
				pr.Files = []string{"src/screen.tsx"}
				pr.Additions = 120
			},
			classification: &Classification{HeadSHA: "head", Score: 3},
			want: Lane{Name: "quick_look", Action: ActionSuggest, Notes: []string{
				"Changes src/screen.tsx", "128 changed lines, more than 100", "Score 3, more than 2",
			}},
		},
		{
			name: "no lane",
			edit: func(pr *PR) {
				pr.Author = "alice"
				pr.Title = "Rewrite"
				pr.Files = []string{"src/app.tsx"}
				pr.Additions = 900
				pr.CheckState = ""
			},
			classification: &Classification{HeadSHA: "head", Score: 7},
			want: Lane{Name: Manual, Action: ActionSuggest, Notes: []string{
				"Changes src/app.tsx", "908 changed lines, more than 100", "Score 7, more than 2", "Checks are not green",
			}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pr := dependabot
			if c.edit != nil {
				c.edit(&pr)
			}
			got := Evaluate(rules, pr, c.classification)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func TestEvaluateFirstLaneWinsAndLabels(t *testing.T) {
	rules, err := Parse([]byte(`version: 1
lanes:
  first:
    max_changed_lines: 10
    action: approve-and-automerge
    add_labels: [ready-to-merge]
  second:
    action: approve-and-automerge
`))
	if err != nil {
		t.Fatal(err)
	}
	got := Evaluate(rules, PR{Author: "alice", Additions: 3}, nil)
	want := Lane{Name: "first", Action: ActionApproveAndAutomerge, Labels: []string{"ready-to-merge"}, Notes: []string{"3 changed lines"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if got := Evaluate(rules, PR{Author: "alice", Additions: 30}, nil); got.Name != "second" || len(got.Notes) != 0 || got.Notes == nil {
		t.Fatalf("got %+v, want the lane second with empty notes", got)
	}
}

func TestEvaluateWithoutActionLanes(t *testing.T) {
	got := Evaluate(Rules{}, PR{Author: "alice"}, nil)
	want := Lane{Name: Manual, Action: ActionSuggest, Notes: []string{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestNeedsClassification(t *testing.T) {
	rules, err := Parse([]byte(`version: 1
lanes:
  docs:
    paths: ["docs/**"]
  safe:
    max_changed_lines: 50
    max_score: 2
    require_checks: true
    action: approve-and-automerge
`))
	if err != nil {
		t.Fatal(err)
	}
	pr := PR{Author: "alice", Additions: 10, Files: []string{"src/a.go"}, TotalFiles: 1, HeadSHA: "head", CheckState: "SUCCESS"}
	cases := []struct {
		name           string
		edit           func(*PR)
		classification *Classification
		want           bool
	}{
		{name: "only the score is missing", want: true},
		{name: "score of an older commit", classification: &Classification{HeadSHA: "older", Score: 1}, want: true},
		{name: "score of the head commit", classification: &Classification{HeadSHA: "head", Score: 5}},
		{name: "checks are not green", edit: func(pr *PR) { pr.CheckState = "PENDING" }},
		{name: "too many lines", edit: func(pr *PR) { pr.Additions = 90 }},
		{name: "an earlier lane matches", edit: func(pr *PR) { pr.Files = []string{"docs/a.md"} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pr := pr
			if c.edit != nil {
				c.edit(&pr)
			}
			if got := Evaluate(rules, pr, c.classification); got.NeedsClassification != c.want {
				t.Errorf("got %+v, want NeedsClassification %v", got, c.want)
			}
		})
	}
	if got := Evaluate(rules, pr, &Classification{HeadSHA: "head", Score: 1}); got.Name != "safe" || got.NeedsClassification {
		t.Errorf("a PR in the approve lane %+v", got)
	}
}
