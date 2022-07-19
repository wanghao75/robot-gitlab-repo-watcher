package main

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/opensourceways/robot-gitlab-repo-watcher/community"
)

func (bot *robot) handleBranch(
	expectRepo expectRepoInfo,
	localBranches []community.RepoBranch,
	log *logrus.Entry,
) []community.RepoBranch {
	org := expectRepo.org
	repo := expectRepo.getNewRepoName()

	// get pid
	gid, err := bot.getGroupID(org)
	if err != nil {
		return nil
	}
	pid, _, err := bot.getProjectID(gid, repo)
	if err != nil {
		return nil
	}

	if len(localBranches) == 0 {
		v, err := bot.listAllBranchOfRepo(pid)
		if err != nil {
			log.Errorf("handle branch and list all branch of repo:%s, err:%s", repo, err.Error())
			return nil
		}
		localBranches = v
	}

	bsExpect := genBranchSets(expectRepo.expectRepoState.Branches)
	bsLocal := genBranchSets(localBranches)
	newState := []community.RepoBranch{}

	// update
	if v := bsExpect.intersectionByName(&bsLocal); len(v) > 0 {
		for name := range v {
			eb := bsExpect.get(name)
			lb := bsLocal.get(name)
			if eb.Type != lb.Type {
				if eb.Type == "readonly" {
					newState = append(newState, *eb)
					continue
				}
				l := log.WithField("update branch", fmt.Sprintf("%s/%s", repo, name))
				l.Info("start")

				err := bot.updateBranch(
					name, pid, eb.Type == community.BranchProtected,
				)
				if err == nil {
					newState = append(newState, *eb)
					continue
				} else {
					l.WithField("type", eb.Type).Error(err)
				}
			}
			newState = append(newState, *lb)
		}
	}

	// add new
	if v := bsExpect.differenceByName(&bsLocal); len(v) > 0 {
		for _, item := range v {
			if b, ok := bot.createBranch(repo, pid, item, log); ok {
				newState = append(newState, b)
			}
		}
	}

	return newState
}

func (bot *robot) createBranch(
	repo string,
	projectID int,
	branch community.RepoBranch,
	log *logrus.Entry,
) (community.RepoBranch, bool) {
	ref := branch.CreateFrom
	if ref == "" {
		// ref must be passed according to the gitlab api and the default value is "master"
		ref = community.BranchMaster
	}

	log = log.WithField("create branch", fmt.Sprintf("%s/%s", repo, branch.Name))
	log.Info("start")

	err := bot.cli.CreateBranch(projectID, branch.Name, ref)
	if err != nil {
		if bs, err1 := bot.cli.GetProjectAllBranches(projectID); err1 != nil {
			flag := false
			for _, b := range bs {
				if b.Name != branch.Name {
					continue
				}
				flag = true
			}
			if flag {
				log.WithField("CreateFrom", ref).Error(err)
				return community.RepoBranch{}, false
			}
			log.WithField("CreateFrom", ref).Error(err)
			log.WithField("Reason", fmt.Sprintf("cannot find ref %s", branch.Name))
			return community.RepoBranch{}, false
		}
	}

	if branch.Type == community.BranchProtected {
		if err := bot.cli.SetProtectionBranch(projectID, branch.Name); err != nil {
			log.Errorf("set the branch to be protected, err:%s", err.Error())

			return community.RepoBranch{
				Name:       branch.Name,
				CreateFrom: ref,
			}, true
		}
	}

	return branch, true
}

func (bot *robot) updateBranch(branch string, projectID int, protected bool) error {
	if protected {
		return bot.cli.SetProtectionBranch(projectID, branch)
	}
	return bot.cli.UnProtectBranch(projectID, branch)
}

func (bot *robot) listAllBranchOfRepo(projectID int) ([]community.RepoBranch, error) {
	items, err := bot.cli.GetProjectAllBranches(projectID)
	if err != nil {
		return nil, err
	}

	v := make([]community.RepoBranch, len(items))

	for i := range items {
		item := items[i]

		v[i] = community.RepoBranch{Name: item.Name}
		if item.Protected {
			v[i].Type = community.BranchProtected
		}
	}
	return v, nil
}

type branchSets struct {
	b     []community.RepoBranch
	s     sets.String
	index map[string]int
}

func (bs *branchSets) intersectionByName(bs1 *branchSets) sets.String {
	return bs.s.Intersection(bs1.s)
}

func (bs *branchSets) differenceByName(bs1 *branchSets) []community.RepoBranch {
	v := bs.s.Difference(bs1.s)
	n := v.Len()
	if n == 0 {
		return nil
	}

	r := make([]community.RepoBranch, n)
	i := 0
	for k := range v {
		r[i] = *bs.get(k)
		i++
	}
	return r
}

func (bs *branchSets) get(name string) *community.RepoBranch {
	if i, ok := bs.index[name]; ok {
		return &bs.b[i]
	}

	return nil
}

func genBranchSets(b []community.RepoBranch) branchSets {
	index := map[string]int{}
	s := make([]string, len(b))
	for i := range b {
		name := b[i].Name
		index[name] = i
		s[i] = name
	}

	return branchSets{b, sets.NewString(s...), index}
}
