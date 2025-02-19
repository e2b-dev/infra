# API
data "google_secret_manager_secret_version" "postgres_connection_string" {
  secret = var.postgres_connection_string_secret_name
}

data "google_secret_manager_secret_version" "posthog_api_key" {
  secret = var.posthog_api_key_secret_name
}

# Telemetry
data "google_secret_manager_secret_version" "analytics_collector_host" {
  secret = var.analytics_collector_host_secret_name
}

data "google_secret_manager_secret_version" "analytics_collector_api_token" {
  secret = var.analytics_collector_api_token_secret_name
}

provider "nomad" {
  address      = "https://nomad.${var.domain_name}"
  secret_id    = var.nomad_acl_token_secret
  consul_token = var.consul_acl_token_secret
}

resource "nomad_job" "api" {
  jobspec = templatefile("${path.module}/api.hcl", {
    update_stanza                 = var.api_machine_count > 1
    orchestrator_port             = var.orchestrator_port
    template_manager_address      = "http://template-manager.service.consul:${var.template_manager_port}"
    otel_collector_grpc_endpoint  = "localhost:4317"
    loki_address                  = "http://loki.service.consul:${var.loki_service_port.port}"
    logs_collector_address        = "http://localhost:${var.logs_proxy_port.port}"
    gcp_zone                      = var.gcp_zone
    port_name                     = var.api_port.name
    port_number                   = var.api_port.port
    api_docker_image              = var.api_docker_image_digest
    postgres_connection_string    = data.google_secret_manager_secret_version.postgres_connection_string.secret_data
    posthog_api_key               = data.google_secret_manager_secret_version.posthog_api_key.secret_data
    environment                   = var.environment
    analytics_collector_host      = data.google_secret_manager_secret_version.analytics_collector_host.secret_data
    analytics_collector_api_token = data.google_secret_manager_secret_version.analytics_collector_api_token.secret_data
    otel_tracing_print            = var.otel_tracing_print
    nomad_acl_token               = var.nomad_acl_token_secret
    admin_token                   = var.api_admin_token
    redis_url                     = "redis://redis.service.consul:${var.redis_port.port}"
    dns_port_number               = var.api_dns_port_number
  })
}

resource "nomad_job" "redis" {
  jobspec = templatefile("${path.module}/redis.hcl",
    {
      gcp_zone    = var.gcp_zone
      port_number = var.redis_port.port
      port_name   = var.redis_port.name
    }
  )
}

resource "nomad_job" "docker_reverse_proxy" {
  jobspec = file("${path.module}/docker-reverse-proxy.hcl")

  hcl2 {
    vars = {
      gcp_zone                      = var.gcp_zone
      image_name                    = var.docker_reverse_proxy_docker_image_digest
      postgres_connection_string    = data.google_secret_manager_secret_version.postgres_connection_string.secret_data
      google_service_account_secret = var.docker_reverse_proxy_service_account_key
      port_number                   = var.docker_reverse_proxy_port.port
      port_name                     = var.docker_reverse_proxy_port.name
      health_check_path             = var.docker_reverse_proxy_port.health_path
      domain_name                   = var.domain_name
      gcp_project_id                = var.gcp_project_id
      gcp_region                    = var.gcp_region
      docker_registry               = var.custom_envs_repository_name
    }
  }
}

resource "nomad_job" "client_proxy" {
  jobspec = templatefile("${path.module}/client-proxy.hcl",
    {
      update_stanza = var.api_machine_count > 1

      gcp_zone           = var.gcp_zone
      port_name          = var.client_proxy_port.name
      port_number        = var.client_proxy_port.port
      health_port_number = var.client_proxy_health_port.port
      environment        = var.environment

      image_name = var.client_proxy_docker_image_digest

      otel_collector_grpc_endpoint = "localhost:4317"
  })
}

resource "nomad_job" "session_proxy" {
  jobspec = file("${path.module}/session-proxy.hcl")

  hcl2 {
    vars = {
      gcp_zone                   = var.gcp_zone
      session_proxy_port_number  = var.session_proxy_port.port
      session_proxy_port_name    = var.session_proxy_port.name
      session_proxy_service_name = var.session_proxy_service_name
      load_balancer_conf         = file("${path.module}/proxies/session.conf")
      nginx_conf                 = file("${path.module}/proxies/nginx.conf")
    }
  }
}


variable "prefix" {
  type    = string
  default = "e2b-"
}

# grafana otel collector url

resource "google_secret_manager_secret" "grafana_otlp_url" {
  secret_id = "${var.prefix}grafana-otlp-url"

  replication {
    auto {}
  }

}

resource "google_secret_manager_secret_version" "grafana_otlp_url" {
  secret      = google_secret_manager_secret.grafana_otlp_url.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

data "google_secret_manager_secret_version" "grafana_otlp_url" {
  secret = google_secret_manager_secret.grafana_otlp_url.name

  depends_on = [google_secret_manager_secret_version.grafana_otlp_url]
}


# grafana otel collector token
resource "google_secret_manager_secret" "grafana_otel_collector_token" {
  secret_id = "${var.prefix}grafana-otel-collector-token"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "grafana_otel_collector_token" {
  secret      = google_secret_manager_secret.grafana_otel_collector_token.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }

}

# grafana username
resource "google_secret_manager_secret" "grafana_username" {
  secret_id = "${var.prefix}grafana-username"

  replication {
    auto {}
  }

}


resource "google_secret_manager_secret_version" "grafana_username" {
  secret      = google_secret_manager_secret.grafana_username.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }

}

