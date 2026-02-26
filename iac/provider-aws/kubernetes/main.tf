locals {
  clickhouse_connection_string = var.clickhouse_server_count > 0 ? "clickhouse://${var.clickhouse_username}:${random_password.clickhouse_password.result}@clickhouse.e2b.svc.cluster.local:${var.clickhouse_server_port.port}/${var.clickhouse_database}" : ""
  redis_url                    = trimspace(data.aws_secretsmanager_secret_version.redis_cluster_url.secret_string) == "" ? "redis.e2b.svc.cluster.local:${var.redis_port.port}" : ""
  redis_cluster_url            = trimspace(data.aws_secretsmanager_secret_version.redis_cluster_url.secret_string)
  loki_url                     = "http://loki.e2b.svc.cluster.local:${var.loki_service_port.port}"

  common_labels = {
    "app.kubernetes.io/managed-by" = "terraform"
    "app.kubernetes.io/part-of"    = "e2b"
  }
}

# --- gp3 StorageClass for EBS CSI Driver ---
resource "kubernetes_storage_class_v1" "gp3" {
  metadata {
    name = "gp3"
    annotations = {
      "storageclass.kubernetes.io/is-default-class" = "true"
    }
  }

  storage_provisioner = "ebs.csi.aws.com"
  reclaim_policy      = "Delete"
  volume_binding_mode = "WaitForFirstConsumer"

  parameters = {
    type      = "gp3"
    encrypted = "true"
    fsType    = "ext4"
  }
}

# --- Namespace ---
resource "kubernetes_namespace_v1" "e2b" {
  metadata {
    name = "e2b"
    labels = merge(local.common_labels, {
      "pod-security.kubernetes.io/enforce" = "baseline"
      "pod-security.kubernetes.io/warn"    = "restricted"
    })
  }
}

# --- Secrets Manager reads ---
data "aws_secretsmanager_secret_version" "postgres_connection_string" {
  secret_id = var.postgres_connection_string_secret_arn
}

data "aws_secretsmanager_secret_version" "postgres_read_replica_connection_string" {
  secret_id = var.postgres_read_replica_connection_string_secret_arn
}

data "aws_secretsmanager_secret_version" "supabase_jwt_secrets" {
  secret_id = var.supabase_jwt_secrets_secret_arn
}

data "aws_secretsmanager_secret_version" "posthog_api_key" {
  secret_id = var.posthog_api_key_secret_arn
}

data "aws_secretsmanager_secret_version" "analytics_collector_host" {
  secret_id = var.analytics_collector_host_secret_arn
}

data "aws_secretsmanager_secret_version" "analytics_collector_api_token" {
  secret_id = var.analytics_collector_api_token_secret_arn
}

data "aws_secretsmanager_secret_version" "launch_darkly_api_key" {
  secret_id = var.launch_darkly_api_key_secret_arn
}

data "aws_secretsmanager_secret_version" "redis_cluster_url" {
  secret_id = var.redis_cluster_url_secret_arn
}

data "aws_secretsmanager_secret_version" "redis_tls_ca_base64" {
  secret_id = var.redis_tls_ca_base64_secret_arn
}

# --- Grafana secrets (create + read pattern) ---
resource "aws_secretsmanager_secret" "grafana_otlp_url" {
  name = "${var.prefix}grafana-otlp-url"
}

