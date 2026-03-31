// Package configsdk provides a Go client for Config Service.
//
// It handles HTTP transport, AES-256-GCM decryption, JSON deserialization
// into arbitrary Go structs, real-time config updates via SSE, and
// caching with conditional requests (ETag / If-Modified-Since).
//
// Quick start:
//
//	client, err := configsdk.New(configsdk.Options{
//	    Host:          "https://config.example.com",
//	    ServiceToken:  os.Getenv("CONFIG_SERVICE_TOKEN"),
//	    EncryptionKey: os.Getenv("CONFIG_ENCRYPTION_KEY"),
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close()
//
//	var cfg MyConfig
//	if err := client.Get(context.Background(), "my-service", &cfg); err != nil {
//	    log.Fatal(err)
//	}
package configsdk
