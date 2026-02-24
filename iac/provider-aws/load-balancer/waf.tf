# --- WAF v2 Web ACL (ALB only, WAF not supported on NLB) ---
resource "aws_wafv2_web_acl" "main" {
  name  = "${var.prefix}waf"
  scope = "REGIONAL"

  default_action {
    allow {}
  }

  # Rate limit: API sandbox creation per API key header
  rule {
    name     = "api-throttle-by-api-key"
    priority = 1

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
    priority = 2

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
