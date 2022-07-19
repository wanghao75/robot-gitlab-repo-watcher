package main

import (
	"github.com/opensourceways/robot-gitlab-repo-watcher/models"
	"strings"
)

type localState struct {
	repos map[string]*models.Repo
}

func (r *localState) getOrNewRepo(repo string) *models.Repo {
	if v, ok := r.repos[repo]; ok {
		return v
	}

	v := models.NewRepo(repo, models.RepoState{})
	r.repos[repo] = v

	return v
}

func (r *localState) clear(isExpectedRepo func(string) bool) {
	for k := range r.repos {
		if !isExpectedRepo(k) {
			delete(r.repos, k)
		}
	}
}

func (bot *robot) loadALLRepos(org string) (*localState, error) {
	gid, err := bot.getGroupID(org)
	if err != nil || gid == 0 {
		return nil, err
	}

	items, err := bot.cli.GetProjects(gid)
	if err != nil {
		return nil, err
	}

	r := localState{
		repos: make(map[string]*models.Repo),
	}

	for i := range items {
		item := items[i]

		var owner string
		if item.Owner == nil {
			owner = ""
		}
		members, _ := bot.getAndToLowerOfMembers(item.ID)
		private := !item.Public
		r.repos[item.Path] = models.NewRepo(item.Path, models.RepoState{
			Available: true,
			Members:   members,
			Property: models.RepoProperty{
				Private: private,
				// CanComment: item.Permissions,
			},
			Owner: owner,
		})
	}

	return &r, nil
}

func (bot *robot) getAndToLowerOfMembers(pid int) (map[string]int, error) {
	members, err := bot.cli.ListCollaborators(pid)
	if err != nil || len(members) == 0 {
		return nil, err
	}

	projectMembers := make(map[string]int, len(members))
	for _, m := range members {
		if m.AccessLevel == 30 {
			projectMembers[strings.ToLower(m.Username)] = m.ID
		}
	}

	return projectMembers, nil
}

func (bot *robot) getGroupID(org string) (int, error) {
	grps, err := bot.cli.GetGroups()
	if err != nil || len(grps) == 0 {
		return 0, err
	}

	gid := 0

	for _, g := range grps {
		if g.Name == org {
			gid = g.ID
		}
	}

	return gid, nil
}

func (bot *robot) getProjectID(gid int, repo string) (int, string, error) {
	prjs, err := bot.cli.GetProjects(gid)
	if err != nil || len(prjs) == 0 {
		return 0, "", err
	}

	pid := 0
	owner := ""

	for _, p := range prjs {
		if p.Name == repo {
			pid = p.ID
		}
	}

	return pid, owner, nil
}
