# --- Temporal Server for Multi-Agent Orchestration ---
#
# Deploys Temporal Server on EKS via the official Helm chart.
# Persistence: Aurora PostgreSQL (two databases: temporal, temporal_visibility)
# Security: mTLS for internode + frontend, NetworkPolicy for namespace isolation
#
# IMPORTANT: numHistoryShards (512) is IMMUTABLE after first deploy.
# Pre-requisite: Create databases and user on Aurora before enabling this module.
#   CREATE DATABASE temporal;
#   CREATE DATABASE temporal_visibility;
#   CREATE USER temporal WITH PASSWORD '<from secrets manager>';
#   GRANT ALL PRIVILEGES ON DATABASE temporal TO temporal;
#   GRANT ALL PRIVILEGES ON DATABASE temporal_visibility TO temporal;

terraform {
  required_providers {
    tls = {
      source  = "hashicorp/tls"
      version = ">= 4.0"
    }
  }
}

# --- Namespace ---

resource "kubernetes_namespace_v1" "temporal" {
  metadata {
    name = "temporal"

    labels = {
      "app.kubernetes.io/managed-by" = "terraform"
      "app.kubernetes.io/part-of"    = "temporal"
    }
  }
}

# --- Database Credentials ---

resource "random_password" "temporal_db_password" {
  length           = 32
  special          = true
  override_special = "!#$%&*()-_=+[]{}|:,.<>?"

  lifecycle {
    ignore_changes = [special, override_special]
  }
}

resource "aws_secretsmanager_secret" "temporal_db_password" {
  name                    = "${var.prefix}temporal-db-password"
  recovery_window_in_days = 7
  tags                    = var.tags
}

resource "aws_secretsmanager_secret_version" "temporal_db_password" {
  secret_id     = aws_secretsmanager_secret.temporal_db_password.id
  secret_string = random_password.temporal_db_password.result
}

resource "kubernetes_secret_v1" "temporal_db" {
  metadata {
    name      = "temporal-db"
    namespace = kubernetes_namespace_v1.temporal.metadata[0].name
  }

  data = {
    PASSWORD = random_password.temporal_db_password.result
  }
}

# --- mTLS Certificate Authority ---

resource "tls_private_key" "temporal_ca" {
  algorithm = "RSA"
  rsa_bits  = 4096
}

resource "tls_self_signed_cert" "temporal_ca" {
  private_key_pem = tls_private_key.temporal_ca.private_key_pem

  subject {
    common_name  = "Temporal Internal CA"
    organization = "E2B"
  }

  validity_period_hours = 87600 # 10 years
  is_ca_certificate     = true

  allowed_uses = [
    "cert_signing",
    "crl_signing",
  ]
}

# --- Internode mTLS Certificate ---

resource "tls_private_key" "temporal_internode" {
  algorithm = "RSA"
  rsa_bits  = 4096
}

resource "tls_cert_request" "temporal_internode" {
  private_key_pem = tls_private_key.temporal_internode.private_key_pem

  subject {
    common_name  = "temporal-internode"
    organization = "E2B"
  }

  dns_names = [
    "*.temporal.svc.cluster.local",
    "temporal-frontend",
    "temporal-frontend-headless",
    "temporal-history",
    "temporal-history-headless",
    "temporal-matching",
    "temporal-matching-headless",
    "temporal-worker",
    "temporal-worker-headless",
  ]
}

resource "tls_locally_signed_cert" "temporal_internode" {
  cert_request_pem   = tls_cert_request.temporal_internode.cert_request_pem
  ca_private_key_pem = tls_private_key.temporal_ca.private_key_pem
  ca_cert_pem        = tls_self_signed_cert.temporal_ca.cert_pem

  validity_period_hours = var.temporal_cert_validity_hours

  allowed_uses = [
    "digital_signature",
    "key_encipherment",
    "server_auth",
    "client_auth",
  ]
}

# --- Frontend mTLS Certificate ---

resource "tls_private_key" "temporal_frontend" {
  algorithm = "RSA"
  rsa_bits  = 4096
}

resource "tls_cert_request" "temporal_frontend" {
  private_key_pem = tls_private_key.temporal_frontend.private_key_pem

  subject {
    common_name  = "temporal-frontend"
    organization = "E2B"
  }

  dns_names = [
    "temporal-frontend.temporal.svc.cluster.local",
    "temporal-frontend",
    "temporal-frontend-headless",
  ]
}

