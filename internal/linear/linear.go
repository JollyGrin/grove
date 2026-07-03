// Package linear is a minimal read-only GraphQL client (net/http, no SDK).
// ovs only ever reads Linear: agents transition tickets via dev-linear MCP,
// humans finish them. See DESIGN.md "Linear integration".
package linear

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"
)

const endpoint = "https://api.linear.app/graphql"

var identifierRe = regexp.MustCompile(`[A-Z][A-Z0-9]*-\d+`)

type Comment struct {
	Author string
	Body   string
}

type Issue struct {
	Identifier  string
	Title       string
	Description string
	URL         string
	Team        string
	Labels      []string
	Comments    []Comment
}

// ParseIdentifier extracts DEV-1234 from a bare id or a Linear URL.
func ParseIdentifier(s string) (string, error) {
	m := identifierRe.FindString(s)
	if m == "" {
		return "", fmt.Errorf("no ticket identifier found in %q", s)
	}
	return m, nil
}

func query(apiKey, q string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": q, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("linear: decode response (HTTP %d): %w", resp.StatusCode, err)
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("linear: %s", envelope.Errors[0].Message)
	}
	return json.Unmarshal(envelope.Data, out)
}

const issueQuery = `query Issue($id: String!) {
  issue(id: $id) {
    identifier title description url
    team { key }
    labels { nodes { name } }
    comments { nodes { body user { displayName } createdAt } }
  }
}`

// FetchIssue fetches a ticket with comments by identifier or URL.
func FetchIssue(apiKey, idOrURL string) (*Issue, error) {
	id, err := ParseIdentifier(idOrURL)
	if err != nil {
		return nil, err
	}
	var data struct {
		Issue *struct {
			Identifier  string `json:"identifier"`
			Title       string `json:"title"`
			Description string `json:"description"`
			URL         string `json:"url"`
			Team        struct {
				Key string `json:"key"`
			} `json:"team"`
			Labels struct {
				Nodes []struct {
					Name string `json:"name"`
				} `json:"nodes"`
			} `json:"labels"`
			Comments struct {
				Nodes []struct {
					Body string `json:"body"`
					User *struct {
						DisplayName string `json:"displayName"`
					} `json:"user"`
					CreatedAt string `json:"createdAt"`
				} `json:"nodes"`
			} `json:"comments"`
		} `json:"issue"`
	}
	if err := query(apiKey, issueQuery, map[string]any{"id": id}, &data); err != nil {
		return nil, err
	}
	if data.Issue == nil {
		return nil, fmt.Errorf("ticket %s not found", id)
	}
	iss := &Issue{
		Identifier:  data.Issue.Identifier,
		Title:       data.Issue.Title,
		Description: data.Issue.Description,
		URL:         data.Issue.URL,
		Team:        data.Issue.Team.Key,
	}
	for _, l := range data.Issue.Labels.Nodes {
		iss.Labels = append(iss.Labels, l.Name)
	}
	for _, c := range data.Issue.Comments.Nodes {
		author := "unknown"
		if c.User != nil {
			author = c.User.DisplayName
		}
		iss.Comments = append(iss.Comments, Comment{Author: author, Body: c.Body})
	}
	return iss, nil
}
