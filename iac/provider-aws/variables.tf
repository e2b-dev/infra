variable "aws_region" {
  description = "The AWS region to deploy resources in"
  type        = string
}

variable "availability_zones" {
  description = "List of availability zones to use"
  type        = list(string)
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC"
  type        = string
  default     = "10.0.0.0/16"
}

# --- EKS Configuration ---
variable "kubernetes_version" {
  description = "Kubernetes version for the EKS cluster"
  type        = string
  default     = "1.31"
}

variable "eks_ami_id" {
  description = "Custom AMI ID for EKS nodes (must have kubelet + nested virtualization support)"
  type        = string
}

variable "karpenter_version" {
  description = "Karpenter Helm chart version"
  type        = string
  default     = "1.6.0"
}

variable "bootstrap_instance_type" {
  description = "Instance type for bootstrap managed node group (Karpenter controller + system pods). Dev default (2 vCPU, 4 GiB). For production or Temporal, use t3.xlarge."
  type        = string
  default     = "t3.medium"
}

variable "client_instance_types" {
  description = "Instance types for the client (orchestrator) Karpenter NodePool"
  type        = list(string)
  default     = ["c8i.2xlarge", "c8i.4xlarge", "c8i.8xlarge"]
}

variable "build_instance_types" {
  description = "Instance types for the build (template-manager) Karpenter NodePool"
  type        = list(string)
  default     = ["c8i.2xlarge", "c8i.4xlarge", "c8i.8xlarge"]
}

variable "client_capacity_types" {
  description = "Capacity types for client Karpenter NodePool (on-demand, spot)"
  type        = list(string)
  default     = ["on-demand", "spot"]
}

variable "boot_disk_size_gb" {
  description = "Boot EBS volume size in GB for Karpenter nodes"
  type        = number
  default     = 100
}

variable "cache_disk_size_gb" {
  description = "Cache EBS volume size in GB for Karpenter nodes. For dev/staging with low sandbox density, 200 GB is sufficient."
  type        = number
  default     = 500
}

variable "client_hugepages_percentage" {
  description = "Hugepages percentage for Karpenter-managed nodes (client and build share one EC2NodeClass)"
  type        = number
  default     = 80
}

# --- API Configuration ---
variable "api_cluster_size" {
  type = number
}

variable "api_resources_cpu_count" {
  description = "CPU cores for API pods. Dev default. For production, use 2."
  type        = number
  default     = 0.5
}

variable "api_resources_memory_mb" {
  description = "Memory in MB for API pods. Dev default. For production, use 2048."
  type        = number
  default     = 512
}

variable "clickhouse_cluster_size" {
  type = number
}

variable "clickhouse_database_name" {
  description = "The name of the ClickHouse database to create."
  type        = string
  default     = "default"
}

variable "clickhouse_server_service_port" {
  type = object({
    name = string
    port = number
  })
  default = {
    name = "clickhouse"
    port = 9000
  }
}

variable "clickhouse_health_port" {
  type = object({
    name = string
    port = number
    path = string
  })
  default = {
    name = "clickhouse-health"
    port = 8123
    path = "/health"
  }
}

variable "client_proxy_count" {
  type    = number
  default = 2
}

variable "ingress_count" {
  type    = number
  default = 2
}

variable "client_proxy_resources_memory_mb" {
  description = "Memory in MB for client proxy pods. Dev default. For production, use 1024."
  type        = number
  default     = 256
}

variable "client_proxy_resources_cpu_count" {
  description = "CPU cores for client proxy pods. Dev default. For production, use 1."
  type        = number
  default     = 0.25
}

variable "client_proxy_update_max_parallel" {
  type        = number
  description = "The number of client proxies to update in parallel during a rolling update."
  default     = 1
}

variable "client_proxy_health_port" {
  type = object({
    name = string
    port = number
    path = string
  })
  default = {
    name = "client-proxy"
    port = 3001
    path = "/health"
  }
}

variable "client_proxy_port" {
  type = object({
    name = string
    port = number
  })
  default = {
    name = "session"
    port = 3002
  }
}

variable "loki_cluster_size" {
  type    = number
  default = 1
}

variable "api_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
  default = {
    name        = "api"
    port        = 50001
    health_path = "/health"
  }
}

variable "ingress_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
  default = {
    name        = "ingress"
    port        = 8800
    health_path = "/ping"
  }
}

variable "docker_reverse_proxy_count" {
  description = "Number of docker-reverse-proxy replicas"
  type        = number
  default     = 2
}

variable "docker_reverse_proxy_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
  default = {
    name        = "docker-reverse-proxy"
    port        = 5000
    health_path = "/health"
  }
}

variable "redis_port" {
  type = object({
    name = string
    port = number
  })
  default = {
    name = "redis"
    port = 6379
  }
}

variable "allow_sandbox_internet" {
  type    = bool
  default = true
}

