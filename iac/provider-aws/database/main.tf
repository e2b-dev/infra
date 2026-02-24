# Database module: Placeholder for Aurora Serverless v2 / RDS
#
# This module creates the subnet group needed for an RDS/Aurora deployment.
# The actual database should be provisioned externally (recommended: Aurora Serverless v2)
# and its connection string injected into the Secrets Manager secret:
#   ${var.prefix}postgres-connection-string
#
# Recommended Aurora Serverless v2 setup:
#   - Engine: aurora-postgresql (15.x+)
#   - Min capacity: 0.5 ACU (~$44/month minimum)
#   - Max capacity: 128 ACU (auto-scales with demand)
#   - Multi-AZ: enabled
#   - Encryption: enabled (AWS KMS)

resource "aws_db_subnet_group" "main" {
  name       = "${var.prefix}database"
  subnet_ids = var.subnet_ids

  tags = merge(var.tags, {
    Name = "${var.prefix}database"
  })
}
