package main

import (
	"github.com/ivysterling59-cyber/compatprobe/internal/app"
	"os"
)

var version = "dev"

func main() { os.Exit(app.App{Version: version}.Run(os.Args[1:])) }
