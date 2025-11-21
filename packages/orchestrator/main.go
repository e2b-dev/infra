package main

import (
	"log"

	"github.com/e2b-dev/infra/packages/orchestrator/ioc"
)

const version = "0.1.0"

var commitSHA string

func main() {
	config, err := ioc.NewConfig()
	if err != nil {
		log.Fatal(err)
	}

	ioc.New(config, version, commitSHA).Run()
}