variable "orchestrator_port" {
  type    = number
  default = 5008
}

variable "orchestrator_proxy_port" {
  type    = number
  default = 5007
}

variable "template_manager_port" {
  type    = number
  default = 5008
}

variable "envd_timeout" {
  type    = string
  default = "40s"
}

variable "environment" {
  type    = string
  default = "prod"
}

variable "otel_collector_resources_memory_mb" {
  description = "Memory in MB for OTEL collector pods. Dev default. For production telemetry, use 1024."
  type        = number
  default     = 256
}

variable "otel_collector_resources_cpu_count" {
  description = "CPU cores for OTEL collector pods. Dev default. For production telemetry, use 0.5."
  type        = number
  default     = 0.1
}

variable "clickhouse_resources_memory_mb" {
  description = "Memory in MB for ClickHouse pods. Dev default. For production analytics, use 8192."
  type        = number
  default     = 1024
}

variable "clickhouse_resources_cpu_count" {
  description = "CPU cores for ClickHouse pods. Dev default. For production analytics, use 4."
  type        = number
  default     = 0.5
}

variable "domain_name" {
  type        = string
  description = "The domain name where e2b will run"
}

variable "prefix" {
  type        = string
  description = "The prefix to use for all resources in this module"
  default     = "e2b-"
}

variable "bucket_prefix" {
  type = string
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default = {
    "app"       = "e2b"
    "terraform" = "true"
  }
}

variable "loki_resources_memory_mb" {
  description = "Memory in MB for Loki pods. Dev default. For production log volume, use 2048."
  type        = number
  default     = 512
}

variable "loki_resources_cpu_count" {
  description = "CPU cores for Loki pods. Dev default. For production log volume, use 1."
  type        = number
  default     = 0.25
}

variable "loki_service_port" {
  type = object({
    name = string
    port = number
  })
  default = {
    name = "loki"
    port = 3100
  }
}

variable "template_bucket_name" {
  type        = string
  description = "The name of the FC template bucket"
  default     = ""
}

variable "redis_managed" {
  default = false
  type    = bool
}

variable "redis_node_type" {
  description = "ElastiCache node type for Redis"
  type        = string
  default     = "cache.t3.medium"
}

variable "redis_shard_count" {
  description = "Number of shards in the Redis cluster"
  type        = number
  default     = 1
}

variable "redis_replica_count" {
  description = "Number of replicas per shard"
  type        = number
  default     = 1
}

variable "efs_cache_enabled" {
  type        = bool
  description = "Set to true to enable EFS shared cache (replaces GCP Filestore)"
  default     = false
}

# --- Compliance Services ---
variable "enable_vpc_flow_logs" {
  description = "Enable VPC Flow Logs for network audit trail (GDPR + ISO 27001)"
  type        = bool
  default     = false
}

variable "vpc_flow_logs_retention_days" {
  description = "CloudWatch log group retention in days for VPC Flow Logs"
  type        = number
  default     = 90
}

variable "enable_guardduty" {
  description = "Enable AWS GuardDuty for threat detection (ISO 27001). Enabled by default for production security."
  type        = bool
  default     = true
}

variable "enable_aws_config" {
  description = "Enable AWS Config for configuration compliance monitoring (ISO 27001)"
  type        = bool
  default     = false
}

variable "enable_inspector" {
  description = "Enable AWS Inspector v2 for vulnerability scanning (ISO 27001)"
  type        = bool
  default     = false
}

variable "enable_cloudtrail" {
  description = "Enable AWS CloudTrail for API audit logging (ISO 27001 / SOC2). Enabled by default for production security."
  type        = bool
  default     = true
}

variable "enable_s3_access_logging" {
  description = "Enable S3 server access logging for compliance-sensitive buckets"
  type        = bool
  default     = false
}

variable "eks_public_access_cidrs" {
  description = "CIDR blocks allowed to access the EKS API endpoint publicly. Empty default forces explicit configuration. Restrict to your team's IP ranges."
  type        = list(string)
  default     = []
}

# --- EKS Cluster Logging ---

variable "eks_cluster_log_types" {
  description = "EKS control plane log types to enable (default: audit + authenticator for non-production; production should override to include all 5 types)"
  type        = list(string)
  default     = ["audit", "authenticator"]
}

variable "eks_log_retention_days" {
  description = "CloudWatch log group retention in days for EKS cluster logs"
  type        = number
  default     = 90
}

# --- Karpenter Tuning ---

variable "client_consolidation_after" {
  description = "Karpenter consolidation delay for client NodePool (prevents thrashing for bursty sandboxes)"
  type        = string
  default     = "300s"
}

variable "build_consolidation_after" {
  description = "Karpenter consolidation delay for build NodePool (batch-style, fast consolidation)"
  type        = string
  default     = "60s"
}

# --- EBS Performance ---

