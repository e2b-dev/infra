ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

init:
	packer init -upgrade .

build:
	AWS_ACCESS_KEY_ID=$(AWS_ACCESS_KEY_ID) AWS_SECRET_ACCESS_KEY=$(AWS_SECRET_ACCESS_KEY) packer build -var "aws_region=$(AWS_REGION)" .

format:
	packer fmt .
