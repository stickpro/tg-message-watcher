package config

import (
	"github.com/ilyakaznacheev/cleanenv"
	"log"
)

type (
	Config struct {
		TgApp TgAppConfig `yaml:"tg_app"`
	}

	TgAppConfig struct {
		AppId        int    `yaml:"app_id"`
		AppHash      string `yaml:"app_hash"`
		ChatForWatch int64  `yaml:"chat_for_watch"`
		WebhookUrl   string `yaml:"webhook_url"`
	}
)

func Init() (*Config, error) {
	cfg := Config{}

	err := cleanenv.ReadConfig("./config.yml", &cfg)
	if err != nil {
		log.Printf("Error reading environment variables: %v", err)
		return nil, err
	}
	return &cfg, nil
}
