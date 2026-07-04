package provider

import (
	"fmt"
	"strings"

	"github.com/JollyGrin/grove/internal/linear"
)

// Linear adapts the existing read-only GraphQL client (internal/linear,
// byte-comparable with ovs upstream) to the Provider interface.
type Linear struct {
	apiKey string
}

func NewLinear(apiKey string) *Linear { return &Linear{apiKey: apiKey} }

func (l *Linear) Kind() string { return "linear" }

func (l *Linear) ParseID(raw string) (string, error) {
	return linear.ParseIdentifier(strings.ToUpper(raw))
}

func (l *Linear) Get(id string) (*Task, error) {
	iss, err := linear.FetchIssue(l.apiKey, id)
	if err != nil {
		return nil, err
	}
	t := &Task{
		ID:          iss.Identifier,
		Title:       iss.Title,
		Description: iss.Description,
		URL:         iss.URL,
		Labels:      iss.Labels,
	}
	for _, c := range iss.Comments {
		t.Comments = append(t.Comments, Comment{Author: c.Author, Body: c.Body})
	}
	return t, nil
}

func (l *Linear) List() ([]*Task, error) {
	return nil, fmt.Errorf("the linear provider cannot list yet — pass a ticket id: gv grab <id>")
}

func (l *Linear) Verbs() Verbs {
	return Verbs{
		Start:  `Move the ticket to "In Progress" using the dev-linear Linear tools.`,
		Review: `move the ticket to "In Review". Do NOT post Linear comments and do NOT move the ticket to Done.`,
	}
}

func (l *Linear) Capabilities() Capabilities { return Capabilities{CanList: false} }
