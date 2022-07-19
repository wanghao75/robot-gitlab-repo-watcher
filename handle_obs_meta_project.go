package main

import (
	"encoding/base64"
	"fmt"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	"path"
	"sync"
	"time"
)

type yamlStruct struct {
	Packages []PackageInfo `json:"packages,omitempty"`
}

type PackageInfo struct {
	Name    string `json:"name,omitempty"`
	ObsFrom string `json:"obs_from,omitempty"`
	ObsTo   string `json:"obs_to,omitempty"`
	Date    string `json:"date,omitempty"`
}

var m sync.Mutex

func (bot *robot) createOBSMetaProject(repo string, log *logrus.Entry) {
	if !bot.cfg.EnableCreatingOBSMetaProject {
		return
	}

	m.Lock()
	defer m.Unlock()
	var y yamlStruct

	project := &bot.cfg.OBSMetaProject
	readingPath := path.Join(project.ProjectDir, project.ProjectFileName)
	b := &project.Branch

	// get pid by org and repo field
	gid, err := bot.getGroupID(b.Org)
	if err != nil {
		return
	}

	pid, _, err := bot.getProjectID(gid, b.Repo)
	if err != nil {
		return
	}

	f, err := bot.cli.GetPathContent(pid, readingPath, b.Branch)
	if err != nil {
		return
	}

	w := &bot.cfg.WatchingFiles
	msg := fmt.Sprintf(
		"add project according to the file: %s/%s/%s:%s",
		w.Org, w.Repo, w.Branch, w.RepoOrg,
	)

	c, err := base64.StdEncoding.DecodeString(f.Content)
	if err != nil {
		return
	}

	if err = yaml.Unmarshal(c, &y); err != nil {
		return
	}

	var p PackageInfo
	p.Name = repo
	p.ObsTo = "openEuler:Factory"
	year, month, day := time.Now().Format("2006"), time.Now().Format("01"), time.Now().Format("02")
	p.Date = fmt.Sprintf("%s-%s-%s", year, month, day)
	y.Packages = append(y.Packages, p)

	by, err := yaml.Marshal(&y)
	if err != nil {
		return
	}

	// pathContent := base64.StdEncoding.EncodeToString(by)
	pathContent := string(by)

	err = bot.cli.PatchFile(pid, readingPath, pathContent, b.Branch, msg)
	if err != nil {
		log.Errorf("update file failed %v", err)
		return
	}
}
