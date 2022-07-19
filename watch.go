package main

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/opensourceways/robot-gitlab-repo-watcher/community"
	"github.com/opensourceways/robot-gitlab-repo-watcher/models"
)

type expectRepoInfo struct {
	expectRepoState *community.Repository
	expectOwners    []string
	expectAdmins    []string
	org             string
}

func (e *expectRepoInfo) getNewRepoName() string {
	return e.expectRepoState.Name
}

func (bot *robot) run(ctx context.Context, log *logrus.Entry) error {
	w := &bot.cfg.WatchingFiles
	expect := &expectState{
		w:         w.repoBranch,
		log:       log,
		cli:       bot.cli,
		sigOwners: make(map[string]*expectSigOwners),
		sigInfos:  make(map[string]*expectSigInfos),
	}

	org, err := expect.init(w.RepoOrg, w.SigDir)
	if err != nil {
		return err
	}

	local, err := bot.loadALLRepos(org)
	if err != nil {
		log.Errorf("Load repos of org(%s) failed, err:%s", org, err.Error())
		return err
	}

	bot.watch(ctx, org, local, expect)
	return nil
}

func (bot *robot) watch(ctx context.Context, org string, local *localState, expect *expectState) {
	if interval := bot.cfg.Interval; interval <= 0 {
		for {
			if isCancelled(ctx) {
				break
			}

			bot.checkOnce(ctx, org, local, expect)
		}
	} else {
		t := time.Duration(interval) * time.Minute

		for {
			if isCancelled(ctx) {
				break
			}

			s := time.Now()

			bot.checkOnce(ctx, org, local, expect)

			e := time.Now()
			if v := e.Sub(s); v < t {
				time.Sleep(t - v)
			}
		}
	}

	bot.wg.Wait()
}

func (bot *robot) checkOnce(ctx context.Context, org string, local *localState, expect *expectState) {
	f := func(repo *community.Repository, owners []string, admins []string, sigLabel string, log *logrus.Entry) {
		if repo == nil {
			return
		}
		cpo := make([]string, len(owners))
		if len(owners) > 0 {
			copy(cpo, owners)
		}

		err := bot.execTask(
			local.getOrNewRepo(repo.Name),
			expectRepoInfo{
				org:             org,
				expectOwners:    cpo,
				expectAdmins:    admins,
				expectRepoState: repo,
			},
			sigLabel,
			log,
		)
		if err != nil {
			log.Errorf("submit task of repo:%s, err:%s", repo.Name, err.Error())
		}
	}

	isStopped := func() bool {
		return isCancelled(ctx)
	}

	expect.log.Info("new check")

	expect.check(org, isStopped, local.clear, f)
}

func (bot *robot) execTask(localRepo *models.Repo, expectRepo expectRepoInfo, sigLabel string, log *logrus.Entry) error {
	f := func(before models.RepoState) models.RepoState {
		if !before.Available {
			return bot.createRepo(expectRepo, sigLabel, log, bot.createOBSMetaProject)
		}

		return models.RepoState{
			Available: true,
			Branches:  bot.handleBranch(expectRepo, before.Branches, log),
			Members:   bot.handleMember(expectRepo, before.Members, &before.Owner, log),
			Property:  bot.updateRepo(expectRepo, before.Property, log),
			Owner:     before.Owner,
		}
	}

	bot.wg.Add(1)
	err := bot.pool.Submit(func() {
		defer bot.wg.Done()

		localRepo.Update(f)
	})
	if err != nil {
		bot.wg.Done()
	}
	return err
}

func isCancelled(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
