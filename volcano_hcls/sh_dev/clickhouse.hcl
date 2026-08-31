job "clickhouse" {
  type        = "service"
  datacenters = ["dev-e2b-dc"]
  node_pool   = "api"
  priority    = 75

  group "server-1" {
    count = 1

    restart {
      interval = "5m"
      attempts = 5
      delay    = "15s"
      mode     = "delay"
    }

    constraint {
      attribute = "${meta.role}"
      value     = "api"
    }

    constraint {
      attribute = "${node.unique.name}"
      value     = "nomad-server-api"
    }

    network {
      mode = "host"

      port "clickhouse-http" {
        static = 8123
        to     = 8123
      }

      port "clickhouse-server" {
        static = 9000
        to     = 9000
      }

      port "clickhouse-metrics" {
        static = 9363
        to     = 9363
      }
    }

    service {
      name     = "clickhouse"
      port     = "clickhouse-server"
      tags     = ["server-1"]
      provider = "consul"

      check {
        type     = "http"
        path     = "/ping"
        port     = "clickhouse-http"
        interval = "10s"
        timeout  = "5s"
      }
    }

    task "clickhouse-server" {
      driver = "docker"

      env {
        CLICKHOUSE_USER     = "default"
        CLICKHOUSE_PASSWORD = ""
        CLICKHOUSE_DB       = "dev_e2b_clickhouse"
      }

      config {
        image        = "clickhouse/clickhouse-server:25.4.5.24"
        ports        = ["clickhouse-server", "clickhouse-http", "clickhouse-metrics"]
        network_mode = "host"

        ulimit {
          nofile = "262144:262144"
        }

        volumes = [
          "/var/lib/docker/volumes/clickhouse/_data:/var/lib/clickhouse",
          "local/config.xml:/etc/clickhouse-server/config.d/config.xml",
          "local/users.xml:/etc/clickhouse-server/users.d/users.xml",
        ]
      }

      resources {
        cpu    = 4000
        memory = 2048
      }

      # ClickHouse 主配置
      template {
        destination = "local/config.xml"
        data        = <<EOF
<?xml version="1.0"?>
<clickhouse>
    <shutdown_wait_unfinished>60</shutdown_wait_unfinished>
    <shutdown_wait_unfinished_queries>1</shutdown_wait_unfinished_queries>

    <max_server_memory_usage_to_ram_ratio>0.8</max_server_memory_usage_to_ram_ratio>

    <logger>
        <formatting>
            <type>json</type>
        </formatting>
        <console>1</console>
        <level>information</level>
    </logger>

    <listen_host>0.0.0.0</listen_host>

    <!-- 单节点，不需要分布式集群配置 -->
    <remote_servers replace="true">
        <cluster>
            <secret>dev-e2b-dc-clickhouse-secret</secret>
            <shard>
                <replica>
                    <host>127.0.0.1</host>
                    <port>9000</port>
                    <user>default</user>
                    <password></password>
                </replica>
            </shard>
        </cluster>
    </remote_servers>

    <asynchronous_metric_log>
        <ttl>event_date + INTERVAL 1 DAY</ttl>
    </asynchronous_metric_log>
    <trace_log remove="1"/>
    <text_log>
        <ttl>event_date + INTERVAL 1 DAY</ttl>
    </text_log>
    <query_log>
        <ttl>event_date + INTERVAL 1 DAY</ttl>
    </query_log>
    <query_views_log>
        <ttl>event_date + INTERVAL 1 DAY</ttl>
    </query_views_log>
    <metric_log>
        <ttl>event_date + INTERVAL 1 DAY</ttl>
    </metric_log>
    <part_log>
        <ttl>event_date + INTERVAL 1 DAY</ttl>
    </part_log>
    <processors_profile_log remove="1"/>

    <!-- Prometheus metrics -->
    <prometheus>
        <endpoint>/metrics</endpoint>
        <port>9363</port>
        <metrics>true</metrics>
        <events>true</events>
        <asynchronous_metrics>true</asynchronous_metrics>
    </prometheus>
</clickhouse>
EOF
      }

      # 用户权限配置
      template {
        destination = "local/users.xml"
        data        = <<EOF
<?xml version="1.0"?>
<clickhouse>
    <profiles>
        <default>
            <async_insert>1</async_insert>
            <wait_for_async_insert>1</wait_for_async_insert>
            <async_insert_busy_timeout_min_ms>400</async_insert_busy_timeout_min_ms>
            <async_insert_busy_timeout_max_ms>4000</async_insert_busy_timeout_max_ms>
            <async_insert_max_data_size>104857600</async_insert_max_data_size>
            <distributed_background_insert_batch>1</distributed_background_insert_batch>
            <distributed_background_insert_split_batch_on_failure>1</distributed_background_insert_split_batch_on_failure>
        </default>
    </profiles>

    <users>
        <default>
            <password></password>
            <allow_plaintext_password>1</allow_plaintext_password>
            <networks>
                <ip>127.0.0.1</ip>
                <ip>::1</ip>
                <ip>192.168.162.0/24</ip>
                <ip>172.26.64.0/20</ip>
                <ip>10.0.0.0/8</ip>
            </networks>
            <profile>default</profile>
            <quota>default</quota>
            <access_management>1</access_management>
        </default>
    </users>
</clickhouse>
EOF
      }
    }
  }
}
