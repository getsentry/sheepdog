package github

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

type Row struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	IsDraft   bool      `json:"isDraft"`
	UpdatedAt time.Time `json:"updatedAt"`
	Additions int       `json:"additions"`
	Deletions int       `json:"deletions"`
	Author    string    `json:"author"`
	Reasons   []string  `json:"reasons"`
}

type nodes[T any] struct {
	Nodes []T `json:"nodes"`
}

// ToRows reads the data object of the review request query.
func ToRows(data []byte, login string) ([]Row, error) {
	var response struct {
		Search nodes[struct {
			Number    int       `json:"number"`
			Title     string    `json:"title"`
			URL       string    `json:"url"`
			IsDraft   bool      `json:"isDraft"`
			UpdatedAt time.Time `json:"updatedAt"`
			Additions int       `json:"additions"`
			Deletions int       `json:"deletions"`
			Author    *struct {
				Login string `json:"login"`
			} `json:"author"`
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
		reasons := []string{}
		for _, request := range pr.ReviewRequests.Nodes {
			reviewer := request.RequestedReviewer
			switch {
			case reviewer == nil:
			case reviewer.Login != "" && strings.EqualFold(reviewer.Login, login):
				reasons = append(reasons, "you")
			case reviewer.CombinedSlug != "" && myTeams[reviewer.CombinedSlug] && reviewer.Name != "":
				reasons = append(reasons, reviewer.Name)
			}
		}
		author := "ghost"
		if pr.Author != nil {
			author = pr.Author.Login
		}
		rows = append(rows, Row{
			Number:    pr.Number,
			Title:     pr.Title,
			URL:       pr.URL,
			IsDraft:   pr.IsDraft,
			UpdatedAt: pr.UpdatedAt,
			Additions: pr.Additions,
			Deletions: pr.Deletions,
			Author:    author,
			Reasons:   reasons,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].UpdatedAt.After(rows[j].UpdatedAt) })
	return rows, nil
}
