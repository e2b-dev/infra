job "clickhouse-cluster" {
  datacenters = ["${zone}"]
  type        = "service"
  node_pool   = "api"

  group "clickhouse-keeper" {
    count = 1

    network {
      port "keeper" {
        to = 9181
      }
      port "keeper_tcp" {
        to = 9234
      }
    }

    task "keeper" {
      driver = "docker"

      config {
        image = "clickhouse/clickhouse-server:${clickhouse_version}"
        ports = ["keeper", "keeper_tcp"]
        volumes = [
          "local/keeper_config.xml:/etc/clickhouse-server/config.d/keeper_config.xml",
          "local/storage_config.xml:/etc/clickhouse-server/config.d/storage.xml",
        ]
      }

      template {
        data = <<EOH
<clickhouse>
    <logger>
        <console>1</console>
    </logger>
    <keeper_server>
        <tcp_port>9181</tcp_port>
        <server_id>1</server_id>
        <log_storage_path>/var/lib/clickhouse/coordination/log</log_storage_path>
        <snapshot_storage_path>/var/lib/clickhouse/coordination/snapshots</snapshot_storage_path>
        
        <coordination_settings>
            <operation_timeout_ms>10000</operation_timeout_ms>
            <session_timeout_ms>30000</session_timeout_ms>
        </coordination_settings>

        <raft_configuration>
            <server>
                <id>1</id>
                <hostname>{{ env "NOMAD_IP_keeper_tcp" }}</hostname>
                <port>9234</port>
            </server>
        </raft_configuration>
    </keeper_server>

    <storage_configuration>
        <disks>
            <log_local>
                <type>local</type>
                <path>/var/lib/clickhouse/coordination/log/</path>
            </log_local>
            <log_s3_plain>
                <type>s3_plain</type>
                <endpoint>https://storage.googleapis.com/${gcs_bucket}/${gcs_folder}/keeper/logs/</endpoint>
                <access_key_id>${hmac_key}</access_key_id>
                <secret_access_key>${hmac_secret}</secret_access_key>
            </log_s3_plain>
            <snapshot_local>
                <type>local</type>
                <path>/var/lib/clickhouse/coordination/snapshots/</path>
            </snapshot_local>
            <snapshot_s3_plain>
                <type>s3_plain</type>
                <endpoint>https://storage.googleapis.com/${gcs_bucket}/${gcs_folder}/keeper/snapshots/</endpoint>
                <access_key_id>${hmac_key}</access_key_id>
                <secret_access_key>${hmac_secret}</secret_access_key>
            </snapshot_s3_plain>
        </disks>
        <policies>
            <keeper_logs>
                <volumes>
                    <main>
                        <disk>log_s3_plain</disk>
                    </main>
                </volumes>
            </keeper_logs>
            <keeper_snapshots>
                <volumes>
                    <main>
                        <disk>snapshot_s3_plain</disk>
                    </main>
                </volumes>
            </keeper_snapshots>
        </policies>
    </storage_configuration>
</clickhouse>
EOH
        destination = "local/keeper_config.xml"
      }

      resources {
        cpu    = 500
        memory = 1024
      }

      service {
        name = "clickhouse-keeper"
        port = "keeper"
        
        check {
          type     = "tcp"
          port     = "keeper"
          interval = "10s"
          timeout  = "2s"
        }
      }
    }
  }

  group "clickhouse-server-1" {
    count = 1

    network {
      port "http" {
        to = 8123
      }
      port "tcp" {
        to = 9000
      }
      port "interserver" {
        to = 9009
      }
    }

    task "server" {
      driver = "docker"

      kill_timeout = "120s"

      config {
        image = "clickhouse/clickhouse-server:${clickhouse_version}"
        ports = ["http", "tcp", "interserver"]
        
        ulimit {
          nofile = "262144:262144"
        }

        volumes = [
          "local/server_config.xml:/etc/clickhouse-server/config.d/server_config.xml",
          "local/macros.xml:/etc/clickhouse-server/config.d/macros.xml",
          "local/storage_config.xml:/etc/clickhouse-server/config.d/storage.xml",
          "local/users.xml:/etc/clickhouse-server/users.d/users.xml",
          "local/storage:/var/lib/clickhouse"
        ]
      }

      template {
        data = <<EOH
<clickhouse>
    <zookeeper>
        <node>
            <host>{{ range service "clickhouse-keeper" }}{{ .Address }}{{ end }}</host>
            <port>{{ range service "clickhouse-keeper" }}{{ .Port }}{{ end }}</port>
        </node>
    </zookeeper>

    <remote_servers>
        <my_cluster>
            <shard>
                <replica>
                    <host>{{ env "NOMAD_IP_http" }}</host>
                    <port>9000</port>
                </replica>
            </shard>
            <shard>
                <replica>
                    <host>{{ range service "clickhouse-server-2" }}{{ .Address }}{{ end }}</host>
                    <port>9000</port>
                </replica>
            </shard>
        </my_cluster>
    </remote_servers>

    <listen_host>0.0.0.0</listen_host>
    <interserver_http_host>{{ env "NOMAD_IP_interserver" }}</interserver_http_host>
    
    # Enable waiting for shutdown
    <shutdown_wait_unfinished>60</shutdown_wait_unfinished>
    <shutdown_wait_unfinished_queries>1</shutdown_wait_unfinished_queries>
</clickhouse>
EOH
        destination = "local/server_config.xml"
      }

      template {
        data = <<EOH
<clickhouse>
    <storage_configuration>
        <disks>
            <s3_plain>
                <type>s3_plain</type>
                <endpoint>https://storage.googleapis.com/${gcs_bucket}/${gcs_folder}/</endpoint>
                <access_key_id>${hmac_key}</access_key_id>
                <secret_access_key>${hmac_secret}</secret_access_key>
            </s3_plain>
        </disks>
        <policies>
            <s3_plain>
                <volumes>
                    <main>
                        <disk>s3_plain</disk>
                    </main>
                </volumes>
            </s3_plain>
        </policies>
    </storage_configuration>
    <merge_tree>
        <storage_policy>s3_plain</storage_policy>
    </merge_tree>
</clickhouse>
EOH
        destination = "local/storage_config.xml"
      }

      template {
        data = <<EOH
<clickhouse>
    <macros>
        <cluster>my_cluster</cluster>
        <shard>01</shard>
        <replica>01</replica>
    </macros>
</clickhouse>
EOH
        destination = "local/macros.xml"
      }

      template {
        data = <<EOH
<?xml version="1.0"?>
<clickhouse>
    <users>
        <${username}>
            <password_sha256_hex>${password_sha256_hex}</password_sha256_hex>
            <networks>
                <ip>::/0</ip>
            </networks>
            <profile>default</profile>
            <quota>default</quota>
            <access_management>1</access_management>
        </${username}>
    </users>
</clickhouse>
EOH
        destination = "local/users.xml"
      }

      resources {
        cpu    = 1000
        memory = 2048
      }

      service {
        name = "clickhouse-server-1"
        port = "http"
        
        check {
          type     = "http"
          path     = "/ping"
          interval = "10s"
          timeout  = "2s"
        }

        tags = [
          "traefik.enable=true",
          "traefik.http.routers.clickhouse.rule=Host(`clickhouse.service.consul`)",
        ]
      }
    }
  }

  group "clickhouse-server-2" {
    count = 1

    network {
      port "http" {
        to = 8123
      }
      port "tcp" {
        to = 9000
      }
      port "interserver" {
        to = 9009
      }
    }

    task "server" {
      driver = "docker"

      kill_timeout = "120s"

      config {
        image = "clickhouse/clickhouse-server:${clickhouse_version}"
        ports = ["http", "tcp", "interserver"]
        
        ulimit {
          nofile = "262144:262144"
        }

        volumes = [
          "local/server_config.xml:/etc/clickhouse-server/config.d/server_config.xml",
          "local/macros.xml:/etc/clickhouse-server/config.d/macros.xml",
          "local/storage_config.xml:/etc/clickhouse-server/config.d/storage.xml",
          "local/users.xml:/etc/clickhouse-server/users.d/users.xml",
          "local/storage:/var/lib/clickhouse"
        ]
      }

      template {
        data = <<EOH
<clickhouse>
    <zookeeper>
        <node>
            <host>{{ range service "clickhouse-keeper" }}{{ .Address }}{{ end }}</host>
            <port>{{ range service "clickhouse-keeper" }}{{ .Port }}{{ end }}</port>
        </node>
    </zookeeper>

    <remote_servers>
        <my_cluster>
            <shard>
                <replica>
                    <host>{{ range service "clickhouse-server-1" }}{{ .Address }}{{ end }}</host>
                    <port>9000</port>
                </replica>
            </shard>
            <shard>
                <replica>
                    <host>{{ env "NOMAD_IP_http" }}</host>
                    <port>9000</port>
                </replica>
            </shard>
        </my_cluster>
    </remote_servers>

    <listen_host>0.0.0.0</listen_host>
    <interserver_http_host>{{ env "NOMAD_IP_interserver" }}</interserver_http_host>
    
    # Enable waiting for shutdown
    <shutdown_wait_unfinished>60</shutdown_wait_unfinished>
    <shutdown_wait_unfinished_queries>1</shutdown_wait_unfinished_queries>
</clickhouse>
EOH
        destination = "local/server_config.xml"
      }

      template {
        data = <<EOH
<clickhouse>
    <storage_configuration>
        <disks>
            <s3_plain>
                <type>s3_plain</type>
                <endpoint>https://storage.googleapis.com/${gcs_bucket}/${gcs_folder}/</endpoint>
                <access_key_id>${hmac_key}</access_key_id>
                <secret_access_key>${hmac_secret}</secret_access_key>
            </s3_plain>
        </disks>
        <policies>
            <s3_plain>
                <volumes>
                    <main>
                        <disk>s3_plain</disk>
                    </main>
                </volumes>
            </s3_plain>
        </policies>
    </storage_configuration>
    <merge_tree>
        <storage_policy>s3_plain</storage_policy>
    </merge_tree>
</clickhouse>
EOH
        destination = "local/storage_config.xml"
      }

      template {
        data = <<EOH
<clickhouse>
    <macros>
        <cluster>my_cluster</cluster>
        <shard>02</shard>
        <replica>01</replica>
    </macros>
</clickhouse>
EOH
        destination = "local/macros.xml"
      }

      template {
        data = <<EOH
<?xml version="1.0"?>
<clickhouse>
    <users>
        <${username}>
            <password_sha256_hex>${password_sha256_hex}</password_sha256_hex>
            <networks>
                <ip>::/0</ip>
            </networks>
            <profile>default</profile>
            <quota>default</quota>
            <access_management>1</access_management>
        </${username}>
    </users>
</clickhouse>
EOH
        destination = "local/users.xml"
      }

      resources {
        cpu    = 1000
        memory = 2048
      }

      service {
        name = "clickhouse-server-2"
        port = "http"
        
        check {
          type     = "http"
          path     = "/ping"
          interval = "10s"
          timeout  = "2s"
        }
      }
    }
  }
} 