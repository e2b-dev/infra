# IAM Role for infrastructure instances
resource "aws_iam_role" "infra_instances" {
  name = "${var.prefix}infra-instances"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "ec2.amazonaws.com"
        }
      }
    ]
  })

  tags = var.tags
}

resource "aws_iam_instance_profile" "infra_instances" {
  name = "${var.prefix}infra-instances"
  role = aws_iam_role.infra_instances.name

  tags = var.tags
}

# S3 access policy
resource "aws_iam_role_policy" "s3_access" {
  name = "${var.prefix}s3-access"
  role = aws_iam_role.infra_instances.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:PutObject",
          "s3:DeleteObject",
          "s3:ListBucket",
          "s3:GetBucketLocation",
          "s3:AbortMultipartUpload",
          "s3:ListMultipartUploadParts"
        ]
        Resource = [
          "arn:aws:s3:::${var.bucket_prefix}*",
          "arn:aws:s3:::${var.bucket_prefix}*/*"
        ]
      }
    ]
  })
}

# ECR access policy
resource "aws_iam_role_policy" "ecr_access" {
  name = "${var.prefix}ecr-access"
  role = aws_iam_role.infra_instances.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "ecr:GetDownloadUrlForLayer",
          "ecr:BatchGetImage",
          "ecr:BatchCheckLayerAvailability",
          "ecr:GetAuthorizationToken",
          "ecr:DescribeRepositories",
          "ecr:ListImages",
          "ecr:DescribeImages"
        ]
        Resource = "*"
      }
    ]
  })
}

# Secrets Manager access policy
resource "aws_iam_role_policy" "secrets_access" {
  name = "${var.prefix}secrets-access"
  role = aws_iam_role.infra_instances.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "secretsmanager:GetSecretValue",
          "secretsmanager:DescribeSecret"
        ]
        Resource = "arn:aws:secretsmanager:${var.aws_region}:*:secret:${var.prefix}*"
      }
    ]
  })
}

# CloudWatch access policy for monitoring and logging
resource "aws_iam_role_policy" "cloudwatch_access" {
  name = "${var.prefix}cloudwatch-access"
  role = aws_iam_role.infra_instances.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "cloudwatch:PutMetricData",
          "logs:CreateLogGroup",
          "logs:CreateLogStream",
          "logs:PutLogEvents",
          "logs:DescribeLogStreams"
        ]
        Resource = "*"
      }
    ]
  })
}

# EC2 describe access (for Karpenter node discovery)
resource "aws_iam_role_policy" "ec2_describe" {
  name = "${var.prefix}ec2-describe"
  role = aws_iam_role.infra_instances.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "ec2:DescribeInstances",
          "ec2:DescribeTags",
          "autoscaling:DescribeAutoScalingGroups"
        ]
        Resource = "*"
      }
    ]
  })
}

# ECR Repositories
resource "aws_ecr_repository" "core" {
  name                 = "${var.prefix}core"
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = var.tags
}

resource "aws_ecr_lifecycle_policy" "core" {
  repository = aws_ecr_repository.core.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Keep last 50 images"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 50
        }
        action = {
          type = "expire"
        }
      }
    ]
  })
}

resource "aws_ecr_repository" "orchestration" {
  name                 = "${var.prefix}orchestration"
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = var.tags
}

resource "aws_ecr_lifecycle_policy" "orchestration" {
  repository = aws_ecr_repository.orchestration.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Keep last 50 images"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 50
        }
        action = {
          type = "expire"
        }
      }
    ]
  })
}