resource "aws_secretsmanager_secret_version" "grafana_otlp_url" {
  secret_id     = aws_secretsmanager_secret.grafana_otlp_url.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "grafana_otlp_url" {
  secret_id  = aws_secretsmanager_secret.grafana_otlp_url.id
  depends_on = [aws_secretsmanager_secret_version.grafana_otlp_url]
}

resource "aws_secretsmanager_secret" "grafana_otel_collector_token" {
  name = "${var.prefix}grafana-otel-collector-token"
}

resource "aws_secretsmanager_secret_version" "grafana_otel_collector_token" {
  secret_id     = aws_secretsmanager_secret.grafana_otel_collector_token.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "grafana_otel_collector_token" {
  secret_id  = aws_secretsmanager_secret.grafana_otel_collector_token.id
  depends_on = [aws_secretsmanager_secret_version.grafana_otel_collector_token]
}

resource "aws_secretsmanager_secret" "grafana_username" {
  name = "${var.prefix}grafana-username"
}

resource "aws_secretsmanager_secret_version" "grafana_username" {
  secret_id     = aws_secretsmanager_secret.grafana_username.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "grafana_username" {
  secret_id  = aws_secretsmanager_secret.grafana_username.id
  depends_on = [aws_secretsmanager_secret_version.grafana_username]
}

# --- Grafana logs secrets ---
resource "aws_secretsmanager_secret" "grafana_logs_user" {
  name = "${var.prefix}grafana-logs-user"
}

resource "aws_secretsmanager_secret_version" "grafana_logs_user" {
  secret_id     = aws_secretsmanager_secret.grafana_logs_user.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "grafana_logs_user" {
  secret_id  = aws_secretsmanager_secret.grafana_logs_user.id
  depends_on = [aws_secretsmanager_secret_version.grafana_logs_user]
}

resource "aws_secretsmanager_secret" "grafana_logs_url" {
  name = "${var.prefix}grafana-logs-url"
}

resource "aws_secretsmanager_secret_version" "grafana_logs_url" {
  secret_id     = aws_secretsmanager_secret.grafana_logs_url.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "grafana_logs_url" {
  secret_id  = aws_secretsmanager_secret.grafana_logs_url.id
  depends_on = [aws_secretsmanager_secret_version.grafana_logs_url]
}

resource "aws_secretsmanager_secret" "grafana_logs_collector_api_token" {
  name = "${var.prefix}grafana-api-key-logs-collector"
}

resource "aws_secretsmanager_secret_version" "grafana_logs_collector_api_token" {
  secret_id     = aws_secretsmanager_secret.grafana_logs_collector_api_token.id
  secret_string = " "

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "grafana_logs_collector_api_token" {
  secret_id  = aws_secretsmanager_secret.grafana_logs_collector_api_token.id
  depends_on = [aws_secretsmanager_secret_version.grafana_logs_collector_api_token]
}

# --- ClickHouse passwords ---
resource "random_password" "clickhouse_password" {
  length  = 32
  special = false
}

resource "aws_secretsmanager_secret" "clickhouse_password" {
  name = "${var.prefix}clickhouse-password"
}

resource "aws_secretsmanager_secret_version" "clickhouse_password_value" {
  secret_id     = aws_secretsmanager_secret.clickhouse_password.id
  secret_string = random_password.clickhouse_password.result
}

resource "random_password" "clickhouse_server_secret" {
  length  = 32
  special = false
}

resource "aws_secretsmanager_secret" "clickhouse_server_secret" {
  name = "${var.prefix}clickhouse-server-secret"
}

resource "aws_secretsmanager_secret_version" "clickhouse_server_secret_value" {
  secret_id     = aws_secretsmanager_secret.clickhouse_server_secret.id
  secret_string = random_password.clickhouse_server_secret.result
}

# --- K8s Secrets ---
resource "kubernetes_secret_v1" "clickhouse_credentials" {
  metadata {
    name      = "clickhouse-credentials"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = local.common_labels
  }

  data = {
    CLICKHOUSE_PASSWORD = random_password.clickhouse_password.result
  }
}

resource "kubernetes_secret_v1" "otel_credentials" {
  metadata {
    name      = "otel-credentials"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = local.common_labels
  }

  data = {
    GRAFANA_OTEL_COLLECTOR_TOKEN = data.aws_secretsmanager_secret_version.grafana_otel_collector_token.secret_string
    GRAFANA_OTLP_URL             = data.aws_secretsmanager_secret_version.grafana_otlp_url.secret_string
    GRAFANA_USERNAME             = data.aws_secretsmanager_secret_version.grafana_username.secret_string
  }
}

resource "kubernetes_secret_v1" "e2b_secrets" {
  metadata {
    name      = "e2b-secrets"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = local.common_labels
  }

  data = {
    POSTGRES_CONNECTION_STRING              = data.aws_secretsmanager_secret_version.postgres_connection_string.secret_string
    POSTGRES_READ_REPLICA_CONNECTION_STRING = trimspace(data.aws_secretsmanager_secret_version.postgres_read_replica_connection_string.secret_string)
    SUPABASE_JWT_SECRETS                    = trimspace(data.aws_secretsmanager_secret_version.supabase_jwt_secrets.secret_string)
    POSTHOG_API_KEY                         = trimspace(data.aws_secretsmanager_secret_version.posthog_api_key.secret_string)
    ANALYTICS_COLLECTOR_HOST                = trimspace(data.aws_secretsmanager_secret_version.analytics_collector_host.secret_string)
    ANALYTICS_COLLECTOR_API_TOKEN           = trimspace(data.aws_secretsmanager_secret_version.analytics_collector_api_token.secret_string)
    LAUNCH_DARKLY_API_KEY                   = trimspace(data.aws_secretsmanager_secret_version.launch_darkly_api_key.secret_string)
    REDIS_TLS_CA_BASE64                     = trimspace(data.aws_secretsmanager_secret_version.redis_tls_ca_base64.secret_string)
    CLICKHOUSE_PASSWORD                     = random_password.clickhouse_password.result
    CLICKHOUSE_SERVER_SECRET                = random_password.clickhouse_server_secret.result
    API_SECRET                              = var.api_secret
    API_ADMIN_TOKEN                         = var.api_admin_token
    SANDBOX_ACCESS_TOKEN_HASH_SEED          = var.sandbox_access_token_hash_seed
    GRAFANA_OTEL_COLLECTOR_TOKEN            = data.aws_secretsmanager_secret_version.grafana_otel_collector_token.secret_string
    GRAFANA_OTLP_URL                        = data.aws_secretsmanager_secret_version.grafana_otlp_url.secret_string
    GRAFANA_USERNAME                        = data.aws_secretsmanager_secret_version.grafana_username.secret_string
    GRAFANA_LOGS_USER                       = trimspace(data.aws_secretsmanager_secret_version.grafana_logs_user.secret_string)
    GRAFANA_LOGS_URL                        = trimspace(data.aws_secretsmanager_secret_version.grafana_logs_url.secret_string)
    GRAFANA_LOGS_API_KEY                    = trimspace(data.aws_secretsmanager_secret_version.grafana_logs_collector_api_token.secret_string)
  }
}

# --- API Deployment ---
resource "kubernetes_deployment_v1" "api" {
  metadata {
    name      = "api"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "api" })
  }

  spec {
    replicas = var.api_machine_count

    selector {
      match_labels = { "app.kubernetes.io/name" = "api" }
    }

    strategy {
      type = "RollingUpdate"
      rolling_update {
        max_unavailable = 1
        max_surge       = 1
      }
    }

    template {
      metadata {
        labels = merge(local.common_labels, { "app.kubernetes.io/name" = "api" })
      }

      spec {
        # Run on system/bootstrap nodes (not client/build)
        node_selector = {
          "e2b.dev/node-pool" = "system"
        }

        init_container {
          name  = "db-migrator"
          image = "${var.core_repository_url}:db-migrator-latest"

          env_from {
            secret_ref {
              name = kubernetes_secret_v1.e2b_secrets.metadata[0].name
            }
          }
        }

        container {
          name  = "api"
          image = "${var.core_repository_url}:api-latest"

          port {
            container_port = var.api_port.port
            name           = "http"
          }

          port {
            container_port = var.api_grpc_port
            name           = "grpc"
          }

          env {
            name  = "ORCHESTRATOR_PORT"
            value = "5008"
          }

          env {
            name  = "OTEL_COLLECTOR_GRPC_ENDPOINT"
            value = "localhost:${var.otel_collector_grpc_port}"
          }

          env {
            name  = "LOGS_COLLECTOR_ADDRESS"
            value = "http://localhost:${var.logs_proxy_port.port}"
          }

          env {
            name  = "PORT"
            value = tostring(var.api_port.port)
          }

          env {
            name  = "GRPC_PORT"
            value = tostring(var.api_grpc_port)
          }

          env {
            name  = "ENVIRONMENT"
            value = var.environment
          }

          env {
            name  = "REDIS_URL"
            value = local.redis_url
          }

          env {
            name  = "REDIS_CLUSTER_URL"
            value = local.redis_cluster_url
          }

          env {
            name  = "CLICKHOUSE_CONNECTION_STRING"
            value = local.clickhouse_connection_string
          }

          env {
            name  = "LOKI_URL"
            value = local.loki_url
          }

          env_from {
            secret_ref {
              name = kubernetes_secret_v1.e2b_secrets.metadata[0].name
            }
          }

          resources {
            requests = {
              cpu    = "${var.api_resources_cpu_count}"
              memory = "${var.api_resources_memory_mb}Mi"
            }
            limits = {
              cpu    = "${var.api_resources_cpu_count}"
              memory = "${var.api_resources_memory_mb}Mi"
            }
          }

          liveness_probe {
            http_get {
              path = var.api_port.health_path
              port = var.api_port.port
            }
            initial_delay_seconds = 15
            period_seconds        = 10
          }

          readiness_probe {
            http_get {
              path = var.api_port.health_path
              port = var.api_port.port
            }
            initial_delay_seconds = 5
            period_seconds        = 5
          }
        }
      }
    }
  }
}

# --- API Service ---
resource "kubernetes_service_v1" "api" {
  metadata {
    name      = "api"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "api" })
  }

  spec {
    selector = { "app.kubernetes.io/name" = "api" }
    type     = "NodePort"

    port {
      name        = "http"
      port        = var.api_port.port
      target_port = var.api_port.port
    }

    port {
      name        = "grpc"
      port        = var.api_grpc_port
      target_port = var.api_grpc_port
    }
  }
}

# --- API Horizontal Pod Autoscaler ---
resource "kubernetes_horizontal_pod_autoscaler_v2" "api" {
  metadata {
    name      = "api"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "api" })
  }

  spec {
    scale_target_ref {
      api_version = "apps/v1"
      kind        = "Deployment"
      name        = kubernetes_deployment_v1.api.metadata[0].name
    }

    min_replicas = var.api_machine_count
    max_replicas = var.api_machine_count * 3

    metric {
      type = "Resource"

      resource {
        name = "cpu"

        target {
          type                = "Utilization"
          average_utilization = 70
        }
      }
    }
  }
}

# --- Client Proxy Deployment ---
resource "kubernetes_deployment_v1" "client_proxy" {
  metadata {
    name      = "client-proxy"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "client-proxy" })
  }

  spec {
    replicas = var.client_proxy_count

    selector {
      match_labels = { "app.kubernetes.io/name" = "client-proxy" }
    }

    strategy {
      type = "RollingUpdate"
      rolling_update {
        max_unavailable = var.client_proxy_update_max_parallel
      }
    }

    template {
      metadata {
        labels = merge(local.common_labels, { "app.kubernetes.io/name" = "client-proxy" })
      }

      spec {
        node_selector = {
          "e2b.dev/node-pool" = "system"
        }

        affinity {
          pod_anti_affinity {
            preferred_during_scheduling_ignored_during_execution {
              weight = 100
              pod_affinity_term {
                label_selector {
                  match_labels = { "app.kubernetes.io/name" = "client-proxy" }
                }
                topology_key = "kubernetes.io/hostname"
              }
            }
          }
        }

        container {
          name  = "client-proxy"
          image = "${var.core_repository_url}:client-proxy-latest"

          port {
            container_port = var.client_proxy_session_port
            name           = "session"
          }

          port {
            container_port = var.client_proxy_health_port
            name           = "health"
          }

          env {
            name  = "ENVIRONMENT"
            value = var.environment
          }

          env {
            name  = "REDIS_URL"
            value = local.redis_url
          }

          env {
            name  = "REDIS_CLUSTER_URL"
            value = local.redis_cluster_url
          }

          env {
            name  = "API_GRPC_ADDRESS"
            value = "api.e2b.svc.cluster.local:${var.api_grpc_port}"
          }

          env {
            name  = "OTEL_COLLECTOR_GRPC_ENDPOINT"
            value = "localhost:${var.otel_collector_grpc_port}"
          }

          env {
            name  = "LOGS_COLLECTOR_ADDRESS"
            value = "http://localhost:${var.logs_proxy_port.port}"
          }

          env_from {
            secret_ref {
              name = kubernetes_secret_v1.e2b_secrets.metadata[0].name
            }
          }

          resources {
            requests = {
              cpu    = "${var.client_proxy_resources_cpu_count}"
              memory = "${var.client_proxy_resources_memory_mb}Mi"
            }
            limits = {
              cpu    = "${var.client_proxy_resources_cpu_count}"
              memory = "${var.client_proxy_resources_memory_mb}Mi"
            }
          }

          liveness_probe {
            http_get {
              path = "/health"
              port = var.client_proxy_health_port
            }
            initial_delay_seconds = 10
            period_seconds        = 10
          }

          readiness_probe {
            http_get {
              path = "/health"
              port = var.client_proxy_health_port
            }
            initial_delay_seconds = 5
            period_seconds        = 5
          }
        }
      }
    }
  }
}

# --- Client Proxy Service ---
resource "kubernetes_service_v1" "client_proxy" {
  metadata {
    name      = "client-proxy"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "client-proxy" })
  }

  spec {
    selector = { "app.kubernetes.io/name" = "client-proxy" }
    type     = "NodePort"

    port {
      name        = "session"
      port        = var.client_proxy_session_port
      target_port = var.client_proxy_session_port
    }
  }
}

# --- Client Proxy Horizontal Pod Autoscaler ---
resource "kubernetes_horizontal_pod_autoscaler_v2" "client_proxy" {
  metadata {
    name      = "client-proxy"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "client-proxy" })
  }

  spec {
    scale_target_ref {
      api_version = "apps/v1"
      kind        = "Deployment"
      name        = kubernetes_deployment_v1.client_proxy.metadata[0].name
    }

    min_replicas = var.client_proxy_count
    max_replicas = var.client_proxy_count * 3

    metric {
      type = "Resource"

      resource {
        name = "cpu"

        target {
          type                = "Utilization"
          average_utilization = 70
        }
      }
    }
  }
}

