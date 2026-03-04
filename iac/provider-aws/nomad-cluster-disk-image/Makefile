ENV := $(shell cat ../../../.last_used_env || echo "not-set")
-include ../../../.env.${ENV}

init:
	packer init -upgrade .

build:
	packer build -var "prefix=$(PREFIX)" -var "aws_region=$(AWS_REGION)" -var "aws_profile=$(AWS_PROFILE)" .

format:
	packer fmt .
