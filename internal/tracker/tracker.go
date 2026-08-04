// Package tracker creates issues in an external issue tracker.
//
// It is an adapter, not policy: which stories get published, in what order, and
// what happens when one fails is decided in internal/session. What lives here is
// the part that talks to somebody else's API, so that part can be replaced by a
// fake in tests without a network.
//
// Credentials never leave this package. They are read from the environment at
// the moment of use and are never returned, logged, or written into a PRD.
package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Issue is what a tracker was asked to create.
type Issue struct {
	// Title is the user story's title.
	Title string
	// Body is the story description and its acceptance criteria.
	Body string
}

// Created is the issue a tracker made.
type Created struct {
	// Identifier is the human-readable id: DEV-123, or #42.
	Identifier string
	URL        string
}

// Client creates issues in one tracker.
type Client interface {
	// Name is the destination's human name, for messages.
	Name() string
	// Available reports whether this tracker is configured and authenticated for
	// the given repository root, and if not, what is missing.
	Available(ctx context.Context, root string) (bool, string)
	// Create makes one issue.
	Create(ctx context.Context, root string, issue Issue) (Created, error)
}

// requestTimeout bounds a single tracker call. A tracker being slow must not
// wedge publishing indefinitely; the caller retries.
const requestTimeout = 30 * time.Second

// ------------------------------------------------------------------ github ----

// GitHub creates issues with the gh CLI, reusing the authentication Loop
// already depends on for pull requests rather than asking for a second token.
type GitHub struct{}

func (GitHub) Name() string { return "GitHub Issues" }

func (GitHub) Available(ctx context.Context, root string) (bool, string) {
	if _, err := exec.LookPath("gh"); err != nil {
		return false, "the GitHub CLI (gh) is not installed"
	}
	cmd := exec.CommandContext(ctx, "gh", "auth", "status")
	cmd.Dir = root
	if err := cmd.Run(); err != nil {
		return false, "the GitHub CLI is not authenticated; run `gh auth login`"
	}
	cmd = exec.CommandContext(ctx, "gh", "repo", "view", "--json", "name")
	cmd.Dir = root
	if err := cmd.Run(); err != nil {
		return false, "this project has no GitHub repository configured"
	}
	return true, ""
}

func (GitHub) Create(ctx context.Context, root string, issue Issue) (Created, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	// Title and body are discrete arguments; nothing is interpolated into a
	// shell string, so a story title containing quotes or backticks is inert.
	cmd := exec.CommandContext(ctx, "gh", "issue", "create",
		"--title", issue.Title, "--body", issue.Body)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return Created{}, fmt.Errorf("gh issue create: %w", err)
	}

	url := strings.TrimSpace(string(out))
	if url == "" {
		return Created{}, fmt.Errorf("gh issue create returned no issue URL")
	}
	return Created{Identifier: identifierFromURL(url), URL: url}, nil
}

// identifierFromURL turns .../issues/42 into "#42", falling back to the URL so
// a changed output format degrades to something still useful rather than empty.
func identifierFromURL(url string) string {
	at := strings.LastIndex(url, "/")
	if at < 0 || at == len(url)-1 {
		return url
	}
	return "#" + url[at+1:]
}

// ------------------------------------------------------------------ linear ----

// linearAPIKeyEnv is where Linear's personal API key is read from. It stays in
// the environment rather than in .chief/config.yaml so a credential is never
// written into a file that a project might commit.
const linearAPIKeyEnv = "LINEAR_API_KEY"

// linearTeamEnv names the team new issues are filed under. With more than one
// team and no choice made, publishing refuses rather than guessing.
const linearTeamEnv = "LINEAR_TEAM"

const linearEndpoint = "https://api.linear.app/graphql"

// Linear creates issues through Linear's GraphQL API.
type Linear struct {
	// HTTPClient is injectable so tests do not reach the network.
	HTTPClient *http.Client
	// Endpoint overrides the API URL in tests.
	Endpoint string
}

func (Linear) Name() string { return "Linear" }

func (l Linear) Available(ctx context.Context, root string) (bool, string) {
	if os.Getenv(linearAPIKeyEnv) == "" {
		return false, "set " + linearAPIKeyEnv + " to a Linear personal API key"
	}
	if os.Getenv(linearTeamEnv) == "" {
		return false, "set " + linearTeamEnv + " to the Linear team key issues should be filed under"
	}
	return true, ""
}

func (l Linear) client() *http.Client {
	if l.HTTPClient != nil {
		return l.HTTPClient
	}
	return &http.Client{Timeout: requestTimeout}
}

func (l Linear) endpoint() string {
	if l.Endpoint != "" {
		return l.Endpoint
	}
	return linearEndpoint
}

// linearCreateMutation asks for the fields needed to record the reference:
// the human identifier and the issue's web URL.
const linearCreateMutation = `mutation($teamId:String!,$title:String!,$description:String!){
  issueCreate(input:{teamId:$teamId,title:$title,description:$description}){
    success
    issue { identifier url }
  }
}`

const linearTeamQuery = `query($key:String!){ teams(filter:{key:{eq:$key}}) { nodes { id } } }`

func (l Linear) Create(ctx context.Context, root string, issue Issue) (Created, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	teamID, err := l.teamID(ctx, os.Getenv(linearTeamEnv))
	if err != nil {
		return Created{}, err
	}

	var resp struct {
		IssueCreate struct {
			Success bool `json:"success"`
			Issue   struct {
				Identifier string `json:"identifier"`
				URL        string `json:"url"`
			} `json:"issue"`
		} `json:"issueCreate"`
	}
	err = l.graphql(ctx, linearCreateMutation, map[string]any{
		"teamId":      teamID,
		"title":       issue.Title,
		"description": issue.Body,
	}, &resp)
	if err != nil {
		return Created{}, err
	}
	if !resp.IssueCreate.Success || resp.IssueCreate.Issue.URL == "" {
		return Created{}, fmt.Errorf("Linear refused to create the issue")
	}
	return Created{
		Identifier: resp.IssueCreate.Issue.Identifier,
		URL:        resp.IssueCreate.Issue.URL,
	}, nil
}

// teamID resolves a team key (the short prefix such as DEV) to its internal id.
func (l Linear) teamID(ctx context.Context, key string) (string, error) {
	var resp struct {
		Teams struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		} `json:"teams"`
	}
	if err := l.graphql(ctx, linearTeamQuery, map[string]any{"key": key}, &resp); err != nil {
		return "", err
	}
	if len(resp.Teams.Nodes) == 0 {
		return "", fmt.Errorf("no Linear team with key %q", key)
	}
	return resp.Teams.Nodes[0].ID, nil
}

// graphql performs one GraphQL call and decodes into out.
//
// The API key goes on the request and nowhere else: it is not returned in an
// error, so a failure message can be shown or logged safely.
func (l Linear) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.endpoint(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", os.Getenv(linearAPIKeyEnv))

	resp, err := l.client().Do(req)
	if err != nil {
		return fmt.Errorf("Linear request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Linear returned HTTP %d", resp.StatusCode)
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("Linear returned an unreadable response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("Linear: %s", envelope.Errors[0].Message)
	}
	return json.Unmarshal(envelope.Data, out)
}