# --- Docker Reverse Proxy Deployment ---
resource "kubernetes_deployment_v1" "docker_reverse_proxy" {
  metadata {
    name      = "docker-reverse-proxy"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "docker-reverse-proxy" })
  }

  spec {
    replicas = var.docker_reverse_proxy_count

    selector {
      match_labels = { "app.kubernetes.io/name" = "docker-reverse-proxy" }
    }

    template {
      metadata {
        labels = merge(local.common_labels, { "app.kubernetes.io/name" = "docker-reverse-proxy" })
      }

      spec {
        node_selector = {
          "e2b.dev/node-pool" = "system"
        }

        container {
          name  = "docker-reverse-proxy"
          image = "${var.core_repository_url}:docker-reverse-proxy-latest"

          port {
            container_port = var.docker_reverse_proxy_port.port
            name           = "http"
          }

          env {
            name  = "AWS_REGION"
            value = var.aws_region
          }

          env {
            name  = "ECR_REPOSITORY_URL"
            value = var.core_repository_url
          }

          env {
            name  = "PORT"
            value = tostring(var.docker_reverse_proxy_port.port)
          }

          env {
            name  = "DOMAIN_NAME"
            value = var.domain_name
          }

          env_from {
            secret_ref {
              name = kubernetes_secret_v1.e2b_secrets.metadata[0].name
            }
          }

          liveness_probe {
            http_get {
              path = var.docker_reverse_proxy_port.health_path
              port = var.docker_reverse_proxy_port.port
            }
            initial_delay_seconds = 10
            period_seconds        = 10
          }
        }
      }
    }
  }
}

# --- Docker Reverse Proxy Service ---
resource "kubernetes_service_v1" "docker_reverse_proxy" {
  metadata {
    name      = "docker-reverse-proxy"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "docker-reverse-proxy" })
  }

  spec {
    selector = { "app.kubernetes.io/name" = "docker-reverse-proxy" }
    type     = "NodePort"

    port {
      name        = "http"
      port        = var.docker_reverse_proxy_port.port
      target_port = var.docker_reverse_proxy_port.port
    }
  }
}

# --- Ingress Deployment ---
resource "kubernetes_deployment_v1" "ingress" {
  metadata {
    name      = "ingress"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "ingress" })
  }

  spec {
    replicas = var.ingress_count

    selector {
      match_labels = { "app.kubernetes.io/name" = "ingress" }
    }

    template {
      metadata {
        labels = merge(local.common_labels, { "app.kubernetes.io/name" = "ingress" })
      }

      spec {
        node_selector = {
          "e2b.dev/node-pool" = "system"
        }

        container {
          name  = "ingress"
          image = "${var.core_repository_url}:ingress-latest"

          port {
            container_port = var.ingress_port.port
            name           = "http"
          }

          env {
            name  = "PORT"
            value = tostring(var.ingress_port.port)
          }

          env {
            name  = "OTEL_COLLECTOR_GRPC_ENDPOINT"
            value = "localhost:${var.otel_collector_grpc_port}"
          }

          liveness_probe {
            http_get {
              path = var.ingress_port.health_path
              port = var.ingress_port.port
            }
            initial_delay_seconds = 10
            period_seconds        = 10
          }
        }
      }
    }
  }
}

# --- Ingress Service ---
resource "kubernetes_service_v1" "ingress" {
  metadata {
    name      = "ingress"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "ingress" })
  }

  spec {
    selector = { "app.kubernetes.io/name" = "ingress" }
    type     = "NodePort"

    port {
      name        = "http"
      port        = var.ingress_port.port
      target_port = var.ingress_port.port
    }
  }
}

# --- PodDisruptionBudgets ---
resource "kubernetes_pod_disruption_budget_v1" "api" {
  metadata {
    name      = "api"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "api" })
  }

  spec {
    max_unavailable = "1"

    selector {
      match_labels = { "app.kubernetes.io/name" = "api" }
    }
  }
}

resource "kubernetes_pod_disruption_budget_v1" "client_proxy" {
  metadata {
    name      = "client-proxy"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "client-proxy" })
  }

  spec {
    max_unavailable = "1"

    selector {
      match_labels = { "app.kubernetes.io/name" = "client-proxy" }
    }
  }
}

resource "kubernetes_pod_disruption_budget_v1" "ingress" {
  metadata {
    name      = "ingress"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "ingress" })
  }

  spec {
    max_unavailable = "1"

    selector {
      match_labels = { "app.kubernetes.io/name" = "ingress" }
    }
  }
}

resource "kubernetes_pod_disruption_budget_v1" "clickhouse" {
  count = var.clickhouse_server_count > 0 ? 1 : 0

  metadata {
    name      = "clickhouse"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "clickhouse" })
  }

  spec {
    max_unavailable = "1"

    selector {
      match_labels = { "app.kubernetes.io/name" = "clickhouse" }
    }
  }
}

