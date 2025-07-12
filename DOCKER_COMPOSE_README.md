# E2B Infrastructure - Docker Compose Setup

This document explains how to run the E2B infrastructure locally using Docker Compose.

## Overview

The E2B infrastructure consists of several microservices:

- **API**: Main REST API service (port 3000)
- **Client Proxy**: Handles client connections and routing (ports 3001, 3002)
- **Docker Reverse Proxy**: Manages Docker registry operations (port 5000)
- **Orchestrator**: Core service managing sandboxes (ports 5007, 5008) - *Mock service in Docker Compose*
- **Template Manager**: Manages sandbox templates (port 5009) - *Mock service in Docker Compose*
- **PostgreSQL**: Main database
- **ClickHouse**: Analytics database
- **Redis**: Caching and distributed locks
- **Loki**: Log aggregation
- **OpenTelemetry Collector**: Metrics and traces collection

## Prerequisites

1. Docker and Docker Compose installed
2. At least 8GB of available RAM
3. Ports 3000-3002, 5000, 5007-5009, 5432, 6379, 8123, 9000 available

## Quick Start

### 1. Create Environment File

Copy the example environment file:

```bash
cp .env.example .env
```

Edit `.env` and adjust any values as needed. For local development, the default values should work.

### 2. Build and Start Services

Start all services:

```bash
docker-compose up -d
```

To build images fresh:

```bash
docker-compose build --no-cache
docker-compose up -d
```

### 3. Verify Services

Check that all services are healthy:

```bash
docker-compose ps
```

You should see all services in "healthy" state after a minute or two.

### 4. Run Database Migrations

The migrations should run automatically when starting the services. If you need to run them manually:

```bash
# PostgreSQL migrations
docker-compose run --rm db-migrator up

# ClickHouse migrations
docker-compose run --rm clickhouse-migrator up
```

## Service Endpoints

Once running, you can access:

- **API**: http://localhost:3000
- **Client Proxy API**: http://localhost:3001
- **Client Proxy**: http://localhost:3002
- **Docker Registry Proxy**: http://localhost:5000
- **Orchestrator gRPC**: localhost:5008
- **Template Manager gRPC**: localhost:5009
- **PostgreSQL**: localhost:5432
- **Redis**: localhost:6379
- **ClickHouse HTTP**: http://localhost:8123
- **ClickHouse Native**: localhost:9000
- **Loki**: http://localhost:3100
- **OpenTelemetry Collector**: localhost:4317 (gRPC), localhost:4318 (HTTP)

## Common Operations

### View Logs

View logs for all services:
```bash
docker-compose logs -f
```

View logs for specific service:
```bash
docker-compose logs -f api
```

### Stop Services

Stop all services:
```bash
docker-compose down
```

Stop and remove volumes (WARNING: This will delete all data):
```bash
docker-compose down -v
```

### Rebuild Single Service

```bash
docker-compose build api
docker-compose up -d api
```

### Access Service Shell

```bash
docker-compose exec api sh
```

## Environment Variables

Key environment variables (see `.env.example` for full list):

- `ENVIRONMENT`: Set to "local" for development
- `POSTGRES_CONNECTION_STRING`: PostgreSQL connection
- `CLICKHOUSE_CONNECTION_STRING`: ClickHouse connection
- `REDIS_URL`: Redis connection
- `ADMIN_TOKEN`: Admin authentication token
- `EDGE_SECRET`: Client proxy authentication secret

## Troubleshooting

### Services Failing to Start

1. Check logs: `docker-compose logs <service-name>`
2. Ensure all required ports are available
3. Verify environment variables are set correctly

### Database Connection Issues

1. Ensure database services are healthy: `docker-compose ps`
2. Check connection strings in environment variables
3. Verify migrations have run successfully

### Build Failures

1. Clean Docker build cache: `docker system prune -a`
2. Ensure you have sufficient disk space
3. Check for any local modifications to Dockerfiles
4. **Note**: The project uses custom `Dockerfile.dockercompose` files for local development to avoid build issues

### Memory Issues

If services are being killed due to memory:
1. Increase Docker memory limit in Docker Desktop settings
2. Reduce service memory limits in docker-compose.yml
3. Run fewer services simultaneously

### Common Build Errors

If you encounter errors like:
- `fatal: not a git repository`: This is expected in Docker builds
- `gcc: error: unrecognized command-line option '-m64'`: Fixed by using custom Dockerfiles
- `.last_used_env: No such file or directory`: Fixed by creating dummy files in Dockerfiles

These issues have been addressed in the Docker Compose specific Dockerfiles.

## Development Workflow

### Making Code Changes

1. Make your changes in the relevant package directory
2. Rebuild the affected service: `docker-compose build <service>`
3. Restart the service: `docker-compose up -d <service>`

### Adding New Services

1. Create Dockerfile in the service directory
2. Add service definition to docker-compose.yml
3. Add any required environment variables
4. Update this documentation

### Running Tests

Run tests inside containers:
```bash
# API tests
docker-compose exec api go test -v ./...

# Client proxy tests
docker-compose exec client-proxy go test -v ./...
```

## Limitations

**Important**: The Docker Compose setup provides a development environment with some limitations:

1. **Orchestrator and Template Manager**: These are mock services that provide the required endpoints but don't actually create Firecracker VMs
2. **Sandbox Creation**: Actual sandbox functionality requires Linux with KVM support
3. **Full Functionality**: For complete E2B functionality, use the production deployment on Linux servers

This setup is ideal for:
- API development and testing
- Client SDK development
- UI/Frontend development
- Understanding the system architecture

## Production Considerations

This Docker Compose setup is designed for local development. For production:

1. Use proper secrets management (not hardcoded values)
2. Configure proper resource limits
3. Use managed databases (Cloud SQL, etc.)
4. Implement proper backup strategies
5. Use Kubernetes or similar orchestration platform
6. Configure monitoring and alerting

## Architecture Notes

The services communicate as follows:

```
Client -> Client Proxy (3001/3002) -> API (3000) -> PostgreSQL/ClickHouse
                |                          |
                v                          v
          Orchestrator (5008) <-----> Template Manager (5009)
                |
                v
          Docker Registry Proxy (5000)
```

All services send logs to Loki and metrics/traces to OpenTelemetry Collector. 