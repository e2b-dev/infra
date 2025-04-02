#!/bin/bash

export NODE_ID=$NODE_ID
export CONSUL_TOKEN="${consul_acl_token}"
export OTEL_TRACING_PRINT="${otel_tracing_print}"
export LOGS_COLLECTOR_ADDRESS="${logs_collector_address}"
export LOGS_COLLECTOR_PUBLIC_IP="${logs_collector_public_ip}"
export ENVIRONMENT="${environment}"
export TEMPLATE_BUCKET_NAME="${template_bucket_name}"
export OTEL_COLLECTOR_GRPC_ENDPOINT="${otel_collector_grpc_endpoint}"
export CLICKHOUSE_CONNECTION_STRING="${clickhouse_connection_string}"
export CLICKHOUSE_USERNAME="${clickhouse_username}"
export CLICKHOUSE_PASSWORD="${clickhouse_password}"
export CLICKHOUSE_DATABASE="${clickhouse_database}"

# TODO: We should allow versioning for the orchestrator here or at least make the download more robust/explicit—we are using the fuse mount for envd here.
# We can also validate the checksum here.
# TODO: Also let's copy the binary to local dir to avoid problems.
/fc-envd/orchestrator --port ${port}
