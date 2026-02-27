data "aws_caller_identity" "current" {}
data "aws_partition" "current" {}

locals {
  account_id = data.aws_caller_identity.current.account_id
  partition  = data.aws_partition.current.partition
}

# --- EKS Cluster ---
module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "21.15.1"

  name               = var.cluster_name
  kubernetes_version = var.kubernetes_version

  vpc_id     = var.vpc_id
  subnet_ids = var.subnet_ids

  # API endpoint access
  endpoint_public_access       = true
  endpoint_private_access      = true
  endpoint_public_access_cidrs = var.public_access_cidrs

  # Cluster logging
  enabled_log_types                      = var.eks_cluster_log_types
  cloudwatch_log_group_retention_in_days = var.eks_log_retention_days

  # Core EKS addons
  addons = {
    coredns                = {}
    kube-proxy             = {}
    vpc-cni                = {}
    eks-pod-identity-agent = {}
    aws-ebs-csi-driver     = {}
  }

  # Bootstrap managed node group for Karpenter + system pods
  eks_managed_node_groups = {
    bootstrap = {
      ami_type       = "AL2023_x86_64_STANDARD"
      instance_types = [var.bootstrap_instance_type]

      min_size     = var.temporal_enabled ? 4 : 2
      max_size     = 10
      desired_size = var.temporal_enabled ? 4 : 2

      labels = {
        "e2b.dev/node-pool" = "system"
      }

      iam_role_additional_policies = {
        AmazonEBSCSIDriverPolicy = "arn:${local.partition}:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy"
      }

      tags = merge(var.tags, {
        "karpenter.sh/discovery" = var.cluster_name
      })
    }
  }

  # Allow Karpenter nodes to join
  node_security_group_tags = {
    "karpenter.sh/discovery" = var.cluster_name
  }

  # Encrypt K8s secrets at rest with KMS
  encryption_config = {
    provider_key_arn = aws_kms_key.eks_secrets.arn
    resources        = ["secrets"]
  }

  # Enable IRSA
  enable_irsa = true

  tags = merge(var.tags, {
    "karpenter.sh/discovery" = var.cluster_name
  })
}

# --- KMS Key for EKS Secrets Envelope Encryption ---
resource "aws_kms_key" "eks_secrets" {
  description             = "KMS key for EKS secrets envelope encryption"
  deletion_window_in_days = 30
  enable_key_rotation     = true

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "EnableRootAccountAccess"
        Effect = "Allow"
        Principal = {
          AWS = "arn:aws:iam::${local.account_id}:root"
        }
        Action   = "kms:*"
        Resource = "*"
      },
      {
        Sid    = "AllowEKSCluster"
        Effect = "Allow"
        Principal = {
          Service = "eks.amazonaws.com"
        }
        Action = [
          "kms:Decrypt",
          "kms:DescribeKey",
          "kms:Encrypt",
          "kms:GenerateDataKey*",
          "kms:ReEncrypt*",
        ]
        Resource = "*"
      }
    ]
  })

  tags = var.tags
}

resource "aws_kms_alias" "eks_secrets" {
  name          = "alias/${var.cluster_name}-eks-secrets"
  target_key_id = aws_kms_key.eks_secrets.key_id
}

# --- Karpenter IAM & Infrastructure ---
module "karpenter" {
  source  = "terraform-aws-modules/eks/aws//modules/karpenter"
  version = "21.15.1"

  cluster_name = module.eks.cluster_name

  # Create IAM role for Karpenter controller (via Pod Identity)
  create_iam_role                 = true
  create_pod_identity_association = true
  namespace                       = "kube-system"
  service_account                 = "karpenter"

  # Create IAM role for Karpenter nodes
  create_node_iam_role = true
  node_iam_role_additional_policies = {
    AmazonSSMManagedInstanceCore = "arn:${local.partition}:iam::aws:policy/AmazonSSMManagedInstanceCore"
    AmazonEBSCSIDriverPolicy     = "arn:${local.partition}:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy"
  }

  # Create SQS queue for spot interruption handling
  enable_spot_termination = true

  tags = var.tags
}

# --- Karpenter Helm Release ---
resource "helm_release" "karpenter" {
  namespace  = "kube-system"
  name       = "karpenter"
  repository = "oci://public.ecr.aws/karpenter"
  chart      = "karpenter"
  version    = var.karpenter_version
  wait       = true

  values = [
    yamlencode({
      settings = {
        clusterName       = module.eks.cluster_name
        clusterEndpoint   = module.eks.cluster_endpoint
        interruptionQueue = module.karpenter.queue_name
      }
      controller = {
        resources = {
          requests = {
            cpu    = "250m"
            memory = "512Mi"
          }
          limits = {
            cpu    = "250m"
            memory = "512Mi"
          }
        }
      }
      tolerations = [
        {
          key      = "CriticalAddonsOnly"
          operator = "Exists"
        }
      ]
      nodeSelector = {
        "e2b.dev/node-pool" = "system"
      }
    })
  ]

  depends_on = [module.eks, module.karpenter]
}
