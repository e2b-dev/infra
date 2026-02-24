package main

//go:generate protoc --go_out=../shared/pkg/grpc/orchestrator/ --go_opt=paths=source_relative --go-grpc_out=../shared/pkg/grpc/orchestrator/ --go-grpc_opt=paths=source_relative orchestrator.proto
//go:generate protoc --go_out=../shared/pkg/grpc/orchestrator-info/ --go_opt=paths=source_relative --go-grpc_out=../shared/pkg/grpc/orchestrator-info/ --go-grpc_opt=paths=source_relative info.proto
//go:generate protoc --go_out=../shared/pkg/grpc/template-manager/ --go_opt=paths=source_relative --go-grpc_out=../shared/pkg/grpc/template-manager/ --go-grpc_opt=paths=source_relative template-manager.proto
