// Package main demonstrates basic usage of the config-sdk.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	configsdk "github.com/holdemlab/config-sdk"
)

// Config is an example application configuration structure.
type Config struct {
	Database struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		User     string `json:"user"`
		Password string `json:"password"`
		Name     string `json:"name"`
	} `json:"database"`

	Server struct {
		Port         int    `json:"port"`
		ReadTimeout  string `json:"read_timeout"`
		WriteTimeout string `json:"write_timeout"`
	} `json:"server"`
}

func main() {
	client, err := configsdk.New(configsdk.Options{
		Host:          os.Getenv("CONFIG_SERVICE_HOST"),
		ServiceToken:  os.Getenv("CONFIG_SERVICE_TOKEN"),
		EncryptionKey: os.Getenv("CONFIG_SERVICE_KEY"),
	})
	if err != nil {
		log.Fatalf("config client: %v", err)
	}
	defer func() { _ = client.Close() }()

	var cfg Config
	if err := client.Get(context.Background(), "my-service", &cfg); err != nil {
		log.Fatalf("get config: %v", err)
	}

	fmt.Printf("DB: %s:%d/%s\n", cfg.Database.Host, cfg.Database.Port, cfg.Database.Name)
	fmt.Printf("Server port: %d\n", cfg.Server.Port)
}
