package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/xanzy/go-gitlab"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	"github.com/opensourceways/robot-gitlab-repo-watcher/community"
)

type watchingFileObject interface {
	Validate() error
}

type watchingFile struct {
	log      *logrus.Entry
	loadFile func(string) (string, string, error)

	file string
	sha  string
	obj  watchingFileObject
}

type getSHAFunc func(string) string

func (w *watchingFile) update(f getSHAFunc, newObject func() watchingFileObject) {
	if sha := f(w.file); sha == "" || sha == w.sha {
		return
	}

	c, sha, err := w.loadFile(w.file)
	if err != nil {
		w.log.Errorf("load file:%s, err:%s", w.file, err.Error())
		return
	}

	v := newObject()

	if err := decodeYamlFile(c, v); err != nil {
		w.log.Errorf("decode file:%s, err:%s", w.file, err.Error())
		return
	}

	if err := v.Validate(); err != nil {
		w.log.Errorf("validate the data of file:%s, err:%s", w.file, err.Error())
	} else {
		w.obj = v
		w.sha = sha
	}
}

type expectRepos struct {
	wf watchingFile
}

func (e *expectRepos) refresh(f getSHAFunc) *community.Repository {
	e.wf.update(f, func() watchingFileObject {
		return new(community.Repository)
	})

	if v, ok := e.wf.obj.(*community.Repository); ok {
		return v
	}
	return nil
}

type expectSigOwners struct {
	wf watchingFile
}

func (e *expectSigOwners) refresh(f getSHAFunc) *community.RepoOwners {
	e.wf.update(f, func() watchingFileObject {
		return new(community.RepoOwners)
	})

	if v, ok := e.wf.obj.(*community.RepoOwners); ok {
		return v
	}
	return nil
}

type expectSigInfos struct {
	wf watchingFile
}

func (e *expectSigInfos) refresh(f getSHAFunc) *community.SigInfos {
	e.wf.update(f, func() watchingFileObject {
		return new(community.SigInfos)
	})

	if v, ok := e.wf.obj.(*community.SigInfos); ok {
		return v
	}
	return nil
}

type expectState struct {
	log       *logrus.Entry
	cli       iClient
	w         repoBranch
	sigDir    string
	projectID int

	tree      []*gitlab.TreeNode
	reposInfo *community.Repos
	repos     map[string]*expectRepos
	sigOwners map[string]*expectSigOwners

	sigInfos map[string]*expectSigInfos
}

func (e *expectState) init(orgPath, sigDir string) (string, error) {
	pid, err := e.getProjectIDByOrgRepo()
	if err != nil || pid == 0 {
		return "", err
	}
	e.projectID = pid

	recursive := true
	opt := gitlab.ListTreeOptions{Path: &sigDir, Ref: &e.w.Branch, Recursive: &recursive}
	trees, err := e.cli.GetDirectoryTree(pid, opt)
	if err != nil || len(trees) == 0 {
		return "", err
	}
	e.tree = trees

	reposInfo := new(community.Repos)
	e.repos = make(map[string]*expectRepos)
	for _, v := range e.tree {
		pathArr := strings.Split(v.Path, "/")
		if pathArr[0] != "sig" || len(pathArr) != 5 || pathArr[2] != orgPath {
			continue
		}

		exRepo := &expectRepos{wf: e.newWatchingFile(v.Path)}
		e.repos[v.Path] = exRepo
		singleRepo := exRepo.refresh(func(string) string {
			return "init"
		})
		reposInfo.Repositories = append(reposInfo.Repositories, *singleRepo)
	}
	reposInfo.Validate()
	e.reposInfo = reposInfo

	org := orgPath
	if org == "" {
		return "", fmt.Errorf("load repository failed")
	}

	e.sigDir = sigDir

	return org, nil
}

