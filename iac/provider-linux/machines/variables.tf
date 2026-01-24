variable "datacenter" { type = string }

variable "servers" {
  type = list(object({
    host                 = string
    ssh_user             = string
    ssh_private_key_path = string
  }))
}

variable "clients" {
  type = list(object({
    host                 = string
    ssh_user             = string
    ssh_private_key_path = string
    node_pool            = string
  }))
}

variable "consul_acl_token" {
  type    = string
  default = ""
}

variable "nomad_acl_token" {
  type    = string
  default = ""
}

variable "docker_image_prefix" {
  type    = string
  default = ""
}

variable "docker_http_proxy" {
  type    = string
  default = ""
}

variable "docker_https_proxy" {
  type    = string
  default = ""
}

variable "docker_no_proxy" {
  type    = string
  default = ""
}

variable "builder_node_pool" {
  type    = string
  default = ""
}

variable "api_node_pool" {
  type    = string
  default = ""
}

variable "orchestrator_node_pool" {
  type    = string
  default = ""
}

variable "kernel_source_base_url" {
  type = string
}

variable "firecracker_source_base_url" {
  type = string
}

variable "default_kernel_version" {
  type = string
}

variable "default_firecracker_version" {
  type = string
}



variable "base_config_version" {
  type    = string
  default = "v1"
}

variable "docker_proxy_config_version" {
  type    = string
  default = "v1"
}



variable "consul_config_version" {
  type    = string
  default = "v1"
}

variable "nomad_config_version" {
  type    = string
  default = "v2"
}

variable "dns_config_version" {
  type    = string
  default = "v1"
}



variable "fc_artifacts_version" {
  type    = string
  default = "v1"
}

variable "node_pools_config_version" {
  type    = string
  default = "v2"
}

variable "firewall_tools_version" {
  type    = string
  default = "v1"
}

variable "enable_nodes_docker_proxy" {
  type    = bool
  default = true
}

variable "enable_nodes_fc_artifacts" {
  type    = bool
  default = true
}

variable "enable_nodes_uninstall" {
  type    = bool
  default = false
}

variable "uninstall_version" {
  type    = string
  default = "v1"
}

variable "uninstall_confirm_phrase" {
  type    = string
  default = ""
}

variable "enable_network_policy" {
  type    = bool
  default = false
}

variable "network_open_ports" {
  type    = list(string)
  default = []
}

variable "use_nfs_share_storage" {
  type    = bool
  default = false
}

variable "nfs_server_ip" {
  type    = string
  default = ""
}
