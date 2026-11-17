package github

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Row is one review request. The fields with the JSON name "-" are facts for the lane rules that the client does not need.
type Row struct {
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	IsDraft    bool      `json:"isDraft"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Additions  int       `json:"additions"`
	Deletions  int       `json:"deletions"`
	Author     string    `json:"author"`
	AuthorBot  bool      `json:"-"`
	Reasons    []string  `json:"reasons"`
	HeadSHA    string    `json:"headSha"`
	BaseRef    string    `json:"-"`
	Files      []string  `json:"-"`
	TotalFiles int       `json:"-"`
	// CheckState is the check rollup state of the head commit, for example SUCCESS. Empty means no checks.
	CheckState string `json:"-"`
	MergeState string `json:"-"`
}

type nodes[T any] struct {
	Nodes []T `json:"nodes"`
}

// rowFields are the fields of rowNode.
const rowFields = `
fragment rowFields on PullRequest {
  number title url isDraft updatedAt additions deletions headRefOid baseRefName mergeStateStatus
  author { __typename login }
  files(first: 100) { totalCount nodes { path } }
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
}`

type rowNode struct {
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	IsDraft    bool      `json:"isDraft"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Additions  int       `json:"additions"`
	Deletions  int       `json:"deletions"`
	HeadRefOid string    `json:"headRefOid"`
	BaseRef    string    `json:"baseRefName"`
	MergeState string    `json:"mergeStateStatus"`
	Author     *struct {
		Typename string `json:"__typename"`
		Login    string `json:"login"`
	} `json:"author"`
	Files *struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			Path string `json:"path"`
		} `json:"nodes"`
	} `json:"files"`
	Commits nodes[struct {
		Commit struct {
			StatusCheckRollup *struct {
				State string `json:"state"`
			} `json:"statusCheckRollup"`
		} `json:"commit"`
	}] `json:"commits"`
}

func (pr rowNode) row() Row {
	row := Row{
		Number:     pr.Number,
		Title:      pr.Title,
		URL:        pr.URL,
		IsDraft:    pr.IsDraft,
		UpdatedAt:  pr.UpdatedAt,
		Additions:  pr.Additions,
		Deletions:  pr.Deletions,
		Author:     "ghost",
		Reasons:    []string{},
		HeadSHA:    pr.HeadRefOid,
		BaseRef:    pr.BaseRef,
		Files:      []string{},
		MergeState: pr.MergeState,
	}
	if pr.Author != nil {
		row.Author = pr.Author.Login
		row.AuthorBot = pr.Author.Typename == "Bot"
	}
	if pr.Files != nil {
		row.TotalFiles = pr.Files.TotalCount
		for _, file := range pr.Files.Nodes {
			row.Files = append(row.Files, file.Path)
		}
	}
	for _, commit := range pr.Commits.Nodes {
		if rollup := commit.Commit.StatusCheckRollup; rollup != nil {
			row.CheckState = rollup.State
		}
	}
	return row
}

// ToRows reads the data object of the review request query.
func ToRows(data []byte, login string) ([]Row, error) {
	var response struct {
		Search nodes[struct {
			rowNode
			ReviewRequests nodes[struct {
				RequestedReviewer *struct {
					Login        string `json:"login"`
					CombinedSlug string `json:"combinedSlug"`
					Name         string `json:"name"`
				} `json:"requestedReviewer"`
			}] `json:"reviewRequests"`
		}] `json:"search"`
		RepositoryOwner *struct {
			Teams *nodes[struct {
				CombinedSlug string `json:"combinedSlug"`
			}] `json:"teams"`
		} `json:"repositoryOwner"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	myTeams := map[string]bool{}
	if owner := response.RepositoryOwner; owner != nil && owner.Teams != nil {
		for _, team := range owner.Teams.Nodes {
			myTeams[team.CombinedSlug] = true
		}
	}

	rows := make([]Row, 0, len(response.Search.Nodes))
	for _, pr := range response.Search.Nodes {
		row := pr.row()
		for _, request := range pr.ReviewRequests.Nodes {
			reviewer := request.RequestedReviewer
			switch {
			case reviewer == nil:
			case reviewer.Login != "" && strings.EqualFold(reviewer.Login, login):
				row.Reasons = append(row.Reasons, "you")
			case reviewer.CombinedSlug != "" && myTeams[reviewer.CombinedSlug] && reviewer.Name != "":
				row.Reasons = append(row.Reasons, reviewer.Name)
			}
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].UpdatedAt.After(rows[j].UpdatedAt) })
	return rows, nil
}
