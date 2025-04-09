module "monitoring_keeper_cluster" {
  source = "./stateful"

  startup_script   = var.startup_script
  environment      = var.environment
  gcp_zone         = var.gcp_zone
  cluster_name     = "${var.prefix}monitoring-keeper"
  cluster_size     = var.keeper_cluster_size
  cluster_tag_name = var.cluster_tag_name

  machine_type = var.clickhouse_keeper_machine_type
  image_family = var.image_family

  network_name = var.network_name

  service_port        = var.keeper_service_port
  service_health_port = var.keeper_service_health_port

  job_constraint_prefix = "clickhouse-keeper"
  node_pool             = "monitoring"

  nomad_port = var.nomad_port

  service_account_email = var.service_account_email

  labels = var.labels
}

module "monitoring_server_cluster" {
  source = "./stateful"

  startup_script   = var.startup_script
  environment      = var.environment
  gcp_zone         = var.gcp_zone
  cluster_name     = "${var.prefix}monitoring-server"
  cluster_size     = var.server_cluster_size
  cluster_tag_name = var.cluster_tag_name

  machine_type = var.clickhouse_server_machine_type
  image_family = var.image_family

  network_name = var.network_name

  service_port        = var.server_service_port
  service_health_port = var.server_service_health_port

  job_constraint_prefix = "clickhouse-server"
  node_pool             = "monitoring"

  nomad_port = var.nomad_port

  service_account_email = var.service_account_email

  labels = var.labels
}