variable "cache_disk_iops" {
  description = "Provisioned IOPS for cache EBS volume (gp3 baseline: 3000, recommended: 6000 for high sandbox density). For dev/staging, 3000 is sufficient and saves cost."
  type        = number
  default     = 6000
}

variable "cache_disk_throughput_mbps" {
  description = "Provisioned throughput in MB/s for cache EBS volume (gp3 baseline: 125, recommended: 400)"
  type        = number
  default     = 400
}

# --- VPC Endpoints ---

variable "enable_vpc_endpoints" {
  description = "Enable VPC endpoints for AWS services (S3, ECR, Secrets Manager, CloudWatch, STS) to reduce NAT costs. Enabled by default for cost savings."
  type        = bool
  default     = true
}

variable "single_nat_gateway" {
  description = "Use a single NAT gateway instead of one per AZ (cost savings for dev/staging, reduced HA). Recommended true for dev, false for staging/prod."
  type        = bool
  default     = false
}

# --- WAF & Load Balancer ---

variable "enable_waf_managed_rules" {
  description = "Enable AWS managed WAF rule groups (CommonRuleSet, KnownBadInputs, SQLi, IpReputation)"
  type        = bool
  default     = true
}

variable "session_deregistration_delay" {
  description = "Deregistration delay in seconds for NLB session target group (higher for long-lived WebSockets)"
  type        = number
  default     = 300
}

# --- Monitoring & Alerting ---

variable "enable_monitoring" {
  description = "Enable CloudWatch alarms and SNS alerting for cost, reliability, and performance monitoring. Requires alert_email to be set."
  type        = bool
  default     = true
}

variable "alert_email" {
  description = "Email address for CloudWatch alarm notifications"
  type        = string
  default     = ""
}

variable "monthly_budget_amount" {
  description = "Monthly AWS spending threshold in USD for billing alarm"
  type        = number
  default     = 1000
}

# --- Security Hardening ---

variable "restrict_egress_to_vpc" {
  description = "Restrict egress on RDS, ElastiCache, EFS, and ALB security groups to VPC CIDR only (enabled by default)"
  type        = bool
  default     = true
}

variable "filestore_cache_cleanup_disk_usage_target" {
  type        = number
  description = "The max disk usage target of the shared cache in percent"
  default     = 90
}

variable "filestore_cache_cleanup_dry_run" {
  type    = bool
  default = false
}

variable "filestore_cache_cleanup_files_per_loop" {
  type    = number
  default = 10000
}

variable "filestore_cache_cleanup_deletions_per_loop" {
  type    = number
  default = 900
}

variable "filestore_cache_cleanup_max_concurrent_stat" {
  type        = number
  description = "Number of concurrent stat goroutines"
  default     = 16
}

variable "filestore_cache_cleanup_max_concurrent_scan" {
  type        = number
  description = "Number of concurrent scanner goroutines"
  default     = 16
}

variable "filestore_cache_cleanup_max_concurrent_delete" {
  type        = number
  description = "Number of concurrent deleter goroutines"
  default     = 4
}

variable "filestore_cache_cleanup_max_retries" {
  type        = number
  description = "Maximum number of continuous error or miss retries before giving up"
  default     = 10000
}

variable "loki_use_v13_schema_from" {
  type        = string
  description = "This should be a date soon after you deploy. Format = YYYY-MM-DD"
  default     = ""

  validation {
    condition     = var.loki_use_v13_schema_from == "" || can(regex("\\d{4}-\\d{2}-\\d{2}", var.loki_use_v13_schema_from))
    error_message = "must be YYYY-MM-DD"
  }
}

variable "dockerhub_remote_repository_url" {
  type    = string
  default = ""
}

# --- Temporal Configuration ---

variable "temporal_enabled" {
  description = "Enable Temporal Server deployment for multi-agent orchestration"
  type        = bool
  default     = false
}

variable "aurora_host" {
  description = "Aurora PostgreSQL cluster endpoint for Temporal persistence"
  type        = string
  default     = ""
}

variable "aurora_port" {
  description = "Aurora PostgreSQL port"
  type        = number
  default     = 5432
}

variable "temporal_db_user" {
  description = "PostgreSQL user for Temporal databases (temporal, temporal_visibility)"
  type        = string
  default     = "temporal"
}

variable "temporal_chart_version" {
  description = "Temporal Helm chart version. Pin to a specific version for reproducible deploys. Last reviewed: 2026-02-26."
  type        = string
  default     = "0.73.1"
}

variable "temporal_cert_validity_hours" {
  description = "Validity period in hours for Temporal mTLS certificates (default: 1 year)"
  type        = number
  default     = 8760
}

variable "temporal_worker_replica_count" {
  description = "Number of Temporal worker replicas"
  type        = number
  default     = 2
}

variable "temporal_web_replica_count" {
  description = "Number of Temporal web UI replicas"
  type        = number
  default     = 2
}
