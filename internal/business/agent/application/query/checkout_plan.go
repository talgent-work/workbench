package query

import (
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

// checkoutAccessNamespace derives each selected repository's access identity
// from its Work, so every start of the Work names the same one.
var checkoutAccessNamespace = uuid.MustParse("4f8f8e7c-4d0a-4d36-9b8e-6a3e0c1f2b57")

// PlanCheckouts derives where each selected repository is checked out in the
// Work's workspace, from its retained selection alone, so every start plans
// the same targets. The access identity names the repository's temporary
// access in the Work's environment; it is not a personal authorization,
// credential or independently managed grant.
func PlanCheckouts(workID string, selection domain.WorkRepositorySelection) []PreparedCheckout {
	if len(selection.Repositories) == 0 {
		return nil
	}
	plan := make([]PreparedCheckout, 0, len(selection.Repositories))
	for _, repo := range selection.Repositories {
		plan = append(plan, PreparedCheckout{Provider: repo.Provider, RepoID: repo.RepoID, RepoName: repo.RepoName,
			CloneURL: repo.CloneURL, WebURL: repo.WebURL, DefaultBranch: repo.DefaultBranch, BaseBranch: repo.BaseBranch,
			WorkBranch: selection.WorkBranch, TargetPath: repositoryTargetPath(repo.RepoName),
			AccessID: uuid.NewSHA1(checkoutAccessNamespace, []byte(workID+"\x00"+repo.RepoID)).String()})
	}
	allocateRepositoryTargets(plan)
	return plan
}

// allocateRepositoryTargets gives repositories whose names collide distinct
// targets, independently of selection order.
func allocateRepositoryTargets(plan []PreparedCheckout) {
	byTargetPath := map[string]int{}
	order := make([]int, len(plan))
	for n, checkout := range plan {
		byTargetPath[checkout.TargetPath]++
		order[n] = n
	}
	sort.Slice(order, func(i, j int) bool {
		left, right := plan[order[i]], plan[order[j]]
		if left.RepoName != right.RepoName {
			return left.RepoName < right.RepoName
		}
		return left.RepoID < right.RepoID
	})
	initial := make([]string, len(plan))
	for n, checkout := range plan {
		initial[n] = checkout.TargetPath
	}
	used := map[string]struct{}{}
	for _, n := range order {
		targetPath := initial[n]
		if byTargetPath[targetPath] > 1 {
			targetPath = repositoryConflictTargetPath(plan[n].RepoName)
		}
		plan[n].TargetPath = uniqueRepositoryTargetPath(targetPath, plan[n], used)
		used[plan[n].TargetPath] = struct{}{}
	}
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

func uniqueRepositoryTargetPath(targetPath string, checkout PreparedCheckout, used map[string]struct{}) string {
	if _, exists := used[targetPath]; !exists {
		return targetPath
	}
	suffix := repositoryTargetSuffix(checkout)
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

func repositoryTargetSuffix(checkout PreparedCheckout) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(checkout.RepoName))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(checkout.RepoID))
	return fmt.Sprintf("%08x", h.Sum32())
}