func (e *expectState) getProjectIDByOrgRepo() (int, error) {
	grps, err := e.cli.GetGroups()
	if err != nil || len(grps) == 0 {
		return 0, err
	}

	gid := 0

	for _, g := range grps {
		if g.Name == e.w.Org {
			gid = g.ID
		}
	}

	prjs, err := e.cli.GetProjects(gid)
	if err != nil || len(prjs) == 0 {
		return 0, err
	}

	pid := 0
	for _, p := range prjs {
		if p.Name == e.w.Repo {
			pid = p.ID
		}
	}

	return pid, nil
}

// Packages means a slice of repos these are needed to create, example: [{vwh50  openEuler:Factory 2023-03-31}]
// OneCheckTotalRepos means an int number of all repos these are needed to create, example: 1
var Packages []PackageInfo
var OneCheckTotalRepos int

func (e *expectState) check(
	org string,
	isStopped func() bool,
	clearLocal func(func(string) bool),
	checkRepo func(*community.Repository, []string, []string, string, *logrus.Entry),
) {

	// 测试将单次创建仓库对应的单次更新文件，修改为一批次创建仓库对应一次更新文件, 每次轮询check时，进行重置
	Packages = []PackageInfo{}
	OneCheckTotalRepos = 0

	allFiles, allSigs, allSigInfos, err := e.listAllFilesOfRepo(org)
	if err != nil {
		e.log.Errorf("list all file, err:%s", err.Error())

		return
	}

	getSHA := func(p string) string {
		return allFiles[p]
	}

	repoSigsInfo := make(map[string]string)

	for i := range allFiles {
		expState := e.getRepoFile(i)
		singleRepo := expState.refresh(getSHA)

		path := strings.Split(i, ".yaml")[0]
		pathArr := strings.Split(path, "/")
		repoName := pathArr[4]

		//if repofile name is not same with the Name in the file.
		if singleRepo.Name != repoName {
			e.log.Infof("File name(%s) is not same with Repo name(%s) in file.", repoName, singleRepo.Name)
			continue
		}

		repoSigsInfo[repoName] = pathArr[1]

		// refresh e.reposInfo.Repositories which repo's name == repoName
		for i := 0; i < len(e.reposInfo.Repositories); i++ {
			if e.reposInfo.Repositories[i].Name == repoName {
				e.reposInfo.Repositories = append(e.reposInfo.Repositories[:i], e.reposInfo.Repositories[i+1:]...)
				i--
				break
			}
		}

		e.reposInfo.Repositories = append(e.reposInfo.Repositories, *singleRepo)
	}

	for _, key := range e.reposInfo.Repositories {
		hasSameRepo := false
		for i := range allFiles {
			path := strings.Split(i, ".yaml")[0]
			repoName := strings.Split(path, "/")[4]
			if key.Name == repoName {
				hasSameRepo = true
				break
			}
		}
		if hasSameRepo {
			continue
		}
		for i := 0; i < len(e.reposInfo.Repositories); i++ {
			if e.reposInfo.Repositories[i].Name == key.Name {
				e.reposInfo.Repositories = append(e.reposInfo.Repositories[:i], e.reposInfo.Repositories[i+1:]...)
				delete(repoSigsInfo, key.Name)
				break
			}
		}
	}

	e.reposInfo.Validate()
	repoMap := e.reposInfo.GetRepos()

	if len(repoMap) == 0 {
		// keep safe to do this. it is impossible to happen generally.
		e.log.Warning("there are not repos. Impossible!!!")
		return
	}

	clearLocal(func(r string) bool {
		_, ok := repoMap[r]
		return ok
	})
	getSigSHA := func(p string) string {
		return allSigs[p]
	}

	getSigInfoSHA := func(p string) string {
		return allSigInfos[p]
	}

	ownersOfSigs := make(map[string][]string)

	done := sets.NewString()
	for repo := range repoSigsInfo {
		sigName := repoSigsInfo[repo]
		if sigName == "sig-recycle" {
			continue
		}

		if _, ok := allSigs[fmt.Sprintf("sig/%s/OWNERS", sigName)]; !ok {
			delete(e.sigOwners, sigName)
		}

		sigOwner := e.getSigOwner(sigName)
		owners := sigOwner.refresh(getSigSHA)

		// when sig doesn't have a OWNERS file, use sig-info.yaml
		if len(owners.GetOwners()) == 0 {
			rawOwners := make([]string, 0)
			sigInfo := e.getSigInfo(sigName)
			info := sigInfo.refresh(getSigInfoSHA)
			repoAdmin := info.GetRepoAdmin()
			repoOwners := info.GetRepoAdditionalOwners()
			rawOwners = info.GetRepoOwners()
			admins := make([]string, 0)
			additionalOwners := make([]string, 0)

			for k := range repoAdmin {
				if strings.Split(k, "/")[0] == org && strings.Split(k, "/")[1] == repo {
					admins = repoAdmin[k]
				}
			}

			for k := range repoOwners {
				if strings.Split(k, "/")[0] == org && strings.Split(k, "/")[1] == repo {
					additionalOwners = repoOwners[k]
				}
			}

			if isStopped() {
				break
			}

			if org == "openeuler" && repo == "blog" {
				continue
			}

			if len(additionalOwners) > 0 {
				allOwners := append(rawOwners, additionalOwners...)
				ownersOfSigs[sigName] = allOwners
				checkRepo(repoMap[repo], allOwners, admins, sigName, e.log)
			} else {
				ownersOfSigs[sigName] = rawOwners
				checkRepo(repoMap[repo], rawOwners, admins, sigName, e.log)
			}

			done.Insert(repo)
		} else {
			if isStopped() {
				break
			}

			if org == "openeuler" && repo == "blog" {
				continue
			}

			checkRepo(repoMap[repo], owners.GetOwners(), nil, sigName, e.log)

			done.Insert(repo)
		}
	}

	writeToLog(&allFiles, &allSigs, &repoSigsInfo, &repoMap, &ownersOfSigs)

	if len(repoMap) == done.Len() {
		return
	}

	for repo := range repoSigsInfo {
		if isStopped() {
			break
		}

		if !done.Has(repo) {
			sigName := repoSigsInfo[repo]
			if sigName == "sig-recycle" {
				continue
			}

			if org == "openeuler" && repo == "blog" {
				continue
			}

			checkRepo(repoMap[repo], nil, nil, sigName, e.log)
		}
	}
}

