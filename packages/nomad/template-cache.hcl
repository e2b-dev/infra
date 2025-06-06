job "template-cache" {
  type = "service"
  datacenters = ["${gcp_zone}"]
  node_pool = "api"

  priority = 80

  group "template-cache" {
    count = 1

    network {
      port "template-cache" {
        static = "${port_number}"
      }
    
      port "status" {
        static = "${status_port_number}"
      }  
    }

    service {
      name = "template-cache"
      port = "${port_name}"

      check {
        type     = "http"
        name     = "health"
        path     = "/health"
        interval = "20s"
        timeout  = "5s"
        port     = "status"
      }
    }

    update {
      # The number of extra instances to run during the update
      max_parallel     = 1
      # Allows to spawn new version of the service before killing the old one
      canary           = 1
      # Time to wait for the canary to be healthy
      min_healthy_time = "10s"
      # Time to wait for the canary to be healthy, if not it will be marked as failed
      healthy_deadline = "30s"
      # Whether to promote the canary if the rest of the group is not healthy
      auto_promote     = true
      # Deadline for the update to be completed
      progress_deadline = "24h"
    }

    task "template-cache" {
      driver = "docker"

      config {
        image        = "nginx:1.27.0"
        network_mode = "host"
        ports        = ["template-cache", "status"]
        volumes = [
          "local:/etc/nginx/",
          "/var/log/template-cache:/var/log/nginx"
        ]
      }

      // TODO: Saner resources
      resources {
        memory_max = 6000
        memory = 2048
        cpu    = 1000
      }

      template {
        left_delimiter  = "[["
        right_delimiter = "]]"
        destination     = "local/nginx.conf"
        change_mode     = "signal"
        change_signal   = "SIGHUP"
        data = <<EOF
# Run as www-data (safe for most Linux systems like Ubuntu)
user nginx;
worker_processes auto;

error_log  /var/log/nginx/error.log notice;
pid        /var/run/nginx.pid;

events {
    worker_connections 1024;
    multi_accept on;
    use epoll;
}

error_log  /dev/stderr notice;

http {
    access_log /dev/stdout;

    sendfile        on;
    keepalive_timeout 65;
    tcp_nodelay on;
    tcp_nopush on;

    default_type application/octet-stream;

    gzip on;
    gzip_disable "msie6";
    gzip_vary on;
    gzip_proxied any;
    gzip_comp_level 6;
    gzip_buffers 16 8k;
    gzip_types text/plain text/css application/json application/javascript text/xml application/xml application/xml+rss text/javascript;

    # Caching zone and path
    proxy_cache_path /mycache levels=1:2 keys_zone=mycache:10m max_size=10g 
             inactive=60m use_temp_path=off;
    proxy_cache mycache;

    server {
        listen ${port_number};

        slice              20m;
        proxy_cache_key    $host$uri$is_args$args$slice_range;
        proxy_set_header   Range $slice_range;
        proxy_http_version 1.1;
        proxy_cache_valid  200 206 1h;

        location / {
            proxy_pass https://storage.googleapis.com;
            proxy_set_header Host storage.googleapis.com;

            # Optional headers for troubleshooting or GCS
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        }
      
        client_body_buffer_size 2M;
        client_max_body_size 2M;
        client_header_buffer_size 1k;
        large_client_header_buffers 2 2M;
        client_body_timeout 60s;
        keepalive_timeout 60;
    }

    server {
      listen ${status_port_number};
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
}
EOF
      }
    }
  }
}