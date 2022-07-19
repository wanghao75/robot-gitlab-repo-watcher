package main

import (
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
)

func (bot *robot) handleMember(expectRepo expectRepoInfo, localMembers map[string]int, repoOwner *string, log *logrus.Entry) map[string]int {
	org := expectRepo.org
	repo := expectRepo.getNewRepoName()

	// get groupID and projectID
	gid, err := bot.getGroupID(org)
	if err != nil || gid == 0 {
		return nil
	}

	pid, owner, err := bot.getProjectID(gid, repo)
	if err != nil || pid == 0 {
		return nil
	}

	if len(localMembers) == 0 {
		v, err := bot.getAndToLowerOfMembers(pid)
		if err != nil {
			log.Errorf("handle repo members and get repo:%s, err:%s", repo, err.Error())
			return nil
		}

		localMembers = v
		*repoOwner = owner
	}

	localMembersName := make([]string, len(localMembers))
	for k := range localMembers {
		localMembersName = append(localMembersName, k)
	}

	expect := sets.NewString(expectRepo.expectOwners...)
	lm := sets.NewString(localMembersName...)
	r := expect.Intersection(lm).UnsortedList()

	rr := make(map[string]int)
	for _, i := range r {
		rr[i] = localMembers[i]
	}

	// add new
	if v := expect.Difference(lm); v.Len() > 0 {
		for k := range v {
			l := log.WithField("add member", fmt.Sprintf("%s:%s", repo, k))
			l.Info("start")

			// how about adding a member but he/she exits? see the comment of 'addRepoMember'
			userID := bot.cli.GetSingleUser(k)
			if err := bot.addRepoMember(pid, bot.cli.GetSingleUser(k)); err != nil {
				l.Error(err)
			} else {
				rr[k] = userID
			}
		}
	}

	// remove
	if v := lm.Difference(expect); v.Len() > 0 {
		o := *repoOwner

		for k := range v {
			if k == o {
				// Gitlab does not allow to remove the repo owner.
				continue
			}

			l := log.WithField("remove member", fmt.Sprintf("%s:%s", repo, k))
			l.Info("start")

			if err := bot.cli.RemoveProjectMember(pid, localMembers[k]); err != nil {
				l.Error(err)

				rr[k] = localMembers[k]
			}
		}
	}

	return rr
}

// Gitlab api will be successful even if adding a member repeatedly.
func (bot *robot) addRepoMember(pid, addMemberID int) error {
	return bot.cli.AddProjectMember(pid, addMemberID, 30)
}

func (bot *robot) addRepoAdmin(pid, addMemberID int) error {
	return bot.cli.AddProjectMember(pid, addMemberID, 50)
}

func toLowerOfMembers(m []string) []string {
	v := make([]string, len(m))
	for i := range m {
		v[i] = strings.ToLower(m[i])
	}
	return v
}
