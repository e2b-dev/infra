locals {
  ingress_logs_path_prefix   = "ingress"
  ingress_logs_path_location = "s3://${data.aws_s3_bucket.load_balancer_logs.id}/${local.ingress_logs_path_prefix}/AWSLogs/${data.aws_caller_identity.current.account_id}/elasticloadbalancing/${data.aws_region.current.name}"
}

data "aws_s3_bucket" "load_balancer_logs" {
  bucket = module.init.load_balancer_logs_bucket_name
}

resource "aws_glue_catalog_database" "ingress_load_balancer_logs" {
  name        = "${var.prefix}ingress_load_balancer_logs"
  description = "access logs for ingress load balancer"
}

resource "aws_glue_catalog_table" "ingress_load_balancer_logs" {
  name          = "${var.prefix}access_logs"
  table_type    = "EXTERNAL_TABLE"
  database_name = aws_glue_catalog_database.ingress_load_balancer_logs.name

  partition_keys {
    name = "day"
    type = "string"
  }

  parameters = {
    "projection.enabled"           = "true"
    "projection.day.type"          = "date"
    "projection.day.range"         = "2022/01/01,NOW"
    "projection.day.format"        = "yyyy/MM/dd"
    "projection.day.interval"      = "1"
    "projection.day.interval.unit" = "DAYS"
    "storage.location.template"    = "${local.ingress_logs_path_location}/$${day}"
  }

  storage_descriptor {
    location      = local.ingress_logs_path_location
    input_format  = "org.apache.hadoop.mapred.TextInputFormat"
    output_format = "org.apache.hadoop.hive.ql.io.HiveIgnoreKeyTextOutputFormat"

    ser_de_info {
      serialization_library = "org.apache.hadoop.hive.serde2.RegexSerDe"

      parameters = {
        "serialization.format" = 1
        "input.regex"          = "([^ ]*) ([^ ]*) ([^ ]*) ([^ ]*):([0-9]*) ([^ ]*)[:-]([0-9]*) ([-.0-9]*) ([-.0-9]*) ([-.0-9]*) (|[-0-9]*) (-|[-0-9]*) ([-0-9]*) ([-0-9]*) \\\"([^ ]*) (.*) (- |[^ ]*)\\\" \\\"([^\\\"]*)\\\" ([A-Z0-9-_]+) ([A-Za-z0-9.-]*) ([^ ]*) \\\"([^\\\"]*)\\\" \\\"([^\\\"]*)\\\" \\\"([^\\\"]*)\\\" ([-.0-9]*) ([^ ]*) \\\"([^\\\"]*)\\\" \\\"([^\\\"]*)\\\" \\\"([^ ]*)\\\" \\\"([^\\s]+?)\\\" \\\"([^\\s]+)\\\" \\\"([^ ]*)\\\" \\\"([^ ]*)\\\" ?([^ ]*)?"
      }
    }

    columns {
      name = "type"
      type = "string"
    }

    columns {
      name = "time"
      type = "string"
    }

    columns {
      name = "elb"
      type = "string"
    }

    columns {
      name = "client_ip"
      type = "string"
    }

    columns {
      name = "client_port"
      type = "int"
    }

    columns {
      name = "target_ip"
      type = "string"
    }

    columns {
      name = "target_port"
      type = "int"
    }

    columns {
      name = "request_processing_time"
      type = "double"
    }

    columns {
      name = "target_processing_time"
      type = "double"
    }

    columns {
      name = "response_processing_time"
      type = "double"
    }

    columns {
      name = "elb_status_code"
      type = "int"
    }

    columns {
      name = "target_status_code"
      type = "string"
    }

    columns {
      name = "received_bytes"
      type = "bigint"
    }

    columns {
      name = "sent_bytes"
      type = "bigint"
    }

    columns {
      name = "request_verb"
      type = "string"
    }

    columns {
      name = "request_url"
      type = "string"
    }

    columns {
      name = "request_proto"
      type = "string"
    }

    columns {
      name = "user_agent"
      type = "string"
    }

    columns {
      name = "ssl_cipher"
      type = "string"
    }

    columns {
      name = "ssl_protocol"
      type = "string"
    }

    columns {
      name = "target_group_arn"
      type = "string"
    }

    columns {
      name = "trace_id"
      type = "string"
    }

    columns {
      name = "domain_name"
      type = "string"
    }

    columns {
      name = "chosen_cert_arn"
      type = "string"
    }

    columns {
      name = "matched_rule_priority"
      type = "string"
    }

    columns {
      name = "request_creation_time"
      type = "string"
    }

    columns {
      name = "actions_executed"
      type = "string"
    }

    columns {
      name = "redirect_url"
      type = "string"
    }

    columns {
      name = "lambda_error_reason"
      type = "string"
    }

    columns {
      name = "target_port_list"
      type = "string"
    }

    columns {
      name = "target_status_code_list"
      type = "string"
    }

    columns {
      name = "classification"
      type = "string"
    }

    columns {
      name = "classification_reason"
      type = "string"
    }

    columns {
      name = "conn_trace_id"
      type = "string"
    }
  }
}

resource "aws_athena_workgroup" "operations" {
  name          = "${var.prefix}operations"
  force_destroy = true

  configuration {
    enforce_workgroup_configuration    = true
    publish_cloudwatch_metrics_enabled = true

    result_configuration {
      output_location = "s3://${data.aws_s3_bucket.load_balancer_logs.bucket}/athena-output/"
    }
  }
}
