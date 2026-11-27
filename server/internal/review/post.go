package review

import (
	"fmt"
	"strconv"
	"strings"

	"sheepdog/internal/github"
)

// PostMarker starts the body of each review that Sheepdog posts to GitHub.
const PostMarker = "<!-- sheepdog-review -->"

var riskLabels = map[string]string{"green": "Low risk", "yellow": "Needs a careful review", "red": "High risk, review in full"}

// SafeText keeps text of the model, which comes from untrusted PR content, from mentioning people or adding a second marker.
func SafeText(text string) string {
	return strings.NewReplacer("@", "@\u200b", "<!--", "&lt;!--").Replace(strings.TrimSpace(text))
}

type diffPlace struct {
	path string
	// side is "new" or "old", as in a finding.
	side string
	line int
}

// diffPlaces returns the lines that a review comment can name, and maps the old and the new path of each file to the path for a comment.
func diffPlaces(diff string) (map[diffPlace]bool, map[string]string) {
	places := map[diffPlace]bool{}
	paths := map[string]string{}
	oldPath, path := "", ""
	inHunk := false
	oldLine, newLine := 0, 0
	for _, text := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(text, "diff --git "):
			oldPath, path, inHunk = "", "", false
		case !inHunk && strings.HasPrefix(text, "--- "):
			oldPath = cleanPath(strings.TrimPrefix(text, "--- "))
		case !inHunk && strings.HasPrefix(text, "+++ "):
			path = cleanPath(strings.TrimPrefix(text, "+++ "))
			// A deleted file has only its old path, and its comments use it.
			if path == "/dev/null" {
				path = oldPath
			}
			if oldPath != "/dev/null" {
				paths[oldPath] = path
			}
			paths[path] = path
		case strings.HasPrefix(text, "@@ "):
			if header := hunkHeader.FindStringSubmatch(text); header != nil {
				oldLine, _ = strconv.Atoi(header[1])
				newLine, _ = strconv.Atoi(header[2])
				inHunk = true
			}
		case inHunk && strings.HasPrefix(text, "+"):
			places[diffPlace{path, "new", newLine}] = true
			newLine++
		case inHunk && strings.HasPrefix(text, "-"):
			places[diffPlace{path, "old", oldLine}] = true
			oldLine++
		case inHunk && strings.HasPrefix(text, " "):
			places[diffPlace{path, "new", newLine}] = true
			places[diffPlace{path, "old", oldLine}] = true
			oldLine++
			newLine++
		}
	}
	return places, paths
}

// cleanPath removes the quotes of git and the prefix a/ or b/, and the prefix ./ or / that a model can write.
func cleanPath(path string) string {
	if unquoted, err := strconv.Unquote(path); err == nil && strings.HasPrefix(path, `"`) {
		path = unquoted
	}
	if path == "/dev/null" {
		return path
	}
	path = strings.TrimPrefix(strings.TrimPrefix(path, "./"), "/")
	if rest, ok := strings.CutPrefix(path, "a/"); ok {
		return rest
	}
	if rest, ok := strings.CutPrefix(path, "b/"); ok {
		return rest
	}
	return path
}

// GitHubReview turns a review into the body and the line comments of a GitHub review of the diff.
// A finding whose line is in the diff becomes a line comment. Every other finding is part of the body.
func GitHubReview(r Review, model, commit, diff string) (string, []github.ReviewComment) {
	places, paths := diffPlaces(diff)
	comments := []github.ReviewComment{}
	var rest []Finding
	for _, finding := range r.Findings {
		path, known := paths[cleanPath(finding.File)]
		side := finding.Side
		if side == "" {
			side = "new"
		}
		if !known || finding.Line == nil || !places[diffPlace{path, side, *finding.Line}] {
			rest = append(rest, finding)
			continue
		}
		githubSide := "RIGHT"
		if side == "old" {
			githubSide = "LEFT"
		}
		comments = append(comments, github.ReviewComment{
			Path: path,
			Line: *finding.Line,
			Side: githubSide,
			Body: fmt.Sprintf("**%s**: %s\n\n%s", severityLabel(finding.Severity), SafeText(finding.Title), SafeText(finding.Detail)),
		})
	}

	var body strings.Builder
	fmt.Fprintf(&body, "%s\n### Sheepdog review: %s\n\n%s\n", PostMarker, riskLabels[r.Risk], SafeText(r.Summary))
	if len(rest) > 0 {
		body.WriteString("\n")
		for _, finding := range rest {
			place := strings.ReplaceAll(SafeText(finding.File), "`", "'")
			if finding.Line != nil {
				place += ":" + strconv.Itoa(*finding.Line)
			}
			fmt.Fprintf(&body, "- **%s** `%s` %s: %s\n", severityLabel(finding.Severity), place, SafeText(finding.Title), SafeText(finding.Detail))
		}
	}
	fmt.Fprintf(&body, "\nAI review by `%s` of commit %s, posted with Sheepdog.\n", SafeText(model), commit)
	return body.String(), comments
}

func severityLabel(severity string) string {
	return strings.ToUpper(severity[:1]) + severity[1:]
}