# --- NetworkPolicy for e2b namespace ---
resource "kubernetes_network_policy_v1" "e2b" {
  metadata {
    name      = "e2b-default"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = local.common_labels
  }

  spec {
    pod_selector {}

    # Allow all traffic within e2b namespace
    ingress {
      from {
        namespace_selector {
          match_labels = {
            "kubernetes.io/metadata.name" = "e2b"
          }
        }
      }
    }

    # Allow ingress from ALB (kube-system for AWS LB controller, or node traffic)
    ingress {
      from {
        ip_block {
          cidr = "10.0.0.0/16"
        }
      }

      ports {
        port     = "50001"
        protocol = "TCP"
      }
      ports {
        port     = "8800"
        protocol = "TCP"
      }
      ports {
        port     = "5000"
        protocol = "TCP"
      }
      ports {
        port     = "3002"
        protocol = "TCP"
      }
      ports {
        port     = "3001"
        protocol = "TCP"
      }
    }

    # Allow Temporal namespace to reach API gRPC
    ingress {
      from {
        namespace_selector {
          match_labels = {
            "kubernetes.io/metadata.name" = "temporal"
          }
        }
      }

      ports {
        port     = "5009"
        protocol = "TCP"
      }
    }

    # Allow DNS from kube-system
    egress {
      to {
        namespace_selector {
          match_labels = {
            "kubernetes.io/metadata.name" = "kube-system"
          }
        }
      }

      ports {
        port     = "53"
        protocol = "TCP"
      }
      ports {
        port     = "53"
        protocol = "UDP"
      }
    }

    # Allow all egress within e2b namespace
    egress {
      to {
        namespace_selector {
          match_labels = {
            "kubernetes.io/metadata.name" = "e2b"
          }
        }
      }
    }

    # Allow egress to Temporal namespace
    egress {
      to {
        namespace_selector {
          match_labels = {
            "kubernetes.io/metadata.name" = "temporal"
          }
        }
      }

      ports {
        port     = "7233"
        protocol = "TCP"
      }
    }

    # Allow egress to VPC CIDR (for AWS services, RDS, ElastiCache, etc.)
    egress {
      to {
        ip_block {
          cidr = "10.0.0.0/16"
        }
      }
    }

    # Allow HTTPS egress to internet (external APIs, S3, etc.) excluding private ranges
    egress {
      to {
        ip_block {
          cidr = "0.0.0.0/0"
          except = [
            "10.0.0.0/8",
            "172.16.0.0/12",
            "192.168.0.0/16",
          ]
        }
      }

      ports {
        port     = "443"
        protocol = "TCP"
      }
    }

    policy_types = ["Ingress", "Egress"]
  }
}

# --- Orchestrator DaemonSet (runs on client nodes) ---
data "aws_s3_object" "orchestrator" {
  bucket = var.fc_env_pipeline_bucket_name
  key    = "orchestrator"
}

locals {
  orchestrator_checksum = replace(data.aws_s3_object.orchestrator.etag, "\"", "")
}

resource "kubernetes_daemon_set_v1" "orchestrator" {
  metadata {
    name      = "orchestrator"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "orchestrator" })
  }

  spec {
    selector {
      match_labels = { "app.kubernetes.io/name" = "orchestrator" }
    }

    template {
      metadata {
        labels = merge(local.common_labels, {
          "app.kubernetes.io/name" = "orchestrator"
          "orchestrator-checksum"  = local.orchestrator_checksum
        })
      }

      spec {
        host_network = true
        dns_policy   = "ClusterFirstWithHostNet"

        toleration {
          key      = "e2b.dev/node-pool"
          value    = "client"
          operator = "Equal"
          effect   = "NoSchedule"
        }

        node_selector = {
          "e2b.dev/node-pool" = "client"
        }

        # Privileged for Firecracker KVM/NBD access
        container {
          name  = "orchestrator"
          image = "${var.core_repository_url}:orchestrator-latest"

          security_context {
            privileged = true
          }

          port {
            container_port = var.orchestrator_port
            host_port      = var.orchestrator_port
            name           = "grpc"
          }

          port {
            container_port = var.orchestrator_proxy_port
            host_port      = var.orchestrator_proxy_port
            name           = "proxy"
          }

          env {
            name  = "PORT"
            value = tostring(var.orchestrator_port)
          }

          env {
            name  = "PROXY_PORT"
            value = tostring(var.orchestrator_proxy_port)
          }

          env {
            name  = "ENVIRONMENT"
            value = var.environment
          }

          env {
            name  = "OTEL_COLLECTOR_GRPC_ENDPOINT"
            value = "localhost:${var.otel_collector_grpc_port}"
          }

          env {
            name  = "LOGS_COLLECTOR_ADDRESS"
            value = "http://localhost:${var.logs_proxy_port.port}"
          }

          env {
            name  = "ENVD_TIMEOUT"
            value = var.envd_timeout
          }

          env {
            name  = "TEMPLATE_BUCKET_NAME"
            value = var.template_bucket_name
          }

          env {
            name  = "ALLOW_SANDBOX_INTERNET"
            value = tostring(var.allow_sandbox_internet)
          }

          env {
            name  = "CLICKHOUSE_CONNECTION_STRING"
            value = local.clickhouse_connection_string
          }

          env {
            name  = "REDIS_URL"
            value = local.redis_url
          }

          env {
            name  = "REDIS_CLUSTER_URL"
            value = local.redis_cluster_url
          }

          env {
            name  = "DOMAIN_NAME"
            value = var.domain_name
          }

          env {
            name  = "SHARED_CHUNK_CACHE_PATH"
            value = var.shared_chunk_cache_path
          }

          env {
            name  = "AWS_REGION"
            value = var.aws_region
          }

          env {
            name  = "DOCKER_REPOSITORY_NAME"
            value = var.core_repository_url
          }

          env_from {
            secret_ref {
              name = kubernetes_secret_v1.e2b_secrets.metadata[0].name
            }
          }

          volume_mount {
            name       = "dev-kvm"
            mount_path = "/dev/kvm"
          }

          volume_mount {
            name       = "cache"
            mount_path = "/mnt/cache"
          }
        }

        volume {
          name = "dev-kvm"
          host_path {
            path = "/dev/kvm"
            type = "CharDevice"
          }
        }

        volume {
          name = "cache"
          host_path {
            path = "/mnt/cache"
            type = "DirectoryOrCreate"
          }
        }
      }
    }
  }
}

# --- Orchestrator Service ---
resource "kubernetes_service_v1" "orchestrator" {
  metadata {
    name      = "orchestrator"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "orchestrator" })
  }

  spec {
    selector = { "app.kubernetes.io/name" = "orchestrator" }
    type     = "ClusterIP"

    port {
      name        = "grpc"
      port        = var.orchestrator_port
      target_port = var.orchestrator_port
    }

    port {
      name        = "proxy"
      port        = var.orchestrator_proxy_port
      target_port = var.orchestrator_proxy_port
    }
  }
}

# --- Template Manager Deployment (runs on build nodes) ---
data "aws_s3_object" "template_manager" {
  bucket = var.fc_env_pipeline_bucket_name
  key    = "template-manager"
}

locals {
  template_manager_checksum = replace(data.aws_s3_object.template_manager.etag, "\"", "")
}

