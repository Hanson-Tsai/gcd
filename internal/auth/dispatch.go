package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ErrWorkflowNotFound: the repo has no such workflow on the dispatched
// branch (or Actions is disabled for it).
var ErrWorkflowNotFound = errors.New("workflow not found")

// DispatchWorkflow starts workflow file (e.g. "gcd-compact.yml", which must
// have a workflow_dispatch trigger) on branch ref of r, and returns the web
// page to follow it: the new run's page, or the workflow's run list when
// GitHub does not report the run (older API behaviour). Needs the token's
// Actions write permission.
func (g GitHub) DispatchWorkflow(ctx context.Context, r Repo, token, file, ref string) (string, error) {
	path := "/repos/" + r.String() + "/actions/workflows/" + url.PathEscape(file) + "/dispatches"
	body := map[string]any{"ref": ref, "return_run_details": true}
	resp, err := g.api(ctx, http.MethodPost, path, token, body)
	if err != nil {
		return "", err
	}
	if (resp.status == http.StatusBadRequest || resp.status == http.StatusUnprocessableEntity) &&
		strings.Contains(string(resp.body), "return_run_details") {
		// A GitHub (Enterprise Server) that predates run details.
		delete(body, "return_run_details")
		if resp, err = g.api(ctx, http.MethodPost, path, token, body); err != nil {
			return "", err
		}
	}
	workflowPage := g.webURL() + "/" + r.String() + "/actions/workflows/" + url.PathEscape(file)
	switch resp.status {
	case http.StatusOK:
		var run struct {
			HTMLURL string `json:"html_url"`
		}
		if json.Unmarshal(resp.body, &run) == nil && run.HTMLURL != "" {
			return run.HTMLURL, nil
		}
		return workflowPage, nil
	case http.StatusNoContent:
		return workflowPage, nil
	case http.StatusUnauthorized:
		return "", tokenInvalid(r)
	case http.StatusForbidden:
		if resp.rateLimited() {
			return "", resp.err("POST " + path)
		}
		return "", &MissingPermissionsError{Repo: r, Missing: []string{PermActions}}
	case http.StatusNotFound:
		return "", fmt.Errorf("%w: %s has no workflow %s on branch %s, or Actions is disabled for it "+
			"(or the token cannot see the repo)", ErrWorkflowNotFound, r, file, ref)
	}
	return "", resp.err("POST " + path)
}
