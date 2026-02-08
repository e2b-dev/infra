# Persistent Filesystem Design Document

**Version:** 0.1 (Minimal PoC)  
**Date:** January 20, 2026  
**Status:** Draft  

## Overview

This document describes the design for E2B's persistent filesystem feature, which allows teams to create persistent storage volumes that can be mounted in sandboxes and accessed via the SDK/API.

## Goals

### V0 (Minimal PoC)
1. Teams can create persistent volumes via SDK/API
2. Volumes can be mounted in sandboxes at specified paths
3. Files can be read/written via the mounted path inside sandboxes
4. Files can be read/written via SDK/API independently of sandboxes
5. Basic CRUD operations: create, read, write, list, delete

### Non-Goals for V0
- Web UI for file management
- TUI for file management
- S3-compatible API (future iteration)
- Block-level storage (using file-level for simplicity)
- Automatic concurrent access management (NFS handles this natively)

## Architecture

### High-Level Flow

```
┌─────────────────────────────────────────────────────────────────────┐
│                           SDK / API                                 │
│                                                                     │
│  Filesystem.create()    Sandbox.create()    filesystem.read/write   │
└──────────┬─────────────────────┬────────────────────┬───────────────┘
           │                     │                    │
           ▼                     ▼                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│                          E2B API                                    │
│                                                                     │
│   POST /filesystems     POST /sandboxes       GET/PUT /filesystems  │
│                         (with volumeMounts)   /{id}/files/{path}    │
└──────────┬─────────────────────┬────────────────────┬───────────────┘
           │                     │                    │
           ▼                     ▼                    ▼
┌─────────────────┐    ┌─────────────────────────────────┐
│   PostgreSQL    │    │           Orchestrator          │
│   (metadata)    │    │ (VM mgmt, NFS Proxy, Portmap)   │
└─────────────────┘    └────────┬───────────────┬────────┘
                                │               │
                                ▼               ▼
                       ┌─────────────────┐    ┌─────────────────┐
                       │   Firecracker   │    │   GCS Backend   │
                       │   VM + NFS      │    │   (storage)     │
                       │   Client        │    │                 │
                       └─────────────────┘    └─────────────────┘
```

### Components

