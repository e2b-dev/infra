DO $$ 
DECLARE
    team_id UUID := '834777bd-9956-45ca-b088-9bac9290e2ac';
    env_id TEXT := '2j6ly824owf4awgai1xo';
    build_id UUID := '98130b63-ca75-431d-956a-9d829d14d317';
BEGIN

-- Team
INSERT INTO "public"."teams" (
    "id", "created_at", "is_blocked", "name", "tier", "email", "is_banned", "blocked_reason"
) VALUES (
             team_id,
             '2025-01-20 23:48:40.617674+00',
             'false',
             'E2B',
             'base_v1',
             'test-integration@e2b.dev',
             'false',
             NULL
         );

-- Team API Key
INSERT INTO "public"."team_api_keys" (
    "api_key", "created_at", "team_id", "updated_at", "name",
    "last_used", "created_by", "id"
) VALUES (
             'e2b_5ec17bd3933af21f80dc10bba686691c4fcd7057',
             '2025-01-20 23:48:41.786327+00',
             team_id,
             NULL,
             'Integration Tests API Key',
             NULL,
             NULL,
             '92545e69-c024-4d54-970a-367b37395f9d'
         );

-- Base image build
INSERT INTO "public"."envs" (
    "id", "created_at", "updated_at", "public", "build_count",
    "spawn_count", "last_spawned_at", "team_id", "created_by"
) VALUES (
             env_id,
             '2025-02-18 20:44:45.370521+00',
             '2025-02-18 20:46:15.890456+00',
             'false',
             '1',
             '0',
             '2025-02-22 00:17:24.675901+00',
             team_id,
             NULL
         );

INSERT INTO "public"."env_builds" (
    "id", "created_at", "updated_at", "finished_at", "status",
    "dockerfile", "start_cmd", "vcpu", "ram_mb", "free_disk_size_mb",
    "total_disk_size_mb", "kernel_version", "firecracker_version",
    "env_id", "envd_version"
) VALUES (
             build_id,
             '2025-02-18 20:46:16.030485+00',
             '2025-02-18 20:46:16.030486+00',
             '2025-02-18 20:47:13.944072+00',
             'uploaded',
             'FROM e2bdev/base:latest',
             NULL,
             '2',
             '512',
             '512',
             '1982',
             'vmlinux-6.1.102',
             'v1.10.1_1fcdaec',
             env_id,
             '0.1.5'
         );

INSERT INTO "public"."env_aliases" (
    "alias", "is_renamable", "env_id"
) VALUES (
             'base',
             'true',
             env_id
         );

END $$;package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

func main() {
	connectionString := os.Getenv("POSTGRES_CONNECTION_STRING")
	if connectionString == "" {
		log.Fatalf("POSTGRES_CONNECTION_STRING is not set")
	}

	db, err := sql.Open("postgres", connectionString)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	seed, err := os.ReadFile("seed.sql")
	if err != nil {
		log.Fatalf("Failed to read seed file: %v", err)
	}

	// Execute the seed
	_, err = db.Exec(string(seed))
	if err != nil {
		log.Fatalf("Failed to execute seed: %v", err)
	}

	fmt.Println("Seed completed successfully.")
}
