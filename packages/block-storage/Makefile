test:
	go test ./...


start-nbd:
	sudo rm -rf /tmp/nbd.sock
	mkdir -p .test
	sudo dd if=/dev/zero of=.test/test.ext4 bs=4096K count=500
	sudo mkfs.ext4 .test/test.ext4
	sudo go run cmd/mock-nbd-overlay/main.go
