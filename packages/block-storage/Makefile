test:
	go test ./...


start-nbd:
	mkdir -p .test
	sudo dd if=/dev/zero of=.test/test.ext4 bs=4096K count=500
	sudo mkfs.ext4 .test/test.ext4
	sudo go test -run TestStorageOverlayNBD ./... -v