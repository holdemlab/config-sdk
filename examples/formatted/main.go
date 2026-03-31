// Package main demonstrates GetFormatted usage of the config-sdk.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	configsdk "github.com/holdemlab/config-sdk"
)

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

	ctx := context.Background()

	// Get config as JSON
	jsonData, err := client.GetFormatted(ctx, "my-service", configsdk.FormatJSON)
	if err != nil {
		log.Fatalf("get json: %v", err)
	}
	fmt.Println("=== JSON ===")
	fmt.Println(string(jsonData))

	// Get config as YAML
	yamlData, err := client.GetFormatted(ctx, "my-service", configsdk.FormatYAML)
	if err != nil {
		log.Fatalf("get yaml: %v", err)
	}
	fmt.Println("=== YAML ===")
	fmt.Println(string(yamlData))

	// Get config as ENV
	envData, err := client.GetFormatted(ctx, "my-service", configsdk.FormatEnv)
	if err != nil {
		log.Fatalf("get env: %v", err)
	}
	fmt.Println("=== ENV ===")
	fmt.Println(string(envData))
}
