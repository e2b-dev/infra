variable "prefix" {
  type = string
}

variable "lb_type" {
  type        = string
  description = "Hetzner Cloud LB type. lb11=10MB/s, lb21=20MB/s, lb31=40MB/s."
  default     = "lb21"
}

variable "location" {
  type    = string
  default = "fsn1"
}

variable "algorithm" {
  type        = string
  description = "Round-robin or least_connections."
  default     = "round_robin"
}

variable "network_id" {
  type = number
}

variable "subnet_cidr" {
  type = string
}

variable "lb_subnet_offset" {
  type        = number
  description = "Offset into subnet_cidr for LB private IP. Default 100 = .100"
  default     = 100
}

variable "certificate_id" {
  type        = number
  description = "Hetzner Cloud Certificate ID (from NX.2.2 cert module)."
}

variable "ingress_port" {
  type        = number
  description = "Backend port on ingress nodes (Caddy/Traefik typically 8080)."
  default     = 8080
}

variable "enable_grpc" {
  type    = bool
  default = true
}

variable "grpc_listen_port" {
  type        = number
  description = "Public TCP port for gRPC traffic (TLS-passthrough to backend)."
  default     = 8443
}

variable "grpc_destination_port" {
  type        = number
  description = "Backend gRPC port on ingress nodes."
  default     = 5009
}

variable "enable_nomad_listener" {
  type    = bool
  default = true
}

variable "nomad_listen_port" {
  type        = number
  description = "Public TCP port for Nomad UI."
  default     = 4646
}

variable "nomad_destination_port" {
  type        = number
  description = "Backend Nomad HTTP port."
  default     = 4646
}

variable "common_labels" {
  type    = map(string)
  default = {}
}

variable "allow_force_destroy" {
  type    = bool
  default = false
}
