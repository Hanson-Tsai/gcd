package disk

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/Hanson-Tsai/gcd/internal/workflow"
)

// WorkflowResult describes the compaction workflow (workflow.Path) after
// InstallWorkflow or UpgradeWorkflow.
type WorkflowResult struct {
	Changed bool // a commit writing the workflow was pushed
	Existed bool // the repo had a workflow file before
	// Previous is what the existing workflow pinned; zero when there was
	// none or it was not written by gcd.
	Previous workflow.Spec
	// Spec is what the repo's workflow pins now (zero when it is a workflow
	// gcd cannot read and was left alone).
	Spec workflow.Spec
}

// InstallWorkflow commits the compaction workflow, pinned to pin and
// scheduled by the repo config's compact_schedule, when the repo has none
// (as `gcd init` does). An existing workflow is left alone, so a machine
// running an older gcd never downgrades it; UpgradeWorkflow re-pins.
//
// The push needs the token's Workflows permission; GitHub refuses it with
// gitstore.ErrWorkflowsPermission otherwise.
func (d *Disk) InstallWorkflow(ctx context.Context, pin workflow.Pin) (WorkflowResult, error) {
	return d.writeWorkflow(ctx, pin, false)
}

// UpgradeWorkflow rewrites the compaction workflow to pin (and the current
// compact_schedule), replacing whatever workflow file is there. It commits
// nothing when the workflow is already exactly that.
func (d *Disk) UpgradeWorkflow(ctx context.Context, pin workflow.Pin) (WorkflowResult, error) {
	return d.writeWorkflow(ctx, pin, true)
}

func (d *Disk) writeWorkflow(ctx context.Context, pin workflow.Pin, replace bool) (WorkflowResult, error) {
	for attempt := 0; ; attempt++ {
		head, tree, err := d.fetchTree(ctx)
		if err != nil {
			return WorkflowResult{}, err
		}
		cfg, err := d.repoConfig(ctx, tree) // refuses newer formats and invalid configs
		if err != nil {
			return WorkflowResult{}, err
		}
		old, err := d.readBlobAt(ctx, tree, workflow.Path)
		if err != nil {
			return WorkflowResult{}, err
		}
		var res WorkflowResult
		if old != nil {
			res.Existed = true
			res.Previous, _ = workflow.Parse(old) // zero when not gcd's
			if !replace {
				res.Spec = res.Previous
				return res, nil
			}
		}
		res.Spec = workflow.Spec{Pin: pin, Schedule: cfg.CompactSchedule}
		raw, err := workflow.Render(res.Spec)
		if err != nil {
			return WorkflowResult{}, err
		}
		if bytes.Equal(raw, old) {
			return res, nil
		}
		msg := fmt.Sprintf("install compaction workflow (gcd %s) from %s", pin.Version, d.host)
		if res.Existed {
			msg = fmt.Sprintf("upgrade compaction workflow to gcd %s from %s", pin.Version, d.host)
		}
		_, err = d.commitFile(ctx, head, workflow.Path, raw, msg)
		if errors.Is(err, ErrRemoteChanged) && attempt < 2 {
			continue // another machine pushed first: look again
		}
		res.Changed = err == nil
		return res, err
	}
}

// rescheduledWorkflow returns the workflow at head re-rendered with
// schedule, or nil when it needs no change: no workflow, one gcd did not
// write (`gcd workflow upgrade` replaces it), or the same schedule.
func (d *Disk) rescheduledWorkflow(ctx context.Context, head, schedule string) ([]byte, error) {
	if head == "" {
		return nil, nil
	}
	treeHash, err := d.store.TreeOf(ctx, head)
	if err != nil {
		return nil, err
	}
	tree, err := d.store.ReadTree(ctx, treeHash)
	if err != nil {
		return nil, err
	}
	old, err := d.readBlobAt(ctx, tree, workflow.Path)
	if err != nil || old == nil {
		return nil, err
	}
	spec, err := workflow.Parse(old)
	if err != nil || spec.Schedule == schedule {
		return nil, nil
	}
	spec.Schedule = schedule
	return workflow.Render(spec)
}
