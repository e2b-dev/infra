# --- CloudWatch Monitoring & Alerting ---
# All resources are conditional on var.enable_monitoring

# SNS Topic for alarm notifications
resource "aws_sns_topic" "alerts" {
  count = var.enable_monitoring ? 1 : 0

  name = "${var.prefix}alerts"
  tags = var.tags
}

resource "aws_sns_topic_subscription" "email" {
  count = var.enable_monitoring && var.alert_email != "" ? 1 : 0

  topic_arn = aws_sns_topic.alerts[0].arn
  protocol  = "email"
  endpoint  = var.alert_email
}

# --- Billing Alarm ---
resource "aws_cloudwatch_metric_alarm" "monthly_cost" {
  count = var.enable_monitoring ? 1 : 0

  alarm_name          = "${var.prefix}monthly-cost"
  alarm_description   = "Monthly AWS estimated charges exceed $${var.monthly_budget_amount}"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "EstimatedCharges"
  namespace           = "AWS/Billing"
  period              = 86400
  statistic           = "Maximum"
  threshold           = var.monthly_budget_amount
  treat_missing_data  = "notBreaching"

  dimensions = {
    Currency = "USD"
  }

  alarm_actions = [aws_sns_topic.alerts[0].arn]
  ok_actions    = [aws_sns_topic.alerts[0].arn]

  tags = var.tags
}

# --- EKS Node Count Alarm ---
resource "aws_cloudwatch_metric_alarm" "eks_node_count" {
  count = var.enable_monitoring ? 1 : 0

  alarm_name          = "${var.prefix}eks-node-count"
  alarm_description   = "EKS node count exceeds 50 — possible Karpenter scaling anomaly"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  metric_name         = "node_count"
  namespace           = "ContainerInsights"
  period              = 300
  statistic           = "Maximum"
  threshold           = 50
  treat_missing_data  = "notBreaching"

  dimensions = {
    ClusterName = var.eks_cluster_name
  }

  alarm_actions = [aws_sns_topic.alerts[0].arn]
  ok_actions    = [aws_sns_topic.alerts[0].arn]

  tags = var.tags
}

# --- ALB 5xx Errors Alarm ---
resource "aws_cloudwatch_metric_alarm" "alb_5xx" {
  count = var.enable_monitoring && var.alb_arn_suffix != "" ? 1 : 0

  alarm_name          = "${var.prefix}alb-5xx-errors"
  alarm_description   = "ALB returning >100 5xx errors in 5 minutes"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "HTTPCode_ELB_5XX_Count"
  namespace           = "AWS/ApplicationELB"
  period              = 300
  statistic           = "Sum"
  threshold           = 100
  treat_missing_data  = "notBreaching"

  dimensions = {
    LoadBalancer = var.alb_arn_suffix
  }

  alarm_actions = [aws_sns_topic.alerts[0].arn]
  ok_actions    = [aws_sns_topic.alerts[0].arn]

  tags = var.tags
}

# --- Redis CPU Alarm ---
resource "aws_cloudwatch_metric_alarm" "redis_cpu" {
  count = var.enable_monitoring && var.redis_replication_group_id != "" ? 1 : 0

  alarm_name          = "${var.prefix}redis-cpu"
  alarm_description   = "Redis EngineCPUUtilization exceeds 80%"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  metric_name         = "EngineCPUUtilization"
  namespace           = "AWS/ElastiCache"
  period              = 300
  statistic           = "Average"
  threshold           = 80
  treat_missing_data  = "notBreaching"

  dimensions = {
    ReplicationGroupId = var.redis_replication_group_id
  }

  alarm_actions = [aws_sns_topic.alerts[0].arn]
  ok_actions    = [aws_sns_topic.alerts[0].arn]

  tags = var.tags
}

# --- Redis Replication Lag Alarm ---
resource "aws_cloudwatch_metric_alarm" "redis_replication_lag" {
  count = var.enable_monitoring && var.redis_replication_group_id != "" ? 1 : 0

  alarm_name          = "${var.prefix}redis-replication-lag"
  alarm_description   = "Redis replication lag exceeds 30 seconds — possible failover issue"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  metric_name         = "ReplicationLag"
  namespace           = "AWS/ElastiCache"
  period              = 300
  statistic           = "Maximum"
  threshold           = 30
  treat_missing_data  = "notBreaching"

  dimensions = {
    ReplicationGroupId = var.redis_replication_group_id
  }

  alarm_actions = [aws_sns_topic.alerts[0].arn]
  ok_actions    = [aws_sns_topic.alerts[0].arn]

  tags = var.tags
}

# --- NAT Gateway Port Allocation Errors Alarm ---
resource "aws_cloudwatch_metric_alarm" "nat_port_allocation" {
  count = var.enable_monitoring ? 1 : 0

  alarm_name          = "${var.prefix}nat-port-allocation"
  alarm_description   = "NAT Gateway port allocation errors detected — possible NAT exhaustion"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  metric_name         = "ErrorPortAllocation"
  namespace           = "AWS/NATGateway"
  period              = 300
  statistic           = "Sum"
  threshold           = 0
  treat_missing_data  = "notBreaching"

  alarm_actions = [aws_sns_topic.alerts[0].arn]
  ok_actions    = [aws_sns_topic.alerts[0].arn]

  tags = var.tags
}

# --- Karpenter Pending Pods Alarm ---
resource "aws_cloudwatch_metric_alarm" "karpenter_pending_pods" {
  count = var.enable_monitoring ? 1 : 0

  alarm_name          = "${var.prefix}karpenter-pending-pods"
  alarm_description   = "Karpenter has >10 pending pods for 5+ minutes — possible NodePool limit reached or capacity issue"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  metric_name         = "pending_pods"
  namespace           = "ContainerInsights"
  period              = 300
  statistic           = "Maximum"
  threshold           = 10
  treat_missing_data  = "notBreaching"

  dimensions = {
    ClusterName = var.eks_cluster_name
  }

  alarm_actions = [aws_sns_topic.alerts[0].arn]
  ok_actions    = [aws_sns_topic.alerts[0].arn]

  tags = var.tags
}
