# Post-Deployment Manual Steps

After running `terraform apply`, the following manual steps are required to complete the AWS infrastructure setup.

## 1. Populate Secrets Manager

All secrets are created with placeholder values and must be populated manually.

```bash
AWS_REGION=eu-central-1
PREFIX=e2b-

# Required secrets (replace values with actual credentials):
aws secretsmanager put-secret-value --region $AWS_REGION \
  --secret-id "${PREFIX}postgres-connection-string" \
  --secret-string "postgresql://user:pass@host:5432/dbname?sslmode=require"

aws secretsmanager put-secret-value --region $AWS_REGION \
  --secret-id "${PREFIX}postgres-read-replica-connection-string" \
  --secret-string "postgresql://user:pass@replica-host:5432/dbname?sslmode=require"

aws secretsmanager put-secret-value --region $AWS_REGION \
  --secret-id "${PREFIX}supabase-jwt-secrets" \
  --secret-string '{"jwt_secret":"...","anon_key":"...","service_role_key":"..."}'

aws secretsmanager put-secret-value --region $AWS_REGION \
  --secret-id "${PREFIX}cloudflare-api-token" \
  --secret-string "your-cloudflare-api-token"

aws secretsmanager put-secret-value --region $AWS_REGION \
  --secret-id "${PREFIX}posthog-api-key" \
  --secret-string "your-posthog-key"

aws secretsmanager put-secret-value --region $AWS_REGION \
  --secret-id "${PREFIX}analytics-collector-host" \
  --secret-string "https://analytics.example.com"

aws secretsmanager put-secret-value --region $AWS_REGION \
  --secret-id "${PREFIX}analytics-collector-api-token" \
  --secret-string "your-analytics-token"

aws secretsmanager put-secret-value --region $AWS_REGION \
  --secret-id "${PREFIX}launch-darkly-api-key" \
  --secret-string "your-ld-key"

aws secretsmanager put-secret-value --region $AWS_REGION \
  --secret-id "${PREFIX}grafana-api-key" \
  --secret-string "your-grafana-key"

# If using external Redis (redis_managed = false):
aws secretsmanager put-secret-value --region $AWS_REGION \
  --secret-id "${PREFIX}redis-cluster-url" \
  --secret-string "rediss://host:6379"

aws secretsmanager put-secret-value --region $AWS_REGION \
  --secret-id "${PREFIX}redis-tls-ca-base64" \
  --secret-string "base64-encoded-ca-cert"

# DockerHub credentials (for pulling images):
aws secretsmanager put-secret-value --region $AWS_REGION \
  --secret-id "${PREFIX}dockerhub-remote-repo-username" \
  --secret-string "your-dockerhub-username"

aws secretsmanager put-secret-value --region $AWS_REGION \
  --secret-id "${PREFIX}dockerhub-remote-repo-password" \
  --secret-string "your-dockerhub-token"

# Routing domains (JSON array, update when adding domains):
aws secretsmanager put-secret-value --region $AWS_REGION \
  --secret-id "${PREFIX}routing-domains" \
  --secret-string '["sandbox.example.com"]'
```

## 2. Provision Aurora Serverless v2

The database module only creates the subnet group. You must provision the Aurora cluster manually or via a separate Terraform configuration.

```bash
# Recommended: Aurora Serverless v2 PostgreSQL
aws rds create-db-cluster \
  --db-cluster-identifier e2b-aurora \
  --engine aurora-postgresql \
  --engine-version 15.4 \
  --serverless-v2-scaling-configuration MinCapacity=0.5,MaxCapacity=128 \
  --master-username e2b_admin \
  --master-user-password "$(openssl rand -base64 32)" \
  --db-subnet-group-name e2b-database \
  --vpc-security-group-ids <rds-security-group-id> \
  --storage-encrypted \
  --region $AWS_REGION

aws rds create-db-instance \
  --db-instance-identifier e2b-aurora-writer \
  --db-cluster-identifier e2b-aurora \
  --engine aurora-postgresql \
  --db-instance-class db.serverless \
  --region $AWS_REGION
```

After provisioning, update the `postgres-connection-string` secret with the actual connection string.

## 3. Configure kubeconfig

```bash
aws eks update-kubeconfig \
  --name $(terraform output -raw eks_cluster_name) \
  --region $AWS_REGION
```

## 4. Verify Karpenter

```bash
kubectl get nodepools
kubectl get ec2nodeclasses
kubectl get nodeclaims
```

## 5. Push Container Images

Build and push images to the ECR repository:

```bash
ECR_URL=$(terraform output -raw core_ecr_repository_url)
aws ecr get-login-password --region $AWS_REGION | docker login --username AWS --password-stdin $ECR_URL
```

## 6. Production Hardening Checklist

- [ ] Restrict EKS public API endpoint (`eks_public_access_cidrs` variable)
- [ ] Enable CloudTrail (`enable_cloudtrail = true`)
- [ ] Enable S3 access logging (`enable_s3_access_logging = true`)
- [ ] Enable VPC Flow Logs (`enable_vpc_flow_logs = true`)
- [ ] Enable GuardDuty (`enable_guardduty = true`)
- [ ] Enable AWS Config (`enable_aws_config = true`)
- [ ] Enable Inspector (`enable_inspector = true`)
- [ ] Populate all Secrets Manager values
- [ ] Provision Aurora Serverless v2 database
- [ ] Configure DNS records for your domain
- [ ] Verify WAF rules are active on ALB
