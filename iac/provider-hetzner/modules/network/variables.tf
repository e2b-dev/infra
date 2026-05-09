variable "network_name"      { type = string; default = "maxicore-prod" }
variable "ip_range"          { type = string; default = "10.0.0.0/8" }
variable "cloud_ip_range"    { type = string; default = "10.0.1.0/24" }
variable "vswitch_ip_range"  { type = string; default = "10.10.0.0/24" }
variable "vswitch_id"        { type = number; default = 79572 }
variable "network_zone"      { type = string; default = "eu-central" }
variable "env"               { type = string; default = "prod" }
