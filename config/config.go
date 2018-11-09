package config

import (
	"io/ioutil"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
)

var c Config

type Config struct {
	Debug bool `yaml:"debug"`

	Log struct {
		Limit         int64 `yaml:"limit"`
		RangeDuration struct {
			Before int64 `yaml:"before"`
			After  int64 `yaml:"after"`
		} `yaml:"range_duration"`
	} `yaml:"log"`

	Slack struct {
		APIToken        string `yaml:"api_token"`
		Username        string `yaml:"username"`
		IconURL         string `yaml:"icon_url"`
		AttachmentColor string `yaml:"attachment_color"`
		DefaultChannel  string `yaml:"default_channel"`
	} `yaml:"slack"`

	AWS struct {
		Region      string `yaml:"region"`
		AlarmSqsURL string `yaml:"alarm_sqs_url"`
		LogGroup    []struct {
			Name         string  `yaml:"name"`
			SlackChannel *string `yaml:"slack_channel"`
		} `yaml:"log_group"`
		AWSBatch []struct {
			JobDefinitionName string  `yaml:"job_definition_name"`
			SlackChannel      *string `yaml:"slack_channel"`
		} `yaml:"awsbatch"`
	} `yaml:"aws"`
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
