package main

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"github.com/xanzy/go-gitlab"

	"github.com/opensourceways/robot-gitlab-repo-watcher/community"
	"github.com/opensourceways/robot-gitlab-repo-watcher/models"
)

func (bot *robot) createRepo(
	expectRepo expectRepoInfo,
	sigLabel string,
	log *logrus.Entry,
	hook func(string, *logrus.Entry),
) models.RepoState {
	org := expectRepo.org
	repo := expectRepo.expectRepoState
	repoName := expectRepo.getNewRepoName()

	if n := repo.RenameFrom; n != "" && n != repoName {
		return bot.renameRepo(expectRepo, sigLabel, log, hook)
	}

	log = log.WithField("create repo", repoName)
	log.Info("start")

	property, project, err := bot.newRepo(org, repo, sigLabel, log)
	if err != nil {
		log.Warning("repo exists already")

		gid, err := bot.getGroupID(org)
		if err != nil {
			return models.RepoState{}
		}

		pid, _, err := bot.getProjectID(gid, repoName)
		if err != nil {
			return models.RepoState{}
		}

		if s, b := bot.getRepoState(pid, log); b {
			s.Branches = bot.handleBranch(expectRepo, s.Branches, log)
			s.Members = bot.handleMember(expectRepo, s.Members, &s.Owner, log)
			return s
		}

		log.Errorf("create repo, err:%s", err.Error())

		return models.RepoState{}
	}

	OneCheckTotalRepos += 1

	defer func() {
		hook(repoName, log)
	}()

	branches, members := bot.initNewlyCreatedRepo(
		org, repoName, project.ID, repo.Branches, expectRepo.expectOwners, log,
	)

	return models.RepoState{
		Available: true,
		Branches:  branches,
		Members:   members,
		Property:  property,
	}
}

func (bot *robot) newRepo(org string, repo *community.Repository, sigLabel string, log *logrus.Entry) (models.RepoProperty, *gitlab.Project, error) {
	var visibility gitlab.VisibilityValue
	if repo.IsPrivate() {
		visibility = "private"
	} else {
		visibility = "public"
	}
	initializeWithReadme := true

	gid, err := bot.getGroupID(org)
	if err != nil {
		return models.RepoProperty{}, nil, err
	}

	p, err := bot.cli.CreateProject(gitlab.CreateProjectOptions{
		Name:                 &repo.Name,
		Path:                 &repo.Name,
		NamespaceID:          &gid,
		Description:          &repo.Description,
		InitializeWithReadme: &initializeWithReadme,
		Visibility:           &visibility,
	})
	if err != nil {
		return models.RepoProperty{}, nil, err
	}

	err = bot.cli.AddProjectLabel(p.ID, sigLabel, "#FFFFFF")
	if err != nil {
		log.Infof("Add project label %s failed, err: %v", sigLabel, err)
	}

	return models.RepoProperty{
		Private: repo.IsPrivate(),
	}, p, nil
}

func (bot *robot) initNewlyCreatedRepo(
	org, repoName string,
	projectID int,
	repoBranches []community.RepoBranch,
	repoOwners []string,
	log *logrus.Entry,
) ([]community.RepoBranch, map[string]int) {
	branches := []community.RepoBranch{
		{Name: community.BranchMaster},
	}
	for _, item := range repoBranches {
		if item.Name == community.BranchMaster {
			if item.Type != community.BranchProtected {
				continue
			}
			branches[0].Type = community.BranchProtected

			//if err := bot.updateBranch(item.Name, projectID, true); err == nil {
			//	branches[0].Type = community.BranchProtected
			//} else {
			//	log.WithFields(logrus.Fields{
			//		"update branch": fmt.Sprintf("%s/%s", repoName, item.Name),
			//		"type":          item.Type,
			//	}).Error(err)
			//}
		} else {
			if b, ok := bot.createBranch(repoName, projectID, item, log); ok {
				branches = append(branches, b)
			}
		}
	}

	members := make(map[string]int, 0)
	for _, item := range repoOwners {
		uid := bot.cli.GetSingleUser(item)
		if err := bot.addRepoMember(projectID, uid); err != nil {
			log.Errorf("add member:%s, err:%s", item, err)
		} else {
			members[item] = uid
		}
	}

	return branches, members
}

