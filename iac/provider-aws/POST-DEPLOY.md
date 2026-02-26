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

## 6. Temporal Server (if `temporal_enabled = true`)

### 6a. Create Temporal Databases on Aurora

Before enabling the Temporal module, create the databases and user:

```sql
-- Connect to your Aurora cluster
CREATE DATABASE temporal;
CREATE DATABASE temporal_visibility;
CREATE USER temporal WITH PASSWORD '<from Secrets Manager: {prefix}temporal-db-password>';
GRANT ALL PRIVILEGES ON DATABASE temporal TO temporal;
GRANT ALL PRIVILEGES ON DATABASE temporal_visibility TO temporal;
-- Grant schema permissions
\c temporal
GRANT ALL ON SCHEMA public TO temporal;
\c temporal_visibility
GRANT ALL ON SCHEMA public TO temporal;
```

### 6b. Verify Temporal Deployment

```bash
# All pods should be Running
kubectl get pods -n temporal

# Schema setup/update jobs should be Completed
kubectl get jobs -n temporal

# Access Web UI
kubectl port-forward svc/temporal-web -n temporal 8080:8080
# Open http://localhost:8080

# Test admin tools
kubectl exec -it deploy/temporal-admintools -n temporal -- tctl namespace list
```

### 6c. Register Application Namespaces

```bash
kubectl exec -it deploy/temporal-admintools -n temporal -- \
  tctl namespace register default --retention 72h

kubectl exec -it deploy/temporal-admintools -n temporal -- \
  tctl namespace register agents --retention 72h \
  --description "Multi-agent workflow namespace"
```

See `TEMPORAL.md` for worker connection examples, workflow patterns, and security configuration.

## 7. Configure Monitoring

Monitoring is enabled by default (`enable_monitoring = true`). You **must** set `alert_email` for alarm notifications to work.

```bash
# In your tfvars:
alert_email           = "ops@example.com"
monthly_budget_amount = 2000  # USD threshold for billing alarm
```

After applying, confirm the SNS email subscription (check your inbox for the AWS confirmation email).

CloudWatch alarms are created for: monthly cost threshold, EKS node count, ALB 5xx errors, Redis CPU/replication lag, NAT port allocation, and Karpenter pending pods.

## 8. Verify Security Hardening

The following security features are enabled by default and should be verified after deploy:

```bash
# Verify EKS secrets envelope encryption (KMS)
aws eks describe-cluster --name $(terraform output -raw eks_cluster_name) \
  --query 'cluster.encryptionConfig' --region $AWS_REGION

# Verify CloudTrail with log validation and KMS
aws cloudtrail describe-trails --region $AWS_REGION \
  --query 'trailList[].{Name:Name,LogFileValidation:LogFileValidationEnabled,KmsKeyId:KmsKeyId}'

# Verify GuardDuty Runtime Monitoring
aws guardduty list-detectors --region $AWS_REGION

# Verify Pod Security Standards on e2b namespace
kubectl get ns e2b -o jsonpath='{.metadata.labels}' | jq .

# Verify NetworkPolicy for e2b namespace
kubectl get networkpolicies -n e2b

# Verify PodDisruptionBudgets
kubectl get pdb -n e2b

# Verify HPA for API and client-proxy
kubectl get hpa -n e2b
```

## 9. Production Hardening Checklist

**Enabled by default (verify after deploy):**
- [x] CloudTrail with KMS encryption and log file validation (`enable_cloudtrail = true`)
- [x] GuardDuty with Runtime Monitoring (`enable_guardduty = true`)
- [x] CloudWatch monitoring and SNS alerting (`enable_monitoring = true`)
- [x] EKS secrets envelope encryption (KMS)
- [x] Pod Security Standards (baseline enforce, restricted warn)
- [x] NetworkPolicy for e2b and temporal namespaces
- [x] PodDisruptionBudgets for API, client-proxy, ingress, ClickHouse
- [x] HPA for API and client-proxy (scales pods before Karpenter adds nodes)
- [x] VPC endpoints for S3, ECR, Secrets Manager, CloudWatch, STS
- [x] WAF managed rules on ALB

**Manual steps required:**
- [ ] Restrict EKS public API endpoint (`eks_public_access_cidrs` variable)
- [ ] Set `alert_email` and confirm SNS subscription (see Step 7)
- [ ] Enable S3 access logging (`enable_s3_access_logging = true`)
- [ ] Enable VPC Flow Logs (`enable_vpc_flow_logs = true`)
- [ ] Enable AWS Config (`enable_aws_config = true`)
- [ ] Enable Inspector (`enable_inspector = true`)
- [ ] Populate all Secrets Manager values
- [ ] Provision Aurora Serverless v2 database
- [ ] Configure DNS records for your domain
- [ ] If Temporal enabled: verify all pods Running in `temporal` namespace
- [ ] If Temporal enabled: register application namespaces via `tctl`
- [ ] If Temporal enabled: generate worker TLS certificates from Temporal CA
- [ ] If Temporal enabled: note cert expiry dates from `terraform output temporal_internode_cert_expiry` and `terraform output temporal_frontend_cert_expiry`
- [ ] If Temporal enabled: configure OIDC for Web UI before external exposure