#### 1. NFS Proxy & Portmapper (Orchestrator)
- **Technology:** Go implementation using [willscott/go-nfs](https://github.com/willscott/go-nfs) and custom Portmapper.
- **Protocol:** NFS v3 (stateless design) and RPC Portmapper (v2).
- **Purpose:** 
    - **NFS Proxy:** Translates NFS operations from sandboxes to GCS backend storage operations.
    - **Portmapper:** Registers the NFS service and allows clients to discover the NFS port (2049) via RPC port 111.
- **Current Status:** Integrated into Orchestrator; implementation complete.

#### 2. GCS Backend
- **Purpose:** Persistent storage layer for file data
- **Operations:** Maps NFS file operations to GCS object operations
- **Current Status:** Bare minimum implementation complete. Integration with Orchestrator is done via IPTables redirection. `nfs-common` is installed in the base image.

#### 3. Database Schema (New)

```sql
-- Persistent filesystems/volumes table
CREATE TABLE filesystems (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    size_bytes BIGINT NOT NULL,  -- Quota/limit for the volume
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(team_id, name)
);

-- Index for efficient team lookups
CREATE INDEX idx_filesystems_team_id ON filesystems(team_id);
```

#### 4. API Endpoints

##### Filesystem Management

```yaml
# Create a new filesystem
POST /filesystems
Request:
  {
    "name": "my-volume",        # Unique name within team
    "sizeGB": 5                 # Storage quota in GB
  }
Response:
  {
    "id": "fs-abc123",
    "name": "my-volume",
    "sizeGB": 5,
    "createdAt": "2026-01-20T12:00:00Z"
  }

# List filesystems for a team
GET /filesystems
Response:
  [
    {
      "id": "fs-abc123",
      "name": "my-volume",
      "sizeGB": 5,
      "createdAt": "2026-01-20T12:00:00Z"
    }
  ]

# Delete a filesystem
DELETE /filesystems/{filesystemID}
Response: 204 No Content
```

##### File Operations (via API)

```yaml
# Read a file
GET /filesystems/{filesystemID}/files/{path}
Response: File content (binary)

# Write a file
PUT /filesystems/{filesystemID}/files/{path}
Request: File content (binary)
Headers:
  X-E2B-User: root  # Optional: file owner
Response: 201 Created

# List directory
GET /filesystems/{filesystemID}/files/{path}?list=true
Response:
  {
    "entries": [
      {"name": "file.txt", "type": "file", "size": 1024},
      {"name": "subdir", "type": "directory"}
    ]
  }

# Delete file/directory
DELETE /filesystems/{filesystemID}/files/{path}
Response: 204 No Content
```

##### Sandbox Creation with Volume Mounts

```yaml
# Create sandbox with volume mount
POST /sandboxes
Request:
  {
    "templateID": "base",
    "volumeMounts": {
      "/mnt/data": "fs-abc123"  # mountPath: filesystemID
    }
  }
```

## SDK Interface

### TypeScript/JavaScript

```typescript
// Create a filesystem
const filesystem = await Filesystem.create('my-volume', '5gb')

// Create sandbox with volume mount
const sbx = await Sandbox.create({
  template: 'base',
  volumeMounts: {
    '/mnt/data': filesystem.id,
    // or by name
    '/tmp/test': 'my-volume',
  },
})

// File operations via SDK (independent of sandbox)
await filesystem.write('/data/test.txt', 'Hello, World!', {
  user: 'root',
})

const content = await filesystem.read('/data/test.txt')

const entries = await filesystem.list('/data')

await filesystem.delete('/data/test.txt')

// File operations via sandbox (when mounted)
await sbx.filesystem.write('/mnt/data/test.txt', 'Hello from sandbox!')
```

### Python

```python
# Create a filesystem
filesystem = Filesystem.create('my-volume', '5gb')

# Create sandbox with volume mount
sbx = Sandbox.create(
    template='base',
    volume_mounts={
        '/mnt/data': filesystem.id,
    }
)

# File operations via SDK
filesystem.write('/data/test.txt', 'Hello, World!', user='root')
content = filesystem.read('/data/test.txt')
entries = filesystem.list('/data')
filesystem.delete('/data/test.txt')
```

## Implementation Details

### NFS Mount in Sandbox

When a sandbox is created with volume mounts:

1. **Orchestrator** receives the sandbox creation request with volume mount config.
2. **Orchestrator** sets up the sandbox network. It includes **IPTables** rules to redirect traffic destined for the Orchestrator IP on ports 111 (Portmapper) and 2049 (NFS) to the Orchestrator's internal services.
3. **Firecracker VM** starts. The base image must have `nfs-common` installed.
4. **Init script** mounts NFS shares to specified paths. The VM uses the Orchestrator's internal IP as the NFS server address:
   ```bash
   mount -t nfs -o vers=3,nolock <orchestrator-ip>:/<filesystem-id> /mnt/data
   ```

### NFS Proxy → GCS Mapping

| NFS Operation | GCS Operation |
|---------------|---------------|
| `LOOKUP` | `objects.get` (metadata) |
| `READ` | `objects.get` (content) |
| `WRITE` | `objects.insert` |
| `CREATE` | `objects.insert` |
| `REMOVE` | `objects.delete` |
| `READDIR` | `objects.list` |
| `MKDIR` | `objects.insert` (empty marker) |
| `RMDIR` | `objects.delete` |
| `GETATTR` | `objects.get` (metadata) |
| `SETATTR` | `objects.update` (metadata) |

### GCS Object Layout

```
gs://<bucket>/filesystems/<team-id>/<filesystem-id>/
  ├── data/
  │   ├── file1.txt           # Actual file content
  │   └── subdir/
  │       └── file2.txt
  └── .meta/                   # Metadata (permissions, ownership)
      ├── file1.txt.meta
      └── subdir/
          └── file2.txt.meta
```

### Concurrency Considerations

For V0, we allow concurrent access via NFS (no explicit locking):
- NFS v3 is inherently stateless
- Multiple sandboxes can mount the same filesystem
- GCS provides eventual consistency for concurrent writes
- Applications should handle their own file locking if needed

**Note:** The original requirement suggested single-sandbox access for simplicity, but implementing this restriction would require additional complexity. NFS naturally supports concurrent access, so we leverage this in V0.

## Security

### Authentication & Authorization
- Filesystem operations require valid team API key
- Filesystems are scoped to teams (team-level isolation)
- Users cannot access filesystems from other teams

### Network Security
- NFS traffic between sandbox and NFS proxy is internal network only
- API access requires HTTPS and authentication

### Data Isolation
- Each team's data is stored in separate GCS paths
- Filesystem IDs are UUIDs to prevent enumeration

## Observability

### Metrics
- `filesystem_operations_total{operation, status}` - Operation counts
- `filesystem_bytes_read_total` - Total bytes read
- `filesystem_bytes_written_total` - Total bytes written
- `filesystem_operation_duration_seconds` - Operation latency

### Logging
- All filesystem operations logged with team_id, filesystem_id
- Error conditions logged with full context
- NFS Proxy logs includes detailed operation tracking and GCS interaction details

## Future Considerations (Post-V0)

1. **S3-Compatible API** - For broader ecosystem compatibility
2. **Block Storage** - For use cases requiring block-level access
3. **Snapshots** - Point-in-time copies of filesystems
4. **Quotas & Billing** - Usage tracking and enforcement
5. **Cross-Region Replication** - For high availability
6. **File-level Permissions** - More granular access control
7. **Compression** - Reduce storage costs
8. **Encryption at Rest** - Additional security layer

## Migration Path

Since this is a new feature, no migration is required. The system is designed to be additive and doesn't affect existing sandbox functionality.

## Testing Strategy

### Unit Tests
- Database operations (CRUD for filesystems table)
- API handlers
- NFS operation translation

### Integration Tests
- End-to-end filesystem creation and mounting
- File read/write via mounted path in sandbox
- File read/write via API
- Concurrent access scenarios

### Manual Testing
- SDK usage patterns
- Error handling and edge cases

## Rollout Plan

1. **Phase 1:** Internal testing with NFS proxy and GCS backend
2. **Phase 2:** Database schema and API implementation
3. **Phase 3:** SDK integration
4. **Phase 4:** Documentation and beta release
5. **Phase 5:** GA release

## References

- [NFS v3 Protocol Specification (RFC 1813)](https://datatracker.ietf.org/doc/html/rfc1813)
- [willscott/go-nfs](https://github.com/willscott/go-nfs) - Go NFS server implementation
- [willscott/go-nfs-client](https://github.com/willscott/go-nfs-client) - Go NFS client implementation

## Appendix: OpenAPI Spec Additions

```yaml
components:
  schemas:
    Filesystem:
      type: object
      required:
        - id
        - name
        - sizeGB
        - createdAt
      properties:
        id:
          type: string
          description: Unique identifier for the filesystem
        name:
          type: string
          description: User-defined name for the filesystem
        sizeGB:
          type: integer
          description: Storage quota in gigabytes
        createdAt:
          type: string
          format: date-time
          description: Timestamp when the filesystem was created

    NewFilesystem:
      type: object
      required:
        - name
        - sizeGB
      properties:
        name:
          type: string
          description: User-defined name for the filesystem
        sizeGB:
          type: integer
          minimum: 1
          maximum: 100
          description: Storage quota in gigabytes

    VolumeMounts:
      type: object
      additionalProperties:
        type: string
      description: Map of mount paths to filesystem IDs

  # Addition to NewSandbox schema
  NewSandbox:
    properties:
      # ... existing properties ...
      volumeMounts:
        $ref: "#/components/schemas/VolumeMounts"

paths:
  /filesystems:
    get:
      description: List all filesystems for the team
      tags: [filesystems]
      security:
        - ApiKeyAuth: []
      responses:
        "200":
          description: Successfully returned all filesystems
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: "#/components/schemas/Filesystem"
    post:
      description: Create a new filesystem
      tags: [filesystems]
      security:
        - ApiKeyAuth: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/NewFilesystem"
      responses:
        "201":
          description: Filesystem created successfully
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Filesystem"

  /filesystems/{filesystemID}:
    delete:
      description: Delete a filesystem
      tags: [filesystems]
      security:
        - ApiKeyAuth: []
      parameters:
        - name: filesystemID
          in: path
          required: true
          schema:
            type: string
      responses:
        "204":
          description: Filesystem deleted successfully

  /filesystems/{filesystemID}/files/{path}:
    get:
      description: Read a file or list directory contents
      tags: [filesystems]
      security:
        - ApiKeyAuth: []
      parameters:
        - name: filesystemID
          in: path
          required: true
          schema:
            type: string
        - name: path
          in: path
          required: true
          schema:
            type: string
        - name: list
          in: query
          required: false
          schema:
            type: boolean
            default: false
      responses:
        "200":
          description: File content or directory listing

    put:
      description: Write a file
      tags: [filesystems]
      security:
        - ApiKeyAuth: []
      parameters:
        - name: filesystemID
          in: path
          required: true
          schema:
            type: string
        - name: path
          in: path
          required: true
          schema:
            type: string
        - name: X-E2B-User
          in: header
          required: false
          schema:
            type: string
            default: root
      requestBody:
        required: true
        content:
          application/octet-stream:
            schema:
              type: string
              format: binary
      responses:
        "201":
          description: File written successfully

    delete:
      description: Delete a file or directory
      tags: [filesystems]
      security:
        - ApiKeyAuth: []
      parameters:
        - name: filesystemID
          in: path
          required: true
          schema:
            type: string
        - name: path
          in: path
          required: true
          schema:
            type: string
      responses:
        "204":
          description: File deleted successfully
```
