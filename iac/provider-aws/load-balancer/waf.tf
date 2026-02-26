# --- WAF v2 Web ACL (ALB only, WAF not supported on NLB) ---
resource "aws_wafv2_web_acl" "main" {
  name  = "${var.prefix}waf"
  scope = "REGIONAL"

  default_action {
    allow {}
  }

  # --- AWS Managed Rules (opt-in) ---

  dynamic "rule" {
    for_each = var.enable_waf_managed_rules ? [1] : []
    content {
      name     = "aws-common-rules"
      priority = 10

      override_action {
        none {}
      }

      statement {
        managed_rule_group_statement {
          name        = "AWSManagedRulesCommonRuleSet"
          vendor_name = "AWS"
        }
      }

      visibility_config {
        sampled_requests_enabled   = true
        cloudwatch_metrics_enabled = true
        metric_name                = "${var.prefix}aws-common-rules"
      }
    }
  }

  dynamic "rule" {
    for_each = var.enable_waf_managed_rules ? [1] : []
    content {
      name     = "aws-known-bad-inputs"
      priority = 20

      override_action {
        none {}
      }

      statement {
        managed_rule_group_statement {
          name        = "AWSManagedRulesKnownBadInputsRuleSet"
          vendor_name = "AWS"
        }
      }

      visibility_config {
        sampled_requests_enabled   = true
        cloudwatch_metrics_enabled = true
        metric_name                = "${var.prefix}aws-known-bad-inputs"
      }
    }
  }

  dynamic "rule" {
    for_each = var.enable_waf_managed_rules ? [1] : []
    content {
      name     = "aws-sqli-rules"
      priority = 30

      override_action {
        none {}
      }

      statement {
        managed_rule_group_statement {
          name        = "AWSManagedRulesSQLiRuleSet"
          vendor_name = "AWS"
        }
      }

      visibility_config {
        sampled_requests_enabled   = true
        cloudwatch_metrics_enabled = true
        metric_name                = "${var.prefix}aws-sqli-rules"
      }
    }
  }

  dynamic "rule" {
    for_each = var.enable_waf_managed_rules ? [1] : []
    content {
      name     = "aws-ip-reputation"
      priority = 40

      override_action {
        none {}
      }

      statement {
        managed_rule_group_statement {
          name        = "AWSManagedRulesAmazonIpReputationList"
          vendor_name = "AWS"
        }
      }

      visibility_config {
        sampled_requests_enabled   = true
        cloudwatch_metrics_enabled = true
        metric_name                = "${var.prefix}aws-ip-reputation"
      }
    }
  }

  # --- Custom Rate Limiting Rules ---

  # Rate limit: API sandbox creation per API key header
  rule {
    name     = "api-throttle-by-api-key"
    priority = 50

    action {
      block {
        custom_response {
          response_code = 429
        }
      }
    }

    statement {
      rate_based_statement {
        limit              = 12000
        aggregate_key_type = "CUSTOM_KEYS"

        custom_key {
          header {
            name = "X-API-Key"
            text_transformation {
              priority = 0
              type     = "NONE"
            }
          }
        }

        scope_down_statement {
          and_statement {
            statement {
              byte_match_statement {
                search_string         = "/sandboxes"
                positional_constraint = "STARTS_WITH"
                field_to_match {
                  uri_path {}
                }
                text_transformation {
                  priority = 0
                  type     = "NONE"
                }
              }
            }
            statement {
              byte_match_statement {
                search_string         = "POST"
                positional_constraint = "EXACTLY"
                field_to_match {
                  method {}
                }
                text_transformation {
                  priority = 0
                  type     = "NONE"
                }
              }
            }
          }
        }
      }
    }

    visibility_config {
      sampled_requests_enabled   = true
      cloudwatch_metrics_enabled = true
      metric_name                = "${var.prefix}api-throttle-by-api-key"
    }
  }

  # Rate limit: all requests per IP
  rule {
    name     = "ip-rate-limit"
    priority = 60

    action {
      block {
        custom_response {
          response_code = 429
        }
      }
    }

    statement {
      rate_based_statement {
        limit              = 20000
        aggregate_key_type = "IP"
      }
    }

    visibility_config {
      sampled_requests_enabled   = true
      cloudwatch_metrics_enabled = true
      metric_name                = "${var.prefix}ip-rate-limit"
    }
  }

  visibility_config {
    sampled_requests_enabled   = true
    cloudwatch_metrics_enabled = true
    metric_name                = "${var.prefix}waf"
  }

  tags = var.tags
}

resource "aws_wafv2_web_acl_association" "alb" {
  resource_arn = aws_lb.alb.arn
  web_acl_arn  = aws_wafv2_web_acl.main.arn
}
