// Package rules parses .github/sheepdog.yml and puts a pull request in a lane.
package rules

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"go.yaml.in/yaml/v3"
)

const (
	Manual                    = "manual"
	ActionSuggest             = "suggest"
	ActionApproveAndAutomerge = "approve-and-automerge"
	// MaxFiles is the number of changed file paths that the poll reads.
	MaxFiles = 100
)

type Rules struct {
	Lanes          []LaneRule
	NeverAutomatic []string
}

// LaneRule is one lane of the file. A nil pointer or an empty list sets no condition.
type LaneRule struct {
	Name            string   `yaml:"-"`
	Authors         []string `yaml:"authors"`
	Paths           []string `yaml:"paths"`
	MaxChangedLines *int     `yaml:"max_changed_lines"`
	MaxScore        *int     `yaml:"max_score"`
	NoMajorBumps    bool     `yaml:"no_major_bumps"`
	RequireChecks   bool     `yaml:"require_checks"`
	Action          string   `yaml:"action"`
	AddLabels       []string `yaml:"add_labels"`
}

type PR struct {
	Author string
	// AuthorIsBot is true for a GitHub App author, whose GraphQL type is Bot.
	AuthorIsBot bool
	Title       string
	Additions   int
	Deletions   int
	Files       []string
	TotalFiles  int
	HeadSHA     string
	// CheckState is the check rollup state of the head commit, for example SUCCESS. Empty means no checks.
	CheckState string
}

type Classification struct {
	HeadSHA string
	Score   int
}

type Lane struct {
	Name   string
	Action string
	Labels []string
	Notes  []string
}

var laneKeys = map[string]bool{
	"authors": true, "paths": true, "max_changed_lines": true, "max_score": true,
	"no_major_bumps": true, "require_checks": true, "action": true, "add_labels": true,
}

// Named types keep the unknown key errors of the decoder short.
type sheepdogYAML struct {
	Version        int            `yaml:"version"`
	Lanes          yaml.Node      `yaml:"lanes"`
	NeverAutomatic neverAutomatic `yaml:"never_automatic"`
}

type neverAutomatic struct {
	Paths []string `yaml:"paths"`
}

// Parse reads and validates the content of .github/sheepdog.yml. The lanes keep the order of the file.
func Parse(data []byte) (Rules, error) {
	var file sheepdogYAML
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		if errors.Is(err, io.EOF) {
			return Rules{}, errors.New("the file is empty")
		}
		return Rules{}, err
	}
	if file.Version != 1 {
		return Rules{}, fmt.Errorf("version must be 1, not %d", file.Version)
	}
	if err := validateGlobs("never_automatic.paths", file.NeverAutomatic.Paths); err != nil {
		return Rules{}, err
	}
	rules := Rules{NeverAutomatic: file.NeverAutomatic.Paths}

	node := &file.Lanes
	if node.Kind == 0 || node.Tag == "!!null" {
		return rules, nil
	}
	if node.Kind != yaml.MappingNode {
		return Rules{}, fmt.Errorf("line %d: lanes must be a mapping of lane names", node.Line)
	}
	seen := map[string]bool{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		name := key.Value
		switch {
		case name == "":
			return Rules{}, fmt.Errorf("line %d: a lane needs a name", key.Line)
		case name == Manual:
			return Rules{}, fmt.Errorf("line %d: the lane name %q is reserved for pull requests without a lane", key.Line, Manual)
		case seen[name]:
			return Rules{}, fmt.Errorf("line %d: the lane %q is defined twice", key.Line, name)
		}
		seen[name] = true
		lane := LaneRule{Name: name}
		if value.Tag != "!!null" {
			if value.Kind != yaml.MappingNode {
				return Rules{}, fmt.Errorf("line %d: lane %q must be a mapping", value.Line, name)
			}
			for j := 0; j < len(value.Content); j += 2 {
				if field := value.Content[j]; !laneKeys[field.Value] {
					return Rules{}, fmt.Errorf("line %d: lane %q has an unknown key %q", field.Line, name, field.Value)
				}
			}
			if err := value.Decode(&lane); err != nil {
				return Rules{}, fmt.Errorf("lane %q: %w", name, err)
			}
		}
		switch lane.Action {
		case "":
			lane.Action = ActionSuggest
		case ActionSuggest, ActionApproveAndAutomerge:
		default:
			return Rules{}, fmt.Errorf("lane %q: unknown action %q, use %q or %q", name, lane.Action, ActionSuggest, ActionApproveAndAutomerge)
		}
		if err := validateGlobs(fmt.Sprintf("lane %q: paths", name), lane.Paths); err != nil {
			return Rules{}, err
		}
		rules.Lanes = append(rules.Lanes, lane)
	}
	return rules, nil
}

