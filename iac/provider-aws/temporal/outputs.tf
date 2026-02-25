output "temporal_frontend_endpoint" {
  description = "Cluster-internal gRPC endpoint for Temporal workers and clients"
  value       = "temporal-frontend.temporal.svc.cluster.local:7233"
}

output "temporal_web_endpoint" {
  description = "Cluster-internal HTTP endpoint for Temporal Web UI"
  value       = "temporal-web.temporal.svc.cluster.local:8080"
}

output "temporal_namespace" {
  description = "Kubernetes namespace where Temporal is deployed"
  value       = kubernetes_namespace_v1.temporal.metadata[0].name
}