resource "nomad_job" "otel_collector" {
  jobspec = file("${path.module}/otel-collector.hcl")
  depends_on = [
    google_secret_manager_secret_version.grafana_otel_collector_token,
    google_secret_manager_secret_version.grafana_username,
  ]
  hcl2 {
    vars = {

      grafana_otel_collector_token = google_secret_manager_secret_version.grafana_otel_collector_token.secret_data
      grafana_username             = google_secret_manager_secret_version.grafana_username.secret_data
      consul_token                 = var.consul_acl_token_secret

      gcp_zone = var.gcp_zone
    }
  }


}


resource "google_secret_manager_secret" "grafana_logs_username" {
  secret_id = "${var.prefix}grafana-logging-username"

  replication {
    auto {}
  }

}

resource "google_secret_manager_secret_version" "grafana_logs_username" {
  secret      = google_secret_manager_secret.grafana_logs_username.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }

}

resource "google_secret_manager_secret" "grafana_logs_url" {
  secret_id = "${var.prefix}grafana-logs-url"

  replication {
    auto {}
  }

}

resource "google_secret_manager_secret_version" "grafana_logs_url" {
  secret      = google_secret_manager_secret.grafana_logs_url.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }

}


resource "google_secret_manager_secret" "grafana_logs_collector_api_token" {
  secret_id = "${var.prefix}grafana-api-key-logs-collector"

  replication {
    auto {}
  }

}

resource "google_secret_manager_secret_version" "grafana_logs_collector_api_token" {
  secret      = google_secret_manager_secret.grafana_logs_collector_api_token.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }

}


resource "nomad_job" "logs_collector" {
  jobspec = file("${path.module}/logs-collector.hcl")

  depends_on = [
    google_secret_manager_secret_version.grafana_logs_username,
    google_secret_manager_secret_version.grafana_logs_url,
    google_secret_manager_secret_version.grafana_logs_collector_api_token
  ]

  hcl2 {
    vars = {
      gcp_zone = var.gcp_zone

      logs_port_number        = var.logs_proxy_port.port
      logs_health_port_number = var.logs_health_proxy_port.port
      logs_health_path        = var.logs_health_proxy_port.health_path
      logs_port_name          = var.logs_proxy_port.name

      loki_service_port_number = var.loki_service_port.port

      grafana_logs_username = google_secret_manager_secret_version.grafana_logs_username.secret_data
      grafana_logs_endpoint = google_secret_manager_secret_version.grafana_logs_url.secret_data
      grafana_api_key       = google_secret_manager_secret_version.grafana_logs_collector_api_token.secret_data
    }
  }
}

data "google_storage_bucket_object" "orchestrator" {
  name   = "orchestrator"
  bucket = var.fc_env_pipeline_bucket_name
}


data "google_compute_machine_types" "client" {
  zone   = var.gcp_zone
  filter = "name = \"${var.client_machine_type}\""
}

data "external" "orchestrator_checksum" {
  program = ["bash", "${path.module}/checksum.sh"]

  query = {
    base64 = data.google_storage_bucket_object.orchestrator.md5hash
  }
}

resource "nomad_job" "orchestrator" {
  jobspec = templatefile("${path.module}/orchestrator.hcl", {
    gcp_zone         = var.gcp_zone
    port             = var.orchestrator_port
    environment      = var.environment
    consul_acl_token = var.consul_acl_token_secret

    bucket_name                  = var.fc_env_pipeline_bucket_name
    orchestrator_checksum        = data.external.orchestrator_checksum.result.hex
    logs_collector_address       = "http://localhost:${var.logs_proxy_port.port}"
    logs_collector_public_ip     = var.logs_proxy_address
    otel_tracing_print           = var.otel_tracing_print
    template_bucket_name         = var.template_bucket_name
    otel_collector_grpc_endpoint = "localhost:4317"
  })
}

data "google_storage_bucket_object" "template_manager" {
  name   = "template-manager"
  bucket = var.fc_env_pipeline_bucket_name
}


data "external" "template_manager" {
  program = ["bash", "${path.module}/checksum.sh"]

  query = {
    base64 = data.google_storage_bucket_object.template_manager.md5hash
  }
}

resource "nomad_job" "template_manager" {
  jobspec = file("${path.module}/template-manager.hcl")

  hcl2 {
    vars = {
      gcp_project = var.gcp_project_id
      gcp_region  = var.gcp_region
      gcp_zone    = var.gcp_zone
      port        = var.template_manager_port
      environment = var.environment

      api_secret                   = var.api_secret
      bucket_name                  = var.fc_env_pipeline_bucket_name
      docker_registry              = var.custom_envs_repository_name
      google_service_account_key   = var.google_service_account_key
      template_manager_checksum    = data.external.template_manager.result.hex
      otel_tracing_print           = var.otel_tracing_print
      template_bucket_name         = var.template_bucket_name
      otel_collector_grpc_endpoint = "localhost:4317"
    }
  }
}

resource "nomad_job" "loki" {
  jobspec = file("${path.module}/loki.hcl")

  hcl2 {
    vars = {
      gcp_zone = var.gcp_zone

      loki_bucket_name = var.loki_bucket_name

      loki_service_port_number = var.loki_service_port.port
      loki_service_port_name   = var.loki_service_port.name
    }
  }
}
