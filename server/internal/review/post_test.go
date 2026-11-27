package review

import (
	"reflect"
	"strings"
	"testing"

	"sheepdog/internal/github"
)

const postDiff = `diff --git a/src/app.ts b/src/app.ts
index 1111111..2222222 100644
--- a/src/app.ts
+++ b/src/app.ts
@@ -10,3 +10,3 @@ export function start() {
   const a = 1
-  const b = 2
+  const b = 3
   return a
diff --git a/old.ts b/new.ts
similarity index 90%
rename from old.ts
rename to new.ts
--- a/old.ts
+++ b/new.ts
@@ -1 +1 @@
-x
+y
diff --git a/gone.ts b/gone.ts
deleted file mode 100644
--- a/gone.ts
+++ /dev/null
@@ -1 +0,0 @@
-bye
`

func line(n int) *int { return &n }

func TestGitHubReviewPutsFindingsOnLines(t *testing.T) {
	r := Review{Risk: "yellow", Summary: "Changes b. Ask @alice <!-- x -->", Findings: []Finding{
		{Severity: "high", File: "src/app.ts", Line: line(11), Side: "new", Title: "New b", Detail: "b is 3 now."},
		{Severity: "low", File: "./src/app.ts", Line: line(11), Side: "old", Title: "Old b", Detail: "d"},
		{Severity: "medium", File: "src/app.ts", Line: line(12), Side: "new", Title: "Context", Detail: "d"},
		{Severity: "low", File: "old.ts", Line: line(1), Side: "new", Title: "Renamed", Detail: "d"},
		{Severity: "low", File: "gone.ts", Line: line(1), Side: "old", Title: "Deleted", Detail: "d"},
		{Severity: "medium", File: "src/app.ts", Line: line(99), Side: "new", Title: "Far away", Detail: "Not in the diff."},
		{Severity: "low", File: "README.md", Line: nil, Side: "new", Title: "Docs", Detail: "Update the docs."},
		{Severity: "low", File: "src/app.ts", Line: nil, Title: "Old review", Detail: "No side."},
	}}
	body, comments := GitHubReview(r, "m/model", "abc123", postDiff)
	want := []github.ReviewComment{
		{Path: "src/app.ts", Line: 11, Side: "RIGHT", Body: "**High**: New b\n\nb is 3 now."},
		{Path: "src/app.ts", Line: 11, Side: "LEFT", Body: "**Low**: Old b\n\nd"},
		{Path: "src/app.ts", Line: 12, Side: "RIGHT", Body: "**Medium**: Context\n\nd"},
		{Path: "new.ts", Line: 1, Side: "RIGHT", Body: "**Low**: Renamed\n\nd"},
		{Path: "gone.ts", Line: 1, Side: "LEFT", Body: "**Low**: Deleted\n\nd"},
	}
	if !reflect.DeepEqual(comments, want) {
		t.Errorf("comments\n%+v\nwant\n%+v", comments, want)
	}
	wantBody := PostMarker + "\n### Sheepdog review: Needs a careful review\n\nChanges b. Ask @\u200balice &lt;!-- x -->\n\n" +
		"- **Medium** `src/app.ts:99` Far away: Not in the diff.\n" +
		"- **Low** `README.md` Docs: Update the docs.\n" +
		"- **Low** `src/app.ts` Old review: No side.\n" +
		"\nAI review by `m/model` of commit abc123, posted with Sheepdog.\n"
	if body != wantBody {
		t.Errorf("body\n%s\nwant\n%s", body, wantBody)
	}
}

func TestGitHubReviewWithoutFindings(t *testing.T) {
	body, comments := GitHubReview(Review{Risk: "green", Summary: "Fine.", Findings: []Finding{}}, "m", "abc", "")
	if comments == nil || len(comments) != 0 || !strings.HasPrefix(body, PostMarker+"\n### Sheepdog review: Low risk\n\nFine.\n\nAI review") {
		t.Errorf("got %q, %+v", body, comments)
	}
}