resource "kubernetes_deployment_v1" "template_manager" {
  metadata {
    name      = "template-manager"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "template-manager" })
  }

  spec {
    replicas = 1

    selector {
      match_labels = { "app.kubernetes.io/name" = "template-manager" }
    }

    template {
      metadata {
        labels = merge(local.common_labels, {
          "app.kubernetes.io/name"    = "template-manager"
          "template-manager-checksum" = local.template_manager_checksum
        })
      }

      spec {
        toleration {
          key      = "e2b.dev/node-pool"
          value    = "build"
          operator = "Equal"
          effect   = "NoSchedule"
        }

        node_selector = {
          "e2b.dev/node-pool" = "build"
        }

        # Privileged for Firecracker template builds
        container {
          name  = "template-manager"
          image = "${var.core_repository_url}:template-manager-latest"

          security_context {
            privileged = true
          }

          port {
            container_port = var.template_manager_port
            name           = "http"
          }

          env {
            name  = "PORT"
            value = tostring(var.template_manager_port)
          }

          env {
            name  = "ENVIRONMENT"
            value = var.environment
          }

          env {
            name  = "AWS_REGION"
            value = var.aws_region
          }

          env {
            name  = "ECR_REPOSITORY_URL"
            value = var.core_repository_url
          }

          env {
            name  = "DOMAIN_NAME"
            value = var.domain_name
          }

          env {
            name  = "TEMPLATE_BUCKET_NAME"
            value = var.template_bucket_name
          }

          env {
            name  = "BUILD_CACHE_BUCKET_NAME"
            value = var.build_cache_bucket_name
          }

          env {
            name  = "OTEL_COLLECTOR_GRPC_ENDPOINT"
            value = "localhost:${var.otel_collector_grpc_port}"
          }

          env {
            name  = "LOGS_COLLECTOR_ADDRESS"
            value = "http://localhost:${var.logs_proxy_port.port}"
          }

          env {
            name  = "CLICKHOUSE_CONNECTION_STRING"
            value = local.clickhouse_connection_string
          }

          env {
            name  = "SHARED_CHUNK_CACHE_PATH"
            value = var.shared_chunk_cache_path
          }

          env {
            name  = "DOCKERHUB_REMOTE_REPOSITORY_URL"
            value = var.dockerhub_remote_repository_url
          }

          env_from {
            secret_ref {
              name = kubernetes_secret_v1.e2b_secrets.metadata[0].name
            }
          }

          volume_mount {
            name       = "dev-kvm"
            mount_path = "/dev/kvm"
          }

          volume_mount {
            name       = "cache"
            mount_path = "/mnt/cache"
          }
        }

        volume {
          name = "dev-kvm"
          host_path {
            path = "/dev/kvm"
            type = "CharDevice"
          }
        }

        volume {
          name = "cache"
          host_path {
            path = "/mnt/cache"
            type = "DirectoryOrCreate"
          }
        }
      }
    }
  }
}

# --- Redis (self-managed, only when not using ElastiCache) ---
resource "kubernetes_deployment_v1" "redis" {
  count = var.redis_managed ? 0 : 1

  metadata {
    name      = "redis"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "redis" })
  }

  spec {
    replicas = 1

    selector {
      match_labels = { "app.kubernetes.io/name" = "redis" }
    }

    template {
      metadata {
        labels = merge(local.common_labels, { "app.kubernetes.io/name" = "redis" })
      }

      spec {
        node_selector = {
          "e2b.dev/node-pool" = "system"
        }

        container {
          name  = "redis"
          image = "redis:7-alpine"

          port {
            container_port = var.redis_port.port
            name           = "redis"
          }

          resources {
            requests = {
              cpu    = "0.5"
              memory = "512Mi"
            }
            limits = {
              cpu    = "1"
              memory = "1Gi"
            }
          }
        }
      }
    }
  }
}

resource "kubernetes_service_v1" "redis" {
  count = var.redis_managed ? 0 : 1

  metadata {
    name      = "redis"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "redis" })
  }

  spec {
    selector = { "app.kubernetes.io/name" = "redis" }
    type     = "ClusterIP"

    port {
      name        = "redis"
      port        = var.redis_port.port
      target_port = var.redis_port.port
    }
  }
}

# --- ClickHouse StatefulSet ---
resource "kubernetes_stateful_set_v1" "clickhouse" {
  count = var.clickhouse_server_count > 0 ? 1 : 0

  metadata {
    name      = "clickhouse"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "clickhouse" })
  }

  spec {
    service_name = "clickhouse"
    replicas     = var.clickhouse_server_count

    selector {
      match_labels = { "app.kubernetes.io/name" = "clickhouse" }
    }

    template {
      metadata {
        labels = merge(local.common_labels, { "app.kubernetes.io/name" = "clickhouse" })
      }

      spec {
        node_selector = {
          "e2b.dev/node-pool" = "system"
        }

        init_container {
          name  = "clickhouse-migrator"
          image = "${var.core_repository_url}:clickhouse-migrator-latest"

          env {
            name  = "CLICKHOUSE_CONNECTION_STRING"
            value = local.clickhouse_connection_string
          }
        }

        container {
          name  = "clickhouse"
          image = "clickhouse/clickhouse-server:24.1"

          port {
            container_port = var.clickhouse_server_port.port
            name           = "native"
          }

          port {
            container_port = 8123
            name           = "http"
          }

          env {
            name  = "CLICKHOUSE_USER"
            value = var.clickhouse_username
          }

          env {
            name = "CLICKHOUSE_PASSWORD"
            value_from {
              secret_key_ref {
                name = kubernetes_secret_v1.clickhouse_credentials.metadata[0].name
                key  = "CLICKHOUSE_PASSWORD"
              }
            }
          }

          env {
            name  = "CLICKHOUSE_DB"
            value = var.clickhouse_database
          }

          resources {
            requests = {
              cpu    = "${var.clickhouse_resources_cpu_count}"
              memory = "${var.clickhouse_resources_memory_mb}Mi"
            }
            limits = {
              cpu    = "${var.clickhouse_resources_cpu_count}"
              memory = "${var.clickhouse_resources_memory_mb}Mi"
            }
          }

          volume_mount {
            name       = "clickhouse-data"
            mount_path = "/var/lib/clickhouse"
          }

          liveness_probe {
            http_get {
              path = "/ping"
              port = 8123
            }
            initial_delay_seconds = 30
            period_seconds        = 10
          }

          readiness_probe {
            http_get {
              path = "/ping"
              port = 8123
            }
            initial_delay_seconds = 5
            period_seconds        = 5
          }
        }
      }
    }

    volume_claim_template {
      metadata {
        name = "clickhouse-data"
      }

      spec {
        access_modes       = ["ReadWriteOnce"]
        storage_class_name = kubernetes_storage_class_v1.gp3.metadata[0].name

        resources {
          requests = {
            storage = "100Gi"
          }
        }
      }
    }
  }
}

