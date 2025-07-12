# E2B Infrastructure - Build System and Architecture Summary

## Project Overview

E2B.dev is an infrastructure platform for running sandboxed environments. The project uses a microservices architecture deployed on Google Cloud Platform (GCP) with Terraform for infrastructure provisioning and Nomad for orchestration.

## Build System Analysis

### 1. Technology Stack

- **Primary Language**: Go (Golang)
- **Infrastructure**: Terraform + GCP
- **Container Orchestration**: Nomad (production), Docker Compose (local development)
- **Databases**: PostgreSQL (main), ClickHouse (analytics)
- **Caching**: Redis
- **Build Tools**: Make, Docker

### 2. Main Services

#### Core Services:
1. **API** (`packages/api`)
   - Main REST API service
   - Handles authentication, sandbox management, team operations
   - Port: 3000

2. **Client Proxy** (`packages/client-proxy`)
   - Routes client connections to appropriate services
   - Handles edge API and proxy functionality
   - Ports: 3001 (edge), 3002 (proxy)

3. **Orchestrator** (`packages/orchestrator`)
   - Core service managing sandbox lifecycle
   - Handles Firecracker VM orchestration
   - Port: 5008 (gRPC)

4. **Template Manager** (part of orchestrator)
   - Manages sandbox templates and builds
   - Port: 5009

5. **Docker Reverse Proxy** (`packages/docker-reverse-proxy`)
   - Handles Docker registry operations
   - Port: 5000

6. **EnvD** (`packages/envd`)
   - Environment daemon running inside sandboxes
   - Provides file system and process management APIs
   - Port: 49983

#### Supporting Services:
- **DB Migrator**: Handles PostgreSQL schema migrations
- **ClickHouse Migrator**: Handles ClickHouse schema migrations
- **Shared Package**: Common utilities and models

### 3. Build Process

#### Makefile Structure:
The main Makefile orchestrates the entire build process:

1. **Environment Management**:
   - Uses `.env.${ENV}` files for environment-specific configuration
   - Supports multiple environments (dev, staging, prod)
   - `make switch-env ENV=<environment>` to switch contexts

2. **Key Build Targets**:
   ```bash
   make build/<service>           # Build individual service
   make build-and-upload          # Build and push all services to GCP
   make generate                  # Generate code (OpenAPI, protobuf)
   make test                      # Run all tests
   make migrate                   # Run database migrations
   ```

3. **Service-Specific Builds**:
   Each service has its own Makefile that:
   - Builds the Go binary with version info
   - Creates optimized Docker images
   - Handles code generation (OpenAPI, protobuf)

#### Docker Build Process:
1. Multi-stage Dockerfiles for optimized images
2. Alpine Linux base for minimal size
3. Build arguments for versioning (`COMMIT_SHA`)
4. Shared dependencies copied during build

### 4. Dependencies

#### External Dependencies:
- **Google Cloud Platform**: 
  - Cloud Storage (templates, kernels)
  - Artifact Registry (Docker images)
  - Secret Manager
- **Firecracker**: MicroVM technology for sandboxes
- **Supabase**: JWT authentication
- **PostHog**: Analytics (optional)
- **LaunchDarkly**: Feature flags (optional)

#### Inter-Service Dependencies:
```
Client → Client Proxy → API → Orchestrator → Sandbox (Firecracker)
                         ↓         ↓
                    PostgreSQL  Template Manager
                         ↓
                    ClickHouse
```

### 5. Local Development Setup

Two Docker Compose configurations are provided:

1. **Full Stack** (`docker-compose.yml`):
   - All services including telemetry
   - Requires more resources
   - Closer to production setup

2. **Minimal Stack** (`docker-compose.minimal.yml`):
   - Core services only
   - Uses MinIO instead of GCS
   - Easier to run locally

### 6. Configuration Management

Environment variables control service behavior:
- Database connections
- Service discovery
- Feature flags
- Resource limits
- Security tokens

### 7. Code Generation

The project uses code generation for:
- **OpenAPI**: API endpoints and types
- **Protobuf/gRPC**: Inter-service communication
- **SQL**: Type-safe database queries (sqlc)

## Getting Started

### Quick Start (Minimal Setup):
```bash
# 1. Run the quick start script
./quick-start.sh

# 2. Access the API
curl http://localhost:3000/health
```

### Full Setup:
```bash
# 1. Build all services
./docker-compose-build.sh

# 2. Start all services
docker-compose up -d

# 3. Check service health
docker-compose ps
```

### Development Workflow:
1. Make code changes in relevant package
2. Run `make generate` if APIs changed
3. Build service: `./docker-compose-build.sh`
4. Restart service: `docker-compose up -d <service>`
5. View logs: `docker-compose logs -f <service>`

## Production Deployment

In production, the system uses:
- Terraform for infrastructure provisioning
- Nomad for container orchestration
- Consul for service discovery
- GCP managed services (Cloud SQL, etc.)

The deployment process:
1. `make build-and-upload` - Build and push images
2. `make plan` - Review Terraform changes
3. `make apply` - Apply infrastructure changes
4. Nomad automatically deploys new containers

## Key Insights

1. **Modular Architecture**: Each service is independently deployable
2. **Cloud-Native Design**: Built for GCP but adaptable
3. **Developer Experience**: Local development mirrors production
4. **Observability**: Comprehensive logging and metrics
5. **Security**: JWT authentication, service-to-service auth
6. **Scalability**: Horizontal scaling via Nomad

The build system is sophisticated but well-organized, supporting both local development and production deployments efficiently. 