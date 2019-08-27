package config

import (
	"io/ioutil"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
)

var c Config

type AlarmName string

type Config struct {
	Debug bool `yaml:"debug"`

	AWS struct {
		Region string `yaml:"region"`
	} `yaml:"aws"`

	Log struct {
		RangeDuration struct {
			Before *int64 `yaml:"before"`
			After  *int64 `yaml:"after"`
		} `yaml:"range_duration"`
	} `yaml:"log"`

	Slack  SlackConfig         `yaml:"slack"`
	Alarms map[AlarmName]Alarm `yaml:"alarms"`
}

type SlackConfig struct {
	ApiToken        string `yaml:"api_token"`
	Username        string `yaml:"username"`
	Channel         string `yaml:"channel"`
	AttachmentColor string `yaml:"attachment_color"`
	IconURL         string `yaml:"icon_url"`
}

func (c *SlackConfig) Merge(sc SlackConfig) {
	if sc.ApiToken != "" {
		c.ApiToken = sc.ApiToken
	}
	if sc.AttachmentColor != "" {
		c.AttachmentColor = sc.AttachmentColor
	}
	if sc.Channel != "" {
		c.Channel = sc.Channel
	}
	if sc.IconURL != "" {
		c.IconURL = sc.IconURL
	}
	if sc.Username != "" {
		c.Username = sc.Username
	}
}

type Alarm struct {
	SqsURL string      `yaml:"sqs_url"`
	Slack  SlackConfig `yaml:"slack"`
	Groups []struct {
		Slack                  SlackConfig `yaml:"slack"`
		LogGroups              []string    `yaml:"log_groups"`
		AWSBatchJobDefinitions []string    `yaml:"awsbatch_job_definitions"`
	} `yaml:"groups"`
}

func Load(filename string) error {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return errors.WithStack(err)
	}

	if err := yaml.Unmarshal(data, &c); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func Get() *Config {
	return &c
}