func (bot *robot) renameRepo(
	expectRepo expectRepoInfo,
	sigLabel string,
	log *logrus.Entry,
	hook func(string, *logrus.Entry),
) models.RepoState {
	org := expectRepo.org
	oldRepo := expectRepo.expectRepoState.RenameFrom
	newRepo := expectRepo.getNewRepoName()

	// get pid
	gid, err := bot.getGroupID(org)
	if err != nil {
		return models.RepoState{}
	}

	pid, _, err := bot.getProjectID(gid, oldRepo)
	if err != nil {
		return models.RepoState{}
	}

	log = log.WithField("rename repo", fmt.Sprintf("from %s to %s", oldRepo, newRepo))
	log.Info("start")

	err = bot.cli.UpdateProject(
		pid,
		gitlab.EditProjectOptions{
			Name:        &newRepo,
			Path:        &newRepo,
			Description: &expectRepo.expectRepoState.Description,
		},
	)

	err = bot.cli.TransferProjectNameSpace(pid, org)
	if err != nil {
		log.Infof("transfer project failed to %s because project doesnot change the organization", org)
	} else {
		log.Infof("transfer project success to %s ", org)
	}

	if err == nil {
		OneCheckTotalRepos += 1
	}

	lbs, err := bot.cli.GetProjectLabels(pid)
	if err != nil {
		return models.RepoState{}
	}

	err = bot.cli.UpdateProjectLabel(pid, lbs[0].Name, sigLabel, "#FFFFFF")
	if err != nil {
		log.Infof("update label failed: %v", err)
	}

	defer func(b bool) {
		if b {
			hook(newRepo, log)
		}
	}(err == nil)

	// if the err == nil, invoke 'getRepoState' obviously.
	// if the err != nil, it is better to call 'getRepoState' to
	// avoid the case that the repo already exists.
	if s, b := bot.getRepoState(pid, log); b {
		s.Branches = bot.handleBranch(expectRepo, s.Branches, log)
		s.Members = bot.handleMember(expectRepo, s.Members, &s.Owner, log)
		return s
	}

	if err != nil {
		log.Error(err)

		return models.RepoState{}
	}

	return models.RepoState{Available: true}
}

func (bot *robot) getRepoState(projectID int, log *logrus.Entry) (models.RepoState, bool) {
	newRepo, err := bot.cli.GetProject(projectID)
	if err != nil {
		log.Errorf("get repo, err:%s", err.Error())

		return models.RepoState{}, false
	}

	pms := make(map[string]int, 0)
	members, err := bot.getAndToLowerOfMembers(projectID)
	if err != nil || len(members) == 0 {
		pms = members
	}
	pms = members

	var private bool
	if newRepo.Visibility == "private" {
		private = true
	}
	owner := ""

	r := models.RepoState{
		Available: true,
		Members:   pms,
		Property: models.RepoProperty{
			Private: private,
		},
		Owner: owner,
	}

	branches, err := bot.listAllBranchOfRepo(projectID)
	if err != nil {
		log.Errorf("list branch, err:%s", err.Error())
	} else {
		r.Branches = branches
	}

	return r, true
}

func (bot *robot) updateRepo(expectRepo expectRepoInfo, lp models.RepoProperty, log *logrus.Entry) models.RepoProperty {
	org := expectRepo.org
	repo := expectRepo.expectRepoState
	repoName := expectRepo.getNewRepoName()

	// get pid
	gid, err := bot.getGroupID(org)
	if err != nil {
		return models.RepoProperty{}
	}
	pid, _, err := bot.getProjectID(gid, repoName)
	if err != nil {
		return models.RepoProperty{}
	}

	ep := repo.IsPrivate()

	if ep != lp.Private {
		log = log.WithField("update repo", repoName)
		log.Info("start")
		var vis gitlab.VisibilityValue
		if ep {
			vis = "private"
		} else {
			vis = "public"
		}

		err := bot.cli.UpdateProject(
			pid,
			gitlab.EditProjectOptions{Visibility: &vis},
		)
		if err == nil {
			return models.RepoProperty{
				Private: ep,
			}
		}

		log.WithFields(logrus.Fields{
			"Private": ep,
		}).Error(err)
	}
	return lp
}
