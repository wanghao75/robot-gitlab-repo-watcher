package models

import "github.com/opensourceways/robot-gitlab-repo-watcher/community"

var empty = struct{}{}

type RepoProperty struct {
	Private bool
	// CanComment bool
}

type RepoState struct {
	Available bool
	Branches  []community.RepoBranch
	//Members   []string
	Members  map[string]int
	Owner    string
	Property RepoProperty
}

type Repo struct {
	name  string
	state RepoState
	start chan struct{}
}

func NewRepo(repo string, state RepoState) *Repo {
	return &Repo{
		name:  repo,
		state: state,
		start: make(chan struct{}, 1),
	}
}

func (r *Repo) Update(f func(RepoState) RepoState) {
	select {
	case r.start <- empty:
		defer func() {
			<-r.start
		}()

		r.state = f(r.state)
	default:
	}
}