func (e *expectState) getSigOwner(sigName string) *expectSigOwners {
	o, ok := e.sigOwners[sigName]
	if !ok {
		o = &expectSigOwners{
			wf: e.newWatchingFile(
				path.Join(e.sigDir, sigName, "OWNERS"),
			),
		}
		e.sigOwners[sigName] = o
	}

	return o
}

func (e *expectState) getRepoFile(repoPath string) *expectRepos {
	o, ok := e.repos[repoPath]
	if !ok {
		o = &expectRepos{
			wf: e.newWatchingFile(repoPath),
		}

		e.repos[repoPath] = o
	}

	return o
}

func (e *expectState) getSigInfo(sigName string) *expectSigInfos {
	o, ok := e.sigInfos[sigName]
	if !ok {
		o = &expectSigInfos{
			wf: e.newWatchingFile(
				path.Join(e.sigDir, sigName, "sig-info.yaml"),
			),
		}

		e.sigInfos[sigName] = o
	}

	return o
}

func (e *expectState) newWatchingFile(p string) watchingFile {
	return watchingFile{
		file:     p,
		log:      e.log,
		loadFile: e.loadFile,
	}
}

func (e *expectState) listAllFilesOfRepo(org string) (map[string]string, map[string]string, map[string]string, error) {
	pid, err := e.getProjectIDByOrgRepo()
	if err != nil || pid == 0 {
		return nil, nil, nil, err
	}

	recursive := true
	opt := gitlab.ListTreeOptions{Path: &e.sigDir, Ref: &e.w.Branch, Recursive: &recursive}
	trees, err := e.cli.GetDirectoryTree(pid, opt)
	if err != nil || len(trees) == 0 {
		return nil, nil, nil, err
	}

	r := make(map[string]string)
	s := make(map[string]string)
	q := make(map[string]string)
	for i := range trees {
		item := trees[i]
		pathArr := strings.Split(item.Path, "/")
		if len(pathArr) == 0 {
			continue
		}
		if pathArr[0] == "sig" && len(pathArr) == 5 && pathArr[2] == org {
			form := strings.Split(pathArr[4], ".yaml")
			if len(form) != 2 || form[1] != "" {
				continue
			}
			r[item.Path] = item.ID
			continue
		}
		if pathArr[0] == "sig" && len(pathArr) == 3 && pathArr[2] == "OWNERS" {
			s[item.Path] = item.ID
			continue
		}

		if pathArr[0] == "sig" && len(pathArr) == 3 && pathArr[2] == "sig-info.yaml" {
			q[item.Path] = item.ID
			continue
		}
	}

	return r, s, q, nil
}