func validateGlobs(field string, globs []string) error {
	for _, glob := range globs {
		if glob == "" || !doublestar.ValidatePattern(glob) {
			return fmt.Errorf("%s: invalid glob %q", field, glob)
		}
	}
	return nil
}

// Evaluate returns the first lane whose conditions all match, or the lane manual.
func Evaluate(rules Rules, pr PR, classification *Classification) Lane {
	if pr.TotalFiles > MaxFiles {
		return Lane{Name: Manual, Action: ActionSuggest, Notes: []string{fmt.Sprintf("Changes more than %d files", MaxFiles)}}
	}
	for _, file := range pr.Files {
		if matchesAny(rules.NeverAutomatic, file) {
			return Lane{Name: Manual, Action: ActionSuggest, Notes: []string{"Changes " + file}}
		}
	}

	result := Lane{Name: Manual, Action: ActionSuggest}
	for _, lane := range rules.Lanes {
		matched, failed := check(lane, pr, classification)
		if len(failed) == 0 {
			result = Lane{Name: lane.Name, Action: lane.Action, Labels: lane.AddLabels}
			if lane.Action == ActionApproveAndAutomerge {
				result.Notes = append([]string{}, matched...)
				return result
			}
			break
		}
	}

	var closest []string
	found := false
	for _, lane := range rules.Lanes {
		if lane.Action != ActionApproveAndAutomerge {
			continue
		}
		if _, failed := check(lane, pr, classification); !found || len(failed) < len(closest) {
			closest, found = failed, true
		}
	}
	result.Notes = append([]string{}, closest...)
	return result
}

var versionUpdate = regexp.MustCompile(`(?i)\bfrom\s+v?(\d+)(?:\.[0-9a-z.+-]*)?\s+to\s+v?(\d+)`)

// check returns a short note for each matched and each failed condition of the lane.
func check(lane LaneRule, pr PR, classification *Classification) (matched, failed []string) {
	result := func(ok bool, yes, no string) {
		if ok {
			matched = append(matched, yes)
		} else {
			failed = append(failed, no)
		}
	}

	if len(lane.Authors) > 0 {
		ok := false
		for _, author := range lane.Authors {
			ok = ok || authorMatches(author, pr)
		}
		result(ok, "Author "+pr.Author, "Author "+pr.Author+" is not in the lane")
	}
	if len(lane.Paths) > 0 {
		outside := ""
		for _, file := range pr.Files {
			if !matchesAny(lane.Paths, file) {
				outside = file
				break
			}
		}
		result(outside == "", "Changes only lane paths", "Changes "+outside)
	}
	if lane.MaxChangedLines != nil {
		lines := pr.Additions + pr.Deletions
		result(lines <= *lane.MaxChangedLines,
			fmt.Sprintf("%d changed lines", lines),
			fmt.Sprintf("%d changed lines, more than %d", lines, *lane.MaxChangedLines))
	}
	if lane.MaxScore != nil {
		switch {
		case classification == nil || classification.HeadSHA == "" || classification.HeadSHA != pr.HeadSHA:
			failed = append(failed, "Needs a classification of the current commit")
		default:
			result(classification.Score <= *lane.MaxScore,
				fmt.Sprintf("Score %d", classification.Score),
				fmt.Sprintf("Score %d, more than %d", classification.Score, *lane.MaxScore))
		}
	}
	if lane.NoMajorBumps {
		match := versionUpdate.FindStringSubmatch(pr.Title)
		switch {
		case match == nil:
			failed = append(failed, "Title shows no version update")
		default:
			from, _ := strconv.Atoi(match[1])
			to, _ := strconv.Atoi(match[2])
			result(from == to, "No major update", fmt.Sprintf("Major update from %d to %d", from, to))
		}
	}
	if lane.RequireChecks {
		result(pr.CheckState == "SUCCESS", "Checks are green", "Checks are not green")
	}
	return matched, failed
}

// An entry with [bot] matches only a bot author, so that a person with the login renovate cannot match renovate[bot].
func authorMatches(entry string, pr PR) bool {
	if strings.HasSuffix(strings.ToLower(entry), "[bot]") && !pr.AuthorIsBot {
		return false
	}
	return normalizeLogin(entry) == normalizeLogin(pr.Author)
}

func normalizeLogin(login string) string {
	return strings.TrimSuffix(strings.ToLower(login), "[bot]")
}

func matchesAny(globs []string, file string) bool {
	for _, glob := range globs {
		if doublestar.MatchUnvalidated(glob, file) {
			return true
		}
	}
	return false
}
