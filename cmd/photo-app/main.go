package main

import (
	"context"
	"os"

	"photo-browser/internal/app"
)

func main() {
	os.Exit(app.Main(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
