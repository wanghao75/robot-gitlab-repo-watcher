package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/opensourceways/community-robot-lib/gitlabclient"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/opensourceways/community-robot-lib/config"
	"github.com/opensourceways/community-robot-lib/logrusutil"
	liboptions "github.com/opensourceways/community-robot-lib/options"
	"github.com/opensourceways/community-robot-lib/secret"
	"github.com/panjf2000/ants/v2"
	"github.com/sirupsen/logrus"
)

type options struct {
	gitlab     liboptions.GitLabOptions
	configFile string
}

func (o *options) Validate() error {
	return o.gitlab.Validate()
}

func gatherOptions(fs *flag.FlagSet, args ...string) options {
	var o options

	o.gitlab.AddFlags(fs)

	fs.StringVar(&o.configFile, "config-file", "", "Path to config file.")

	fs.Parse(args)
	return o
}

func main() {
	logrusutil.ComponentInit(botName)

	o := gatherOptions(flag.NewFlagSet(os.Args[0], flag.ExitOnError), os.Args[1:]...)
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid options")
	}

	cfg, err := getConfig(o.configFile)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting config.")
	}

	host := "https://source.openeuler.sh/api/v4"
	c, err := genClient(o.gitlab.TokenPath, host)
	if err != nil {
		logrus.WithError(err).Fatal("Error generating client.")
	}

	pool, err := newPool(cfg.ConcurrentSize, logWapper{})
	if err != nil {
		logrus.WithError(err).Fatal("Error starting goroutine pool.")
	}
	defer pool.Release()

	p := newRobot(c, pool, &cfg)

	run(p)
}

func newPool(size int, log ants.Logger) (*ants.Pool, error) {
	return ants.NewPool(size, ants.WithOptions(ants.Options{
		Logger: log,
	}))
}

type logWapper struct{}

func (l logWapper) Printf(format string, args ...interface{}) {
	logrus.Infof(format, args...)
}

func getConfig(configFile string) (botConfig, error) {
	agent := config.NewConfigAgent(func() config.Config {
		return &configuration{}
	})

	if err := agent.Start(configFile); err != nil {
		return botConfig{}, err
	}

	agent.Stop()

	_, v := agent.GetConfig()

	if cfg, ok := v.(*configuration); ok {
		return cfg.Config, nil
	}
	return botConfig{}, fmt.Errorf("can't convert the configuration")
}

func genClient(tokenPath, host string) (iClient, error) {

	secretAgent := new(secret.Agent)
	if err := secretAgent.Start([]string{tokenPath}); err != nil {
		logrus.WithError(err).Fatal("Error starting secret agent.")
		return nil, err
	}

	defer secretAgent.Stop()

	t := secretAgent.GetTokenGenerator(tokenPath)
	return gitlabclient.NewGitlabClient(t, host), nil
}

func run(bot *robot) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	var wg sync.WaitGroup
	defer wg.Wait()

	ctx, done := context.WithCancel(context.Background())
	// it seems that it will be ok even if invoking 'done' twice.
	defer done()

	wg.Add(1)
	go func(ctx context.Context) {
		defer wg.Done()

		select {
		case <-ctx.Done():
			logrus.Info("receive done. exit normally")
			return
		case <-sig:
			logrus.Info("receive exit signal")
			done()
			return
		}
	}(ctx)

	log := logrus.NewEntry(logrus.StandardLogger())

	if err := bot.run(ctx, log); err != nil {
		log.Errorf("start watching, err:%s", err.Error())
	}
}