resource "tls_locally_signed_cert" "temporal_frontend" {
  cert_request_pem   = tls_cert_request.temporal_frontend.cert_request_pem
  ca_private_key_pem = tls_private_key.temporal_ca.private_key_pem
  ca_cert_pem        = tls_self_signed_cert.temporal_ca.cert_pem

  validity_period_hours = var.temporal_cert_validity_hours

  allowed_uses = [
    "digital_signature",
    "key_encipherment",
    "server_auth",
    "client_auth",
  ]
}

# --- TLS Kubernetes Secret ---

resource "kubernetes_secret_v1" "temporal_tls" {
  metadata {
    name      = "temporal-tls"
    namespace = kubernetes_namespace_v1.temporal.metadata[0].name
  }

  data = {
    "ca.crt"            = tls_self_signed_cert.temporal_ca.cert_pem
    "internode-tls.crt" = tls_locally_signed_cert.temporal_internode.cert_pem
    "internode-tls.key" = tls_private_key.temporal_internode.private_key_pem
    "frontend-tls.crt"  = tls_locally_signed_cert.temporal_frontend.cert_pem
    "frontend-tls.key"  = tls_private_key.temporal_frontend.private_key_pem
  }
}

# --- Network Policy ---

resource "kubernetes_network_policy_v1" "temporal" {
  metadata {
    name      = "temporal-access"
    namespace = kubernetes_namespace_v1.temporal.metadata[0].name
  }

  spec {
    pod_selector {}

    # Allow all traffic within temporal namespace (internode communication)
    ingress {
      from {
        namespace_selector {
          match_labels = {
            "kubernetes.io/metadata.name" = "temporal"
          }
        }
      }
    }

    # Allow Frontend (7233) and Web UI (8080) from e2b namespace
    ingress {
      from {
        namespace_selector {
          match_labels = {
            "kubernetes.io/metadata.name" = "e2b"
          }
        }
      }

      ports {
        port     = "7233"
        protocol = "TCP"
      }

      ports {
        port     = "8080"
        protocol = "TCP"
      }
    }

    policy_types = ["Ingress"]
  }
}

# --- Helm Release ---

