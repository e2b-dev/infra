#!/bin/bash
id=$(uuidgen)
sudo `which go` run ./cmd/build-template -build $id -from-build 7427e758-6c90-4eb0-8b64-6078aac50df3 -storage gs://e2b-test-locality -start-cmd "bun run .sandbox/src/server.ts" -ready-cmd 'ss -tuln | grep :4000'
sudo `which go` run ./cmd/resume-sandbox -build $id -from gs://e2b-test-locality -iterations 2
