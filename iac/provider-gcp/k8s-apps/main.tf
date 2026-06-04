resource "google_storage_bucket_object" "namespace" {
  count        = var.argocd_enabled ? 1 : 0
  name         = "namespace.yaml"
  bucket       = var.argocd_apps_bucket_name
  content_type = "application/yaml"

  content = yamlencode({
    apiVersion = "argoproj.io/v1alpha1"
    kind       = "Application"
    metadata = {
      name      = "namespace"
      namespace = var.argocd_namespace
    }
    spec = {
      project = "default"
      source = {
        repoURL        = var.argocd_charts_repo_url
        chart          = "namespace"
        targetRevision = "0.1.0"
        helm = {
          valuesObject = {
            name = var.namespace
            labels = {
              "app.kubernetes.io/part-of"    = "e2b"
              "app.kubernetes.io/managed-by" = "argocd"
            }
          }
        }
      }
      destination = {
        name      = "in-cluster"
        namespace = "default"
      }
    }
  })
}

resource "google_storage_bucket_object" "api" {
  count        = var.argocd_enabled ? 1 : 0
  name         = "api.yaml"
  bucket       = var.argocd_apps_bucket_name
  content_type = "application/yaml"

  content = yamlencode({
    apiVersion = "argoproj.io/v1alpha1"
    kind       = "Application"
    metadata = {
      name      = "api"
      namespace = var.argocd_namespace
    }
    spec = {
      project = "default"
      source = {
        repoURL        = var.argocd_charts_repo_url
        chart          = "api"
        targetRevision = "0.1.0"
        helm = {
          valuesObject = {
            api = {
              env   = var.api_env_vars
              image = data.google_artifact_registry_docker_image.api_image.self_link
            }
            dbMigrator = {
              env = var.api_db_migrator_env_vars
            }
          }
        }
      }
      destination = {
        name      = "in-cluster"
        namespace = var.namespace
      }
    }
  })
}
