package main

import (
	"github.com/xanzy/go-gitlab"
	"sync"

	"github.com/panjf2000/ants/v2"
)

const botName = "repo-watcher"

type iClient interface {
	GetDirectoryTree(projectID interface{}, opts gitlab.ListTreeOptions) ([]*gitlab.TreeNode, error)
	GetGroups() ([]*gitlab.Group, error)
	GetProjects(gid interface{}) ([]*gitlab.Project, error)
	ListCollaborators(projectID interface{}) ([]*gitlab.ProjectMember, error)
	GetPathContent(projectID interface{}, file, branch string) (*gitlab.File, error)
	AddProjectMember(projectID interface{}, loginID interface{}, accessLevel int) error
	RemoveProjectMember(projectID interface{}, loginID int) error
	GetSingleUser(name string) int
	UpdateProject(projectID interface{}, opts gitlab.EditProjectOptions) error
	UpdateProjectLabel(projectID interface{}, oldLabel, label, color string) error
	GetProjectLabels(projectID interface{}) ([]*gitlab.Label, error)
	GetProject(projectID interface{}) (*gitlab.Project, error)
	CreateProject(opts gitlab.CreateProjectOptions) (*gitlab.Project, error)
	AddProjectLabel(projectID interface{}, label, color string) error
	CreateBranch(projectID interface{}, branch, ref string) error
	GetProjectAllBranches(projectID interface{}) ([]*gitlab.Branch, error)
	SetProtectionBranch(projectID interface{}, branch string) error
	UnProtectBranch(projectID interface{}, branch string) error
	TransferProjectNameSpace(projectID interface{}, newNameSpace string) error
	CreateFile(projectID interface{}, file string, opts gitlab.CreateFileOptions) error
	PatchFile(projectID interface{}, filePath, content, branch, message string) error
}

func newRobot(cli iClient, pool *ants.Pool, cfg *botConfig) *robot {
	return &robot{cli: cli, pool: pool, cfg: cfg}
}

type robot struct {
	pool *ants.Pool
	cfg  *botConfig
	cli  iClient
	wg   sync.WaitGroup
}
