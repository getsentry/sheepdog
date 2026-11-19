package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"sheepdog/internal/config"
	"sheepdog/internal/github"
	"sheepdog/internal/openrouter"
	"sheepdog/internal/review"
	"sheepdog/internal/rules"
)

// classify runs in a GitHub Actions job. It classifies the head commit of a PR with classification.model from the rules on the base branch,
// and prints the result as a notice annotation, which the server reads from the check run.
func classify(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer, openRouterURL string) error {
	flags := flag.NewFlagSet("classify", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	number := flags.Int("pr", 0, "the pull request number")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("classify: %w", err)
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("classify: unexpected argument %q", flags.Arg(0))
	}
	if *number <= 0 {
		return errors.New("classify: --pr must be the number of a pull request")
	}
	token, key, repo := getenv("GITHUB_TOKEN"), getenv("OPENROUTER_API_KEY"), getenv("GITHUB_REPOSITORY")
	switch {
	case token == "":
		return errors.New("GITHUB_TOKEN is required")
	case key == "":
		return errors.New("OPENROUTER_API_KEY is required: add it as an Actions secret and as a Dependabot secret of the repository")
	case repo == "":
		return errors.New("GITHUB_REPOSITORY is required")
	}
	if err := github.ValidRepo(repo); err != nil {
		return err
	}
	githubURL := getenv("GITHUB_API_URL")
	if githubURL == "" {
		githubURL = config.DefaultGitHubURL
	}

	gh := github.New(githubURL, token)
	detail, input, err := review.LoadInput(ctx, gh, repo, *number)
	if err != nil {
		return fmt.Errorf("load %s#%d: %w", repo, *number, err)
	}
	text, err := gh.RulesFile(ctx, repo, detail.BaseRefName)
	if err != nil {
		return fmt.Errorf("read %s on %s: %w", github.RulesPath, detail.BaseRefName, err)
	}
	if text == nil {
		return fmt.Errorf("%s is missing on %s: add it with classification.model", github.RulesPath, detail.BaseRefName)
	}
	parsed, err := rules.Parse([]byte(*text))
	if err != nil {
		return fmt.Errorf("%s on %s is not valid: %w", github.RulesPath, detail.BaseRefName, err)
	}
	model := parsed.Classification.Model
	if model == "" {
		return fmt.Errorf("%s on %s has no classification.model", github.RulesPath, detail.BaseRefName)
	}

	result, truncated, err := review.Classify(ctx, openrouter.New(openRouterURL, key), model, input)
	if err != nil {
		return fmt.Errorf("classify with %s: %w", model, err)
	}
	c := github.CIClassification{Score: result.Score, Reason: result.Reason, Model: model, HeadSHA: detail.HeadRefOid, DiffTruncated: truncated}
	message, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if path := getenv("GITHUB_STEP_SUMMARY"); path != "" {
		if err := appendSummary(path, c); err != nil {
			return fmt.Errorf("write the step summary: %w", err)
		}
	}
	if _, err := fmt.Fprintln(stdout, notice(github.CIAnnotationTitle, string(message))); err != nil {
		return err
	}
	if !parsed.Classification.Comment {
		return nil
	}
	// The score is already in the annotation, so a failed comment is a warning and the job still passes.
	if err := upsertComment(ctx, gh, repo, *number, commentBody(c)); err != nil {
		_, err = fmt.Fprintln(stdout, workflowCommand("warning", "Sheepdog comment", "could not post the classification comment: "+err.Error()+". The workflow needs the permission pull-requests: write."))
		return err
	}
	return nil
}

// commentMarker starts the one comment that Sheepdog keeps up to date on a pull request.
const commentMarker = "<!-- sheepdog-classification -->"

// The comment of the job token has this author.
const actionsBot = "github-actions[bot]"

func upsertComment(ctx context.Context, gh *github.Client, repo string, number int, body string) error {
	comments, err := gh.IssueComments(ctx, repo, number)
	if err != nil {
		return err
	}
	for _, comment := range comments {
		if comment.User.Login == actionsBot && strings.HasPrefix(comment.Body, commentMarker) {
			return gh.UpdateIssueComment(ctx, repo, comment.ID, body)
		}
	}
	return gh.CreateIssueComment(ctx, repo, number, body)
}

// commentBody quotes the reason of the model, which comes from untrusted PR content, so that it cannot mention people or add a second marker.
func commentBody(c github.CIClassification) string {
	reason := strings.NewReplacer("@", "@\u200b", "<!--", "&lt;!--").Replace(strings.TrimSpace(c.Reason))
	quoted := "> " + strings.ReplaceAll(reason, "\n", "\n> ")
	text := fmt.Sprintf("%s\n### Sheepdog classification: %d/10, %s\n\n%s\n\nModel `%s`, commit %s.", commentMarker, c.Score, dangerLabel(c.Score), quoted, c.Model, c.HeadSHA)
	if c.DiffTruncated {
		text += fmt.Sprintf(" The model saw only the first %d characters of the diff.", review.MaxDiffCharacters)
	}
	return text + "\n"
}

func dangerLabel(score int) string {
	switch {
	case score <= 1:
		return "no danger, can be merged"
	case score <= 3:
		return "low danger"
	case score <= 6:
		return "medium danger"
	case score <= 9:
		return "high danger"
	default:
		return "extreme danger"
	}
}

func notice(title, message string) string {
	return workflowCommand("notice", title, message)
}

// workflowCommand formats a workflow command. GitHub reads %25, %0D, and %0A in the message as %, CR, and LF.
func workflowCommand(command, title, message string) string {
	return "::" + command + " title=" + title + "::" + strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(message)
}

func appendSummary(path string, c github.CIClassification) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("### Sheepdog classification\n\nScore **%d** of 10 from `%s` for commit `%s`.\n\n%s\n", c.Score, c.Model, c.HeadSHA, c.Reason)
	if c.DiffTruncated {
		text += fmt.Sprintf("\nThe model saw only the first %d characters of the diff.\n", review.MaxDiffCharacters)
	}
	if _, err := io.WriteString(file, text); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