func (e *expectState) loadFile(f string) (string, string, error) {
	c, err := e.cli.GetPathContent(e.projectID, f, e.w.Branch)
	if err != nil {
		return "", "", err
	}

	return c.Content, c.BlobID, nil
}

func decodeYamlFile(content string, v interface{}) error {
	c, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return err
	}

	return yaml.Unmarshal(c, v)
}

func writeToLog(
	allFiles, allSigs, repoSigsInfo *map[string]string,
	repoMap *map[string]*community.Repository,
	ownerOfSigs *map[string][]string,
) {
	logPath := "/home/watcher/Log"
	maxFilesNumber := 20

	_, err := os.Stat(logPath)
	if os.IsNotExist(err) {
		return
	}

	dirs, err := ioutil.ReadDir(logPath)
	if err != nil {
		return
	}

	if len(dirs) >= maxFilesNumber {
		d := dirs[0]
		err := os.Remove(path.Join(logPath, d.Name()))
		if err != nil {
			return
		}
	}

	fileName := fmt.Sprintf("log-%d.log", time.Now().Unix())
	filePath := path.Join(logPath, fileName)

	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	if _, err := f.WriteString(time.Now().String() + "\n"); err != nil {
		return
	}

	if _, err := f.WriteString("allFiles: " + "\n"); err != nil {
		return
	}

	allFilesByte, err := json.Marshal(allFiles)
	if err != nil {
		return
	}
	allFilesString := string(allFilesByte)
	_, err = f.WriteString(allFilesString + "\n")
	if err != nil {
		return
	}

	if _, err := f.WriteString("allSigs: " + "\n"); err != nil {
		return
	}

	allSigsByte, err := json.Marshal(allSigs)
	if err != nil {
		return
	}
	allSigsString := string(allSigsByte)
	_, err = f.WriteString(allSigsString + "\n")
	if err != nil {
		return
	}

	if _, err := f.WriteString("repoSigsInfo: " + "\n"); err != nil {
		return
	}

	repoSigsInfoByte, err := json.Marshal(repoSigsInfo)
	if err != nil {
		return
	}
	repoSigsInfoString := string(repoSigsInfoByte)
	_, err = f.WriteString(repoSigsInfoString + "\n")
	if err != nil {
		return
	}

	if _, err := f.WriteString("repoMap: " + "\n"); err != nil {
		return
	}
	for k, v := range *repoMap {
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		_, err = f.WriteString(k + ": ")
		if err != nil {
			return
		}
		s := string(b)
		_, err = f.WriteString(s + "\n")
		if err != nil {
			return
		}
	}

	if _, err := f.WriteString("ownerOfSigs: " + "\n"); err != nil {
		return
	}
	for k, v := range *ownerOfSigs {
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		_, err = f.WriteString(k + ": ")
		if err != nil {
			return
		}
		s := string(b)
		_, err = f.WriteString(s + "\n")
		if err != nil {
			return
		}
	}
}
