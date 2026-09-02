package main

import (
	"fmt"
	"os"

	"photo-browser/internal/config"
)

func main() {
	if _, err := config.Load(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: photo-app <command>")
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
	os.Exit(2)
}
