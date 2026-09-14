package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RunHealthcheck probes the readiness endpoint and maps it onto a process exit
// code, so the container healthcheck needs no HTTP client in the image.
func RunHealthcheck(ctx context.Context, url string, client *http.Client, stderr io.Writer) int {
	if client == nil {
		client = &http.Client{}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: %v\n", err)
		return 1
	}
	res, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "healthcheck: status %d\n", res.StatusCode)
		return 1
	}
	return 0
}

// healthcheckURL builds the default self-probe URL from HTTP_LISTEN, which is
// typically ":8080" — a bare port with no host.
func healthcheckURL(listen string) string {
	if listen == "" {
		listen = ":8080"
	}
	if listen[0] == ':' {
		listen = "127.0.0.1" + listen
	}
	return "http://" + listen + "/api/v1/health/ready"
}