resource "helm_release" "temporal" {
  name       = "temporal"
  repository = "https://go.temporal.io/helm-charts"
  chart      = "temporal"
  version    = var.temporal_chart_version
  namespace  = kubernetes_namespace_v1.temporal.metadata[0].name
  wait       = true
  timeout    = 900

  values = [
    yamlencode({
      server = {
        config = {
          numHistoryShards = 512

          persistence = {
            default = {
              driver = "sql"

              sql = {
                driver            = "postgres12"
                host              = var.aurora_host
                port              = var.aurora_port
                database          = "temporal"
                user              = var.temporal_db_user
                existingSecret    = kubernetes_secret_v1.temporal_db.metadata[0].name
                secretPasswordKey = "PASSWORD"
              }
            }

            visibility = {
              driver = "sql"

              sql = {
                driver            = "postgres12"
                host              = var.aurora_host
                port              = var.aurora_port
                database          = "temporal_visibility"
                user              = var.temporal_db_user
                existingSecret    = kubernetes_secret_v1.temporal_db.metadata[0].name
                secretPasswordKey = "PASSWORD"
              }
            }
          }

          tls = {
            internode = {
              server = {
                certFile          = "/etc/temporal/tls/internode-tls.crt"
                keyFile           = "/etc/temporal/tls/internode-tls.key"
                requireClientAuth = true
                clientCaFiles     = ["/etc/temporal/tls/ca.crt"]
              }

              client = {
                certFile    = "/etc/temporal/tls/internode-tls.crt"
                keyFile     = "/etc/temporal/tls/internode-tls.key"
                rootCaFiles = ["/etc/temporal/tls/ca.crt"]
              }
            }

            frontend = {
              server = {
                certFile          = "/etc/temporal/tls/frontend-tls.crt"
                keyFile           = "/etc/temporal/tls/frontend-tls.key"
                requireClientAuth = true
                clientCaFiles     = ["/etc/temporal/tls/ca.crt"]
              }

              client = {
                rootCaFiles = ["/etc/temporal/tls/ca.crt"]
              }
            }
          }
        }

        frontend = {
          replicaCount = 2

          resources = {
            requests = { cpu = "500m", memory = "512Mi" }
            limits   = { cpu = "1", memory = "1Gi" }
          }

          podAnnotations = {
            "e2b.dev/cert-hash" = sha256(tls_locally_signed_cert.temporal_frontend.cert_pem)
          }

          nodeSelector = {
            "e2b.dev/node-pool" = "system"
          }

          extraVolumes = [{
            name = "temporal-tls"
            secret = {
              secretName = kubernetes_secret_v1.temporal_tls.metadata[0].name
            }
          }]

          extraVolumeMounts = [{
            name      = "temporal-tls"
            mountPath = "/etc/temporal/tls"
            readOnly  = true
          }]
        }

        history = {
          replicaCount = 2

          resources = {
            requests = { cpu = "500m", memory = "512Mi" }
            limits   = { cpu = "1", memory = "1Gi" }
          }

          podAnnotations = {
            "e2b.dev/cert-hash" = sha256(tls_locally_signed_cert.temporal_internode.cert_pem)
          }

          nodeSelector = {
            "e2b.dev/node-pool" = "system"
          }

          extraVolumes = [{
            name = "temporal-tls"
            secret = {
              secretName = kubernetes_secret_v1.temporal_tls.metadata[0].name
            }
          }]

          extraVolumeMounts = [{
            name      = "temporal-tls"
            mountPath = "/etc/temporal/tls"
            readOnly  = true
          }]
        }

        matching = {
          replicaCount = 2

          resources = {
            requests = { cpu = "250m", memory = "256Mi" }
            limits   = { cpu = "500m", memory = "512Mi" }
          }

          podAnnotations = {
            "e2b.dev/cert-hash" = sha256(tls_locally_signed_cert.temporal_internode.cert_pem)
          }

          nodeSelector = {
            "e2b.dev/node-pool" = "system"
          }

          extraVolumes = [{
            name = "temporal-tls"
            secret = {
              secretName = kubernetes_secret_v1.temporal_tls.metadata[0].name
            }
          }]

          extraVolumeMounts = [{
            name      = "temporal-tls"
            mountPath = "/etc/temporal/tls"
            readOnly  = true
          }]
        }

        worker = {
          replicaCount = var.temporal_worker_replica_count

          resources = {
            requests = { cpu = "250m", memory = "256Mi" }
            limits   = { cpu = "500m", memory = "512Mi" }
          }

          podAnnotations = {
            "e2b.dev/cert-hash" = sha256(tls_locally_signed_cert.temporal_internode.cert_pem)
          }

          nodeSelector = {
            "e2b.dev/node-pool" = "system"
          }

          extraVolumes = [{
            name = "temporal-tls"
            secret = {
              secretName = kubernetes_secret_v1.temporal_tls.metadata[0].name
            }
          }]

          extraVolumeMounts = [{
            name      = "temporal-tls"
            mountPath = "/etc/temporal/tls"
            readOnly  = true
          }]
        }
      }

      web = {
        replicaCount = var.temporal_web_replica_count

        resources = {
          requests = { cpu = "100m", memory = "128Mi" }
          limits   = { cpu = "250m", memory = "256Mi" }
        }

        nodeSelector = {
          "e2b.dev/node-pool" = "system"
        }
      }

      # Schema management (auto-creates tables on first deploy)
      schema = {
        setup = {
          enabled = true
        }
        update = {
          enabled = true
        }
      }

      # Prometheus metrics for OTel scraping
      prometheus = {
        enabled = true
      }

      # Disable unused datastores
      cassandra = {
        enabled = false
      }

      mysql = {
        enabled = false
      }

      elasticsearch = {
        enabled = false
      }

      # Admin tools (tctl)
      admintools = {
        enabled = true

        nodeSelector = {
          "e2b.dev/node-pool" = "system"
        }
      }
    })
  ]

  depends_on = [
    kubernetes_secret_v1.temporal_db,
    kubernetes_secret_v1.temporal_tls,
    kubernetes_network_policy_v1.temporal,
  ]
}
