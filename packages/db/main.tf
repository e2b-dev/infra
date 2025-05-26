# provider.tf
terraform {
  required_providers {
    postgresql = {
      source  = "cyrilgdn/postgresql"
      version = "1.25.0"
    }
  }
}


# ---- Parsing -------------------------------------------------
locals {
  # Extract username, password, host and port exactly as before
  _captures = regexall(
    "postgres(?:ql)?://([^:]+):([^@]+)@([^:/]+):([0-9]+)/([^?]+)",
    var.postgresql_connection_string
  )[0]

  # ── Username handling ───────────────────────────────────────
  username_full  = local._captures[0]
  username_parts = split(".", local.username_full)

  username      = local.username_parts[0]
  pooler_suffix = length(local.username_parts) > 1 ? ".${local.username_parts[1]}" : ""

  # ── Remaining fields ────────────────────────────────────────
  password = local._captures[1]
  hostname = local._captures[2]
  port     = tonumber(local._captures[3])
  database = local._captures[4]
}

provider "postgresql" {
  host            = local.hostname
  port            = local.port
  database        = "postgres"
  username        = local.username_full
  password        = local.password
  sslmode         = "require"
  connect_timeout = 15
  superuser       = false
}


resource "random_password" "api" {
  length = 24

  special = false
}

resource "postgresql_role" "api" {
  name                      = "api"
  login                     = true
  password                  = random_password.api.result
  bypass_row_level_security = true
}

resource "postgresql_grant" "api_public_schema_usage" {
  database    = local.database
  role        = postgresql_role.api.name
  object_type = "schema"
  schema      = "public"
  privileges  = ["USAGE"]

  lifecycle {
    create_before_destroy = true
  }
}

resource "postgresql_grant" "api_public" {
  database    = local.database
  role        = postgresql_role.api.name
  schema      = "public"
  object_type = "table"
  privileges  = ["SELECT", "INSERT", "UPDATE", "DELETE"]


  lifecycle {
    create_before_destroy = true
  }
}

resource "postgresql_default_privileges" "api_future_tables" {
  database    = local.database
  owner       = local.username
  role        = postgresql_role.api.name
  schema      = "public"
  object_type = "table"
  privileges  = ["SELECT", "INSERT", "UPDATE", "DELETE"]
}

resource "postgresql_grant" "api_auth" {
  database    = local.database
  role        = postgresql_role.api.name
  schema      = "auth"
  object_type = "table"
  privileges  = ["SELECT"]
}

resource "random_password" "docker_reverse_proxy" {
  length = 24

  special = false
}

resource "postgresql_grant" "docker_reverse_proxy_public_schema_usage" {
  database    = local.database
  role        = postgresql_role.docker_reverse_proxy.name
  object_type = "schema"
  schema      = "public"
  privileges  = ["USAGE"]
}

resource "postgresql_role" "docker_reverse_proxy" {
  name                      = "docker_reverse_proxy"
  login                     = true
  password                  = random_password.docker_reverse_proxy.result
  bypass_row_level_security = true
}

resource "postgresql_grant" "docker_reserve_proxy" {
  database    = local.database
  role        = postgresql_role.docker_reverse_proxy.name
  schema      = "public"
  object_type = "table"
  privileges  = ["SELECT"]
}

resource "postgresql_grant" "docker_reverse_proxy_auth" {
  database    = local.database
  role        = postgresql_role.docker_reverse_proxy.name
  schema      = "auth"
  object_type = "table"
  privileges  = ["SELECT"]
}

resource "postgresql_default_privileges" "docker_reverse_proxy_future_tables" {
  database    = local.database
  owner       = local.username
  role        = postgresql_role.docker_reverse_proxy.name
  schema      = "public"
  object_type = "table"
  privileges  = ["SELECT"]
}
