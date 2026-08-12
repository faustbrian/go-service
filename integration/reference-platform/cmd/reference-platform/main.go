package main

import (
	"net/http"
	"os"
	"time"

	"github.com/faustbrian/golib/pkg/service"
	referenceplatform "github.com/faustbrian/golib/pkg/service/integration/reference-platform"
)

func main() {
	definition, err := referenceplatform.New(referenceplatform.Config{
		BusinessAddress:   environment("LISTEN_ADDRESS", "0.0.0.0:8080"),
		ManagementAddress: environment("MANAGEMENT_ADDRESS", "0.0.0.0:8081"),
		DependencyURL:     os.Getenv("DEPENDENCY_URL"),
		Client:            &http.Client{Timeout: 3 * time.Second},
	})
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(2)
	}
	os.Exit(service.Main(definition))
}

func environment(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
