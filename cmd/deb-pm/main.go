package main

import (
	"fmt"
	"log"
	"os"

	"github.com/etnz/apt-repo-builder/manifest"
)

// main is the entry point for the deb-pm CLI tool.
func main() {
	if len(os.Args) < 2 {
		for _, name := range []string{"repository.yml", "repository.yaml", "repository.json"} {
			if _, err := os.Stat(name); err == nil {
				runBuild(name)
				return
			}
		}
		log.Fatal("Usage: deb-pm [Repository file]")
	} else {
		runBuild(os.Args[1])
	}
}

// runBuild executes the 'build' subcommand, which processes a manifest file.
func runBuild(path string) {

	repository, err := manifest.NewRepository(path)
	if err != nil {
		log.Fatalf("Failed to load archivefile: %v", err)
	}

	if err := repository.Compile(); err != nil {
		log.Fatalf("Failed to compile repository: %v", err)
	}

	fmt.Println("Build completed successfully.")
}
