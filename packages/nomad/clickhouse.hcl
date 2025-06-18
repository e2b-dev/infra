job "clickhouse" {
  type        = "service"
  node_pool   = "${node_pool}"

%{ for i in range("${server_count}") }
  group "server-${i + 1}" {
    count = 1


    restart {
      interval         = "5m"
      attempts         = 5
      delay            = "15s"
      mode             = "delay"
    }

    constraint {
      attribute = "$${meta.job_constraint}"
      value     = "${job_constraint_prefix}-${i + 1}"
    }

    network {
      mode = "bridge"

      dns {
        servers = ["172.17.0.1", "8.8.8.8", "8.8.4.4", "169.254.169.254"]
      }

      port "clickhouse-http" {
        static = 8123
        to = 8123
      }

      port "clickhouse-metrics" {
        static = "${clickhouse_metrics_port}"
        to = "${clickhouse_metrics_port}"
      }

      port "clickhouse-server" {
        static = "${clickhouse_server_port}"
        to = "${clickhouse_server_port}"
      }
    }

    service {
      name = "clickhouse"
      port = "clickhouse-server"
      tags = ["server-${i + 1}"]

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
           CLICKHOUSE_USER="${username}"
      }

      config {
        image = "clickhouse/clickhouse-server:${clickhouse_version}"
        ports = ["clickhouse-server", "clickhouse-http"]

        ulimit {
          nofile = "262144:262144"
        }

        extra_hosts = [
          "server-${i + 1}.clickhouse.service.consul:127.0.0.1",
        ]

        volumes = [
          "/clickhouse/data:/var/lib/clickhouse",
          "local/config.xml:/etc/clickhouse-server/config.d/config.xml",
          "local/users.xml:/etc/clickhouse-server/users.d/users.xml",
        ]
      }

      resources {
        cpu    = ${cpu_count * 1000}
        memory = ${memory_mb}
      }

      template {
        destination = "local/config.xml"
        data        = <<EOF
<?xml version="1.0"?>
<clickhouse>
<!-- this is undocumented but needed to enable waiting for for shutdown for a custom amount of time  -->
<!-- see https://github.com/ClickHouse/ClickHouse/pull/77515 for more details  -->
    <shutdown_wait_unfinished>60</shutdown_wait_unfinished>
    <shutdown_wait_unfinished_queries>1</shutdown_wait_unfinished_queries>

    <!-- Use up 80% of available RAM to be on the safer side, default is 90% -->
    <max_server_memory_usage_to_ram_ratio>0.8</max_server_memory_usage_to_ram_ratio>

    <logger>
        <formatting>
            <type>json</type>
            <names>
                <date_time>date_time</date_time>
                <thread_id>thread_id</thread_id>
                <level>level</level>
                <query_id>query_id</query_id>
                <logger_name>logger_name</logger_name>
                <message>message</message>
                <source_file>source_file</source_file>
                <source_line>source_line</source_line>
            </names>
        </formatting>
        <console>1</console>
        <level>information</level>
    </logger>

    <default_replica_path>/var/lib/clickhouse/tables/{shard}/{database}/{table}</default_replica_path>

    <remote_servers replace="true">
      <cluster>
        <!-- a secret for servers to use to communicate to each other  -->
        <secret>${server_secret}</secret>
        %{ for j in range("${server_count}") }
        <shard>
          <replica>
            <host>server-${j + 1}.clickhouse.service.consul</host>
            <port>${clickhouse_server_port}</port>
            <user>${username}</user>
            <password>${password}</password>
          </replica>
        </shard>
      %{ endfor }
      </cluster>
    </remote_servers>

    <listen_host>0.0.0.0</listen_host>

    <asynchronous_metric_log>
        <ttl>event_date + INTERVAL 7 DAY</ttl>
    </asynchronous_metric_log>

    <trace_log>
        <ttl>event_date + INTERVAL 7 DAY</ttl>
    </trace_log>

    <text_log>
        <ttl>event_date + INTERVAL 7 DAY</ttl>
    </text_log>

    <latency_log>
        <ttl>event_date + INTERVAL 7 DAY</ttl>
    </latency_log>

    <query_log>
        <ttl>event_date + INTERVAL 7 DAY</ttl>
    </query_log>

    <metric_log>
        <ttl>event_date + INTERVAL 7 DAY</ttl>
    </metric_log>

    <processors_profile_log>
        <ttl>event_date + INTERVAL 7 DAY</ttl>
    </processors_profile_log>

    <asynchronous_metric_log>
        <ttl>event_date + INTERVAL 7 DAY</ttl>
    </asynchronous_metric_log>

    <part_log>
        <ttl>event_date + INTERVAL 7 DAY</ttl>
    </part_log>

    <query_metrics_log>
        <ttl>event_date + INTERVAL 7 DAY</ttl>
    </query_metrics_log>

    <error_log>
        <ttl>event_date + INTERVAL 30 DAY</ttl>
    </error_log>

    <prometheus>
        <port>${clickhouse_metrics_port}</port>
        <endpoint>/metrics</endpoint>
        <metrics>true</metrics>
        <asynchronous_metrics>true</asynchronous_metrics>
        <events>true</events>
        <errors>true</errors>
    </prometheus>

    <tcp_port>${clickhouse_server_port}</tcp_port>
</clickhouse>
EOF
      }

      template {
        destination = "local/users.xml"
        data        = <<EOF
<?xml version="1.0"?>
<clickhouse>
    <users>
        <${username}>
            <password>${password}</password>
            <networks>
              <ip>172.26.64.1/16</ip> <!-- allow Nomad access -->
              <ip>::1</ip> <!-- allow localhost access -->
              <ip>10.0.0.0/8</ip> <!-- restrict to internal traffic -->
            </networks>
            <profile>default</profile>
            <quota>default</quota>
            <access_management>1</access_management>
        </${username}>
    </users>
</clickhouse>
EOF
      }
    }

    task "otel-collector" {
      driver = "docker"

      config {
        network_mode = "host"

        image = "otel/opentelemetry-collector-contrib:0.123.0"
        args = [
          "--config=local/otel.yaml",
          "--feature-gates=pkg.translator.prometheus.NormalizeName",
        ]
      }

      resources {
        cpu    = 250
        memory = 128
      }

      template {
        data        =<<EOF
${otel_agent_config}
EOF
        destination = "local/otel.yaml"
      }

      # Order the sidecar BEFORE the app so it’s ready to receive traffic
      lifecycle {
        sidecar = "true"
        hook = "prestart"
      }
    }
  }
%{ endfor }
}
