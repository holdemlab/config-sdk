package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	configsdk "github.com/holdemlab/config-sdk"
)

type Config struct {
	Database struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	} `json:"database"`

	Features struct {
		EnableCache   bool `json:"enable_cache"`
		EnableMetrics bool `json:"enable_metrics"`
	} `json:"features"`
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

	// Load initial configuration
	var cfg Config
	if err := client.Get(context.Background(), "my-service", &cfg); err != nil {
		log.Fatalf("get config: %v", err)
	}
	fmt.Printf("Initial config: cache=%v, metrics=%v\n", cfg.Features.EnableCache, cfg.Features.EnableMetrics)

	// Watch for changes — automatically updates cfg
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		if err := client.WatchAndDecode(ctx, "my-service", &cfg); err != nil {
			log.Printf("watch stopped: %v", err)
		}
	}()

	fmt.Println("Watching for config changes... Press Ctrl+C to stop.")
	<-ctx.Done()
	fmt.Println("Shutting down.")
}