resource "kubernetes_service_v1" "clickhouse" {
  count = var.clickhouse_server_count > 0 ? 1 : 0

  metadata {
    name      = "clickhouse"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "clickhouse" })
  }

  spec {
    selector = { "app.kubernetes.io/name" = "clickhouse" }
    type     = "ClusterIP"

    port {
      name        = "native"
      port        = var.clickhouse_server_port.port
      target_port = var.clickhouse_server_port.port
    }

    port {
      name        = "http"
      port        = 8123
      target_port = 8123
    }
  }
}

# --- ClickHouse Backup CronJob ---
resource "kubernetes_cron_job_v1" "clickhouse_backup" {
  count = var.clickhouse_server_count > 0 ? 1 : 0

  metadata {
    name      = "clickhouse-backup"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "clickhouse-backup" })
  }

  spec {
    schedule                      = "0 2 * * *"
    successful_jobs_history_limit = 3
    failed_jobs_history_limit     = 3
    concurrency_policy            = "Forbid"

    job_template {
      metadata {
        labels = merge(local.common_labels, { "app.kubernetes.io/name" = "clickhouse-backup" })
      }

      spec {
        backoff_limit = 2

        template {
          metadata {
            labels = merge(local.common_labels, { "app.kubernetes.io/name" = "clickhouse-backup" })
          }

          spec {
            node_selector = {
              "e2b.dev/node-pool" = "system"
            }

            restart_policy = "OnFailure"

            container {
              name  = "backup"
              image = "clickhouse/clickhouse-server:24.1"

              command = ["/bin/sh", "-c"]
              args = [
                <<-EOT
                clickhouse-client --host clickhouse.e2b.svc.cluster.local \
                  --port ${var.clickhouse_server_port.port} \
                  --user ${var.clickhouse_username} \
                  --password "$CLICKHOUSE_PASSWORD" \
                  --query "BACKUP DATABASE ${var.clickhouse_database} TO S3('https://${var.clickhouse_backups_bucket_name}.s3.amazonaws.com/daily/$(date +%%Y-%%m-%%d)', '$AWS_ACCESS_KEY_ID', '$AWS_SECRET_ACCESS_KEY')"
                EOT
              ]

              env {
                name = "CLICKHOUSE_PASSWORD"
                value_from {
                  secret_key_ref {
                    name = kubernetes_secret_v1.clickhouse_credentials.metadata[0].name
                    key  = "CLICKHOUSE_PASSWORD"
                  }
                }
              }

              env {
                name  = "AWS_REGION"
                value = var.aws_region
              }
            }
          }
        }
      }
    }
  }
}

# --- Loki Deployment ---
resource "kubernetes_deployment_v1" "loki" {
  metadata {
    name      = "loki"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "loki" })
  }

  spec {
    replicas = var.loki_machine_count > 0 ? var.loki_machine_count : 1

    selector {
      match_labels = { "app.kubernetes.io/name" = "loki" }
    }

    template {
      metadata {
        labels = merge(local.common_labels, { "app.kubernetes.io/name" = "loki" })
      }

      spec {
        node_selector = {
          "e2b.dev/node-pool" = "system"
        }

        container {
          name  = "loki"
          image = "grafana/loki:2.9.4"

          port {
            container_port = var.loki_service_port.port
            name           = "http"
          }

          env {
            name  = "AWS_REGION"
            value = var.aws_region
          }

          env {
            name  = "S3_BUCKET"
            value = var.loki_bucket_name
          }

          resources {
            requests = {
              cpu    = "${var.loki_resources_cpu_count}"
              memory = "${var.loki_resources_memory_mb}Mi"
            }
            limits = {
              cpu    = "${var.loki_resources_cpu_count}"
              memory = "${var.loki_resources_memory_mb}Mi"
            }
          }

          liveness_probe {
            http_get {
              path = "/ready"
              port = var.loki_service_port.port
            }
            initial_delay_seconds = 30
            period_seconds        = 10
          }

          readiness_probe {
            http_get {
              path = "/ready"
              port = var.loki_service_port.port
            }
            initial_delay_seconds = 5
            period_seconds        = 5
          }
        }
      }
    }
  }
}

resource "kubernetes_service_v1" "loki" {
  metadata {
    name      = "loki"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "loki" })
  }

  spec {
    selector = { "app.kubernetes.io/name" = "loki" }
    type     = "ClusterIP"

    port {
      name        = "http"
      port        = var.loki_service_port.port
      target_port = var.loki_service_port.port
    }
  }
}

