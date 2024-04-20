ENV := $(shell cat ../../.last_used_env || echo "not-set")
-include ../../.env.${ENV}

init:
	packer init -upgrade .

build:
	packer build -var "aws_region=$(AWS_REGION)" -var "aws_access_key=$(AWS_ACCESS_KEY)" -var "aws_secret_key=$(AWS_SECRET_KEY)" .

format:
	packer fmt .
