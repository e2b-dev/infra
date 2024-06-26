variable "gcp_zone" {
  type = string
}

variable "client_cluster_size" {
  type = number
}

variable "session_proxy_port_number" {
  type = number
}

variable "session_proxy_port_name" {
  type = string
}

variable "session_proxy_service_name" {
  type = string
}

job "session-proxy" {
  type = "system"
  datacenters = [var.gcp_zone]

  priority = 80

  // TODO: Removable
  constraint {
    operator = "distinct_hosts"
    value    = "true"
  }

  group "session-proxy" {
    network {
      port "session" {
        static = var.session_proxy_port_number
      }
      port "status" {
        static = 3004
      }
    }

    service {
      name = var.session_proxy_service_name
      port = var.session_proxy_port_name
      meta {
        Client = node.unique.id
      }

      check {
        type     = "http"
        name     = "health"
        path     = "/health"
        interval = "20s"
        timeout  = "5s"
        port     = "status"
      }

    }

    task "session-proxy" {
      driver = "docker"

      config {
        // TODO: Fixate version
        image        = "nginx"
        network_mode = "host"
        ports        = [var.session_proxy_port_name, "status"]
        volumes = [
          "local:/etc/nginx/conf.d",
          "/var/log/session-proxy:/var/log/nginx"
        ]
      }

      // TODO: Saner resources
      resources {
        memory_max = 6000
        memory = 6000
        cpu    = 1024
      }

      template {
        left_delimiter  = "[["
        right_delimiter = "]]"
        destination     = "local/load-balancer.conf"
        change_mode     = "signal"
        change_signal   = "SIGHUP"
        data            = <<EOF
map $host $dbk_port {
  default         "";
  "~^(?<p>\d+)-"  ":$p";
}

map $host $dbk_session_id {
  default         "";
  "~-(?<s>\w+)-"  $s;
}

map $http_upgrade $conn_upgrade {
  default     "";
  "websocket" "Upgrade";
}

log_format logger-json escape=json
'{'
'"source": "session-proxy",'
'"time": "$time_iso8601",'
'"resp_body_size": $body_bytes_sent,'
'"host": "$http_host",'
'"address": "$remote_addr",'
'"request_length": $request_length,'
'"method": "$request_method",'
'"uri": "$request_uri",'
'"status": $status,'
'"user_agent": "$http_user_agent",'
'"resp_time": $request_time,'
'"upstream_addr": "$upstream_addr"'
'}';
access_log /var/log/nginx/access.log logger-json;

server {
  listen 3003;

  # DNS server resolved addreses as to <sandbox-id> <ip-address>
  resolver 127.0.0.1 valid=2s;
  resolver_timeout 5s;

  proxy_set_header Host $host;
  proxy_set_header X-Real-IP $remote_addr;

  proxy_set_header Upgrade $http_upgrade;
  proxy_set_header Connection $conn_upgrade;

  proxy_hide_header x-frame-options;

  proxy_http_version 1.1;

  client_body_timeout 86400s;
  client_header_timeout 5s;

  proxy_read_timeout 600s;
  proxy_send_timeout 86400s;

  proxy_cache_bypass 1;
  proxy_no_cache 1;

  client_max_body_size 1024m;
  
  proxy_buffering off;
  proxy_request_buffering off;

  tcp_nodelay on;
  tcp_nopush on;
  sendfile on;

  # send_timeout                600s;

  # proxy_connect_timeout       30s;
  keepalive_requests 2048;
  keepalive_timeout 600s;
  # keepalive_time 86400s;
  # gzip off;

  location / {
    if ($dbk_session_id = "") {
      return 400 "Unsupported session domain";
    }

    proxy_pass $scheme://$dbk_session_id$dbk_port$request_uri;
  }
}

server {
  listen 3004;

  location /health {
    access_log off;
    add_header 'Content-Type' 'application/json';
    return 200 '{"status":"UP"}';
  }

  location /status {
    access_log off;
    stub_status;
    allow all;
  }
}
EOF
      }
    }
  }
}