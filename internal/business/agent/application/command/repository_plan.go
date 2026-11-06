package command

import (
	"errors"
	"fmt"
	"hash/fnv"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

const repositoryTargetRoot = "/workspace/repos"

// PlanCheckouts derives workspace locations from the Work's retained choices.
// The access reference identifies this receipt's checkout; it is not a personal
// authorization, credential, or independently managed grant.
func (r *InitialExecutionRequest) PlanCheckouts(work *domain.AgentWork) error {
	if err := r.AdmitRepositoryPreparation(work); err != nil {
		return err
	}
	selection := work.RepositorySelection
	if len(r.Checkouts) > 0 && len(r.Checkouts) != len(selection.Repositories) {
		return ErrInitialExecutionConflict
	}
	if len(selection.Repositories) == 0 {
		return nil
	}
	if strings.TrimSpace(r.WorkBranch) == "" || r.WorkID == "" || r.RequestID == "" {
		return domain.ErrInvalidWork
	}
	snapshots := make([]RepositoryCheckout, 0, len(selection.Repositories))
	for n, repo := range selection.Repositories {
		snapshot := RepositoryCheckout{Provider: repo.Provider, RepoID: repo.RepoID, RepoName: repo.RepoName,
			CloneURL: repo.CloneURL, WebURL: repo.WebURL, DefaultBranch: repo.DefaultBranch, BaseBranch: repo.BaseBranch,
			WorkBranch: strings.TrimSpace(r.WorkBranch)}
		if len(r.Checkouts) > 0 {
			retained := r.Checkouts[n]
			snapshot.TargetPath, snapshot.AccessID = retained.TargetPath, retained.AccessID
			if snapshot != retained || retained.TargetPath == "" || retained.AccessID == "" {
				return ErrInitialExecutionConflict
			}
		} else {
			id, err := uuid.NewV7()
			if err != nil {
				return err
			}
			snapshot.AccessID = id.String()
			snapshot.TargetPath = repositoryTargetPath(repo.RepoName)
		}
		snapshots = append(snapshots, snapshot)
	}
	if len(r.Checkouts) == 0 {
		allocateRepositoryTargets(snapshots)
	}
	r.Checkouts = snapshots
	return nil
}

// AdmitRepositoryPreparation maps durable request facts into the Work's decision.
func (r InitialExecutionRequest) AdmitRepositoryPreparation(work *domain.AgentWork) error {
	facts := domain.RepositoryPreparation{OwnerUserID: r.SubjectID, Canceled: r.PreparationCanceled || r.CleanupConfirmed}
	for _, choice := range r.Selections {
		facts.Choices = append(facts.Choices, domain.WorkRepositoryChoice{RepositoryID: choice.RepositoryID, BaseBranch: choice.BaseBranch})
	}
	err := work.AdmitRepositoryPreparation(facts)
	switch {
	case errors.Is(err, domain.ErrRepositoryPreparationCanceled):
		return errors.Join(ErrInitialExecutionRejected, err)
	case errors.Is(err, domain.ErrRepositoryPreparationConflict):
		return errors.Join(ErrInitialExecutionConflict, err)
	default:
		return err
	}
}

func allocateRepositoryTargets(snapshots []RepositoryCheckout) {
	plans := make([]checkoutTargetPlan, 0, len(snapshots))
	byTargetPath := map[string][]int{}
	for n, snapshot := range snapshots {
		byTargetPath[snapshot.TargetPath] = append(byTargetPath[snapshot.TargetPath], n)
		plans = append(plans, checkoutTargetPlan{index: n, initialTargetPath: snapshot.TargetPath})
	}
	sort.Slice(plans, func(i, j int) bool {
		left, right := snapshots[plans[i].index], snapshots[plans[j].index]
		if left.RepoName != right.RepoName {
			return left.RepoName < right.RepoName
		}
		return left.RepoID < right.RepoID
	})
	used := map[string]struct{}{}
	for _, plan := range plans {
		snapshot := snapshots[plan.index]
		targetPath := plan.initialTargetPath
		if len(byTargetPath[plan.initialTargetPath]) > 1 {
			targetPath = repositoryConflictTargetPath(snapshot.RepoName)
		}
		snapshots[plan.index].TargetPath = uniqueRepositoryTargetPath(targetPath, snapshot, used)
		used[snapshots[plan.index].TargetPath] = struct{}{}
	}
}

type checkoutTargetPlan struct {
	index             int
	initialTargetPath string
}

func repositoryTargetPath(repoName string) string {
	repoName = strings.TrimSuffix(strings.TrimSpace(path.Base(repoName)), ".git")
	if repoName == "." || repoName == "/" {
		repoName = ""
	}
	if repoName == "" {
		return repositoryTargetRoot
	}
	return path.Join(repositoryTargetRoot, repoName)
}

func repositoryConflictTargetPath(repoName string) string {
	repoName = strings.TrimSuffix(strings.TrimSpace(repoName), ".git")
	parts := strings.Split(repoName, "/")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." || part == "/" {
			continue
		}
		clean = append(clean, part)
	}
	if len(clean) == 0 {
		return repositoryTargetRoot
	}
	return path.Join(repositoryTargetRoot, strings.Join(clean, "-"))
}

func uniqueRepositoryTargetPath(targetPath string, snapshot RepositoryCheckout, used map[string]struct{}) string {
	if _, exists := used[targetPath]; !exists {
		return targetPath
	}
	suffix := repositoryTargetSuffix(snapshot)
	candidate := targetPath + "-" + suffix
	if _, exists := used[candidate]; !exists {
		return candidate
	}
	for n := 2; ; n++ {
		candidate = targetPath + "-" + suffix + "-" + strconv.Itoa(n)
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
}

func repositoryTargetSuffix(snapshot RepositoryCheckout) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(snapshot.RepoName))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(snapshot.RepoID))
	return fmt.Sprintf("%08x", h.Sum32())
}
