package config

import (
	"io/ioutil"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"gopkg.in/yaml.v2"
)

type File struct {
	ALMOperatorConfig Config `yaml:"almOperator"`
}

type Config struct {
	Namespaces []string      `yaml:"namespaces"`
	Interval   time.Duration `yaml:"interval"`
}

func LoadConfig(cfgPath string) (*Config, error) {
	f, err := os.Open(os.ExpandEnv(cfgPath))
	defer f.Close()
	if err != nil {
		return nil, err
	}

	d, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}

	var cfgFile File
	err = yaml.Unmarshal(d, &cfgFile)
	if err != nil {
		return nil, err
	}

	config := &cfgFile.ALMOperatorConfig
	if config.Namespaces == nil {
		config.Namespaces = []string{metav1.NamespaceAll}
	}

	if config.Interval < 0 {
		config.Interval = 15 * time.Minute
	}

	return config, nil
}
