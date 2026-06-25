// Package storageapi holds the generated gRPC client for the storage index
// service. Only the ingestion RPC is generated here.
package storageapi

//go:generate mise exec -- protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative storageapi.proto