# --- OTel Collector DaemonSet ---
resource "kubernetes_daemon_set_v1" "otel_collector" {
  metadata {
    name      = "otel-collector"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "otel-collector" })
  }

  spec {
    selector {
      match_labels = { "app.kubernetes.io/name" = "otel-collector" }
    }

    template {
      metadata {
        labels = merge(local.common_labels, { "app.kubernetes.io/name" = "otel-collector" })
      }

      spec {
        # Run on all nodes
        toleration {
          operator = "Exists"
        }

        container {
          name  = "otel-collector"
          image = "otel/opentelemetry-collector-contrib:0.96.0"

          port {
            container_port = var.otel_collector_grpc_port
            host_port      = var.otel_collector_grpc_port
            name           = "grpc"
          }

          env {
            name = "GRAFANA_OTEL_COLLECTOR_TOKEN"
            value_from {
              secret_key_ref {
                name = kubernetes_secret_v1.otel_credentials.metadata[0].name
                key  = "GRAFANA_OTEL_COLLECTOR_TOKEN"
              }
            }
          }

          env {
            name = "GRAFANA_OTLP_URL"
            value_from {
              secret_key_ref {
                name = kubernetes_secret_v1.otel_credentials.metadata[0].name
                key  = "GRAFANA_OTLP_URL"
              }
            }
          }

          env {
            name = "GRAFANA_USERNAME"
            value_from {
              secret_key_ref {
                name = kubernetes_secret_v1.otel_credentials.metadata[0].name
                key  = "GRAFANA_USERNAME"
              }
            }
          }

          env {
            name  = "CLICKHOUSE_USERNAME"
            value = var.clickhouse_username
          }

          env {
            name = "CLICKHOUSE_PASSWORD"
            value_from {
              secret_key_ref {
                name = kubernetes_secret_v1.clickhouse_credentials.metadata[0].name
                key  = "CLICKHOUSE_PASSWORD"
              }
            }
          }

          env {
            name  = "CLICKHOUSE_PORT"
            value = tostring(var.clickhouse_server_port.port)
          }

          env {
            name  = "CLICKHOUSE_DATABASE"
            value = var.clickhouse_database
          }

          resources {
            requests = {
              cpu    = "${var.otel_collector_resources_cpu_count}"
              memory = "${var.otel_collector_resources_memory_mb}Mi"
            }
            limits = {
              cpu    = "${var.otel_collector_resources_cpu_count}"
              memory = "${var.otel_collector_resources_memory_mb}Mi"
            }
          }
        }
      }
    }
  }
}

# --- Logs Collector DaemonSet ---
resource "kubernetes_daemon_set_v1" "logs_collector" {
  metadata {
    name      = "logs-collector"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "logs-collector" })
  }

  spec {
    selector {
      match_labels = { "app.kubernetes.io/name" = "logs-collector" }
    }

    template {
      metadata {
        labels = merge(local.common_labels, { "app.kubernetes.io/name" = "logs-collector" })
      }

      spec {
        # Run on all nodes
        toleration {
          operator = "Exists"
        }

        container {
          name  = "logs-collector"
          image = "timberio/vector:0.36.0-alpine"

          port {
            container_port = var.logs_proxy_port.port
            host_port      = var.logs_proxy_port.port
            name           = "api"
          }

          port {
            container_port = var.logs_health_proxy_port.port
            host_port      = var.logs_health_proxy_port.port
            name           = "health"
          }

          env {
            name  = "LOKI_ENDPOINT"
            value = "http://loki.e2b.svc.cluster.local:${var.loki_service_port.port}"
          }

          env {
            name  = "GRAFANA_LOGS_USER"
            value = trimspace(data.aws_secretsmanager_secret_version.grafana_logs_user.secret_string)
          }

          env {
            name  = "GRAFANA_LOGS_ENDPOINT"
            value = trimspace(data.aws_secretsmanager_secret_version.grafana_logs_url.secret_string)
          }

          env {
            name  = "GRAFANA_API_KEY"
            value = trimspace(data.aws_secretsmanager_secret_version.grafana_logs_collector_api_token.secret_string)
          }
        }
      }
    }
  }
}

# --- EFS/NFS Cache Cleanup CronJob ---
data "aws_s3_object" "clean_nfs_cache" {
  count = var.shared_chunk_cache_path != "" ? 1 : 0

  bucket = var.fc_env_pipeline_bucket_name
  key    = "clean-nfs-cache"
}

resource "kubernetes_cron_job_v1" "efs_cleanup" {
  count = var.shared_chunk_cache_path != "" ? 1 : 0

  metadata {
    name      = "efs-cleanup"
    namespace = kubernetes_namespace_v1.e2b.metadata[0].name
    labels    = merge(local.common_labels, { "app.kubernetes.io/name" = "efs-cleanup" })
  }

  spec {
    schedule = "*/30 * * * *"

    job_template {
      metadata {
        labels = merge(local.common_labels, { "app.kubernetes.io/name" = "efs-cleanup" })
      }

      spec {
        template {
          metadata {
            labels = merge(local.common_labels, { "app.kubernetes.io/name" = "efs-cleanup" })
          }

          spec {
            toleration {
              key      = "e2b.dev/node-pool"
              value    = "client"
              operator = "Equal"
              effect   = "NoSchedule"
            }

            node_selector = {
              "e2b.dev/node-pool" = "client"
            }

            restart_policy = "OnFailure"

            container {
              name  = "cleanup"
              image = "${var.core_repository_url}:clean-nfs-cache-latest"

              env {
                name  = "NFS_CACHE_MOUNT_PATH"
                value = var.shared_chunk_cache_path
              }

              env {
                name  = "DRY_RUN"
                value = tostring(var.filestore_cache_cleanup_dry_run)
              }

              env {
                name  = "MAX_DISK_USAGE_TARGET"
                value = tostring(var.filestore_cache_cleanup_disk_usage_target)
              }

              env {
                name  = "FILES_PER_LOOP"
                value = tostring(var.filestore_cache_cleanup_files_per_loop)
              }

              env {
                name  = "DELETIONS_PER_LOOP"
                value = tostring(var.filestore_cache_cleanup_deletions_per_loop)
              }

              env {
                name  = "MAX_CONCURRENT_STAT"
                value = tostring(var.filestore_cache_cleanup_max_concurrent_stat)
              }

              env {
                name  = "MAX_CONCURRENT_SCAN"
                value = tostring(var.filestore_cache_cleanup_max_concurrent_scan)
              }

              env {
                name  = "MAX_CONCURRENT_DELETE"
                value = tostring(var.filestore_cache_cleanup_max_concurrent_delete)
              }

              env {
                name  = "MAX_RETRIES"
                value = tostring(var.filestore_cache_cleanup_max_retries)
              }

              env_from {
                secret_ref {
                  name = kubernetes_secret_v1.e2b_secrets.metadata[0].name
                }
              }

              volume_mount {
                name       = "efs-cache"
                mount_path = var.shared_chunk_cache_path
              }
            }

            volume {
              name = "efs-cache"
              host_path {
                path = var.shared_chunk_cache_path
                type = "Directory"
              }
            }
          }
        }
      }
    }
  }
}
