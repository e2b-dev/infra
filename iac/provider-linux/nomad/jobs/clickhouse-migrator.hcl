job "clickhouse-migrator" {
  type      = "batch"
  node_pool = "${node_pool}"

%{ for i in range("${server_count}") }
   group "migrator-${i + 1}" {
     count = 1

     restart {
       interval = "5m"
       attempts = 5
       delay    = "15s"
       mode     = "delay"
     }

    # Prestart task to create database
    task "create-db" {
      driver = "docker"
      
      lifecycle {
        hook = "prestart"
        sidecar = false
      }

      config {
        network_mode = "host"
        image = "alpine/curl:latest"
        
        command = "/bin/sh"
        args = ["-c", <<-EOF
          set -e
          
          CLICKHOUSE_HOST="server-${i + 1}.clickhouse.service.consul"
          CLICKHOUSE_PORT="${clickhouse_server_port}"
          CLICKHOUSE_USER="${username}"
          CLICKHOUSE_PASSWORD="${password}"
          DB_NAME="${clickhouse_database}"
          
          echo "Waiting for ClickHouse at $CLICKHOUSE_HOST:$CLICKHOUSE_PORT to be ready..."
          MAX_RETRIES=30
          RETRY_COUNT=0
          
          while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
            if curl -s -u "$CLICKHOUSE_USER:$CLICKHOUSE_PASSWORD" \
                "http://$CLICKHOUSE_HOST:$CLICKHOUSE_PORT/ping" > /dev/null 2>&1; then
              echo "ClickHouse is ready!"
              break
            fi
            
            RETRY_COUNT=$((RETRY_COUNT + 1))
            echo "Waiting... (attempt $RETRY_COUNT/$MAX_RETRIES)"
            sleep 2
          done
          
          if [ $RETRY_COUNT -eq $MAX_RETRIES ]; then
            echo "Error: ClickHouse is not ready after $MAX_RETRIES attempts"
            exit 1
          fi
          
          echo "Creating database '$DB_NAME'..."
          curl -s -u "$CLICKHOUSE_USER:$CLICKHOUSE_PASSWORD" \
            "http://$CLICKHOUSE_HOST:$CLICKHOUSE_PORT/" \
            -d "CREATE DATABASE IF NOT EXISTS $DB_NAME"
          
          echo "Database created successfully!"
        EOF
        ]
      }

      resources {
        cpu    = 100
        memory = 64
      }
    }

    task "migrator" {
      driver = "docker"

      env {
        GOOSE_DBSTRING = "${clickhouse_connection_string}"
      }

      config {
        network_mode = "host"
%{ if clickhouse_migrator_image != "" }
        image = "${clickhouse_migrator_image}"
%{ else }
  %{ if docker_image_prefix != "" }
        image = "${docker_image_prefix}/clickhouse/clickhouse-migrator:latest"
  %{ else }
        image = "clickhouse/clickhouse-migrator:latest"
  %{ endif }
%{ endif }
      }

      resources {
        cpu    = 250
        memory = 128
      }
    }
  }
%{ endfor }
}
