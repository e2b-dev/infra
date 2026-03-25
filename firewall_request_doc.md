# Firewall Request Document - E2B Infrastructure (GCP)

## Overview
This document specifies the firewall requirements for the E2B infrastructure deployment on GCP. The system runs Firecracker microVMs and requires strict networking control.

## Inbound Rules (Ingress)
These rules should be applied to the VPC network where E2B is deployed.

| Port | Protocol | Source | Target | Purpose |
|------|----------|--------|--------|---------|
| 443 | TCP | Any | LB | HTTPS API & Session Proxy (Public Access) |
| 80 | TCP | Any | LB | HTTP Redirect / Nomad UI (Public Access) |
| 3002 | TCP | Any | API Nodes | Sandbox Session Proxy |
| 5000 | TCP | Any | API Nodes | Docker Reverse Proxy |
| 8800 | TCP | Any | API Nodes | Traefik Ingress |
| 22 | TCP | 35.235.240.0/20 | All Nodes | SSH via Identity-Aware Proxy (IAP) |
| 22 | TCP | YOUR_LOCAL_IP/32 | All Nodes | Direct SSH access (Optional/Restricted) |

## Outbound Rules (Egress)
All outbound traffic from the internal nodes (API, Orchestrator, ClickHouse) must be routed through the Cloud NAT gateway.

| Port | Protocol | Destination | Purpose |
|------|----------|-------------|---------|
| 80, 443 | TCP | Any | General Internet (Package Managers, Git, GCS, APIs) |
| 53 | UDP/TCP | Any | DNS Resolution |
| 22 | TCP | Any | Git SSH access (Clone repositories) |

## Internal Cluster Communication (VPC Internal)
Nodes within the cluster (`orch` tag) require full communication on the following ports:

| Port | Protocol | Purpose |
|------|----------|---------|
| 4646-4648 | TCP | Nomad HTTP/RPC/Serf |
| 8500, 8300-8302 | TCP | Consul HTTP/RPC/Serf |
| 8600 | UDP/TCP | Consul DNS |
| 9000, 8123 | TCP | ClickHouse Native/HTTP |
| 3100 | TCP | Loki Logging |
| 5007-5008 | TCP | Orchestrator & Template Manager |
| 6379 | TCP | Redis |
