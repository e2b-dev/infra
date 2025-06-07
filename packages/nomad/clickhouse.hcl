job "clickhouse" {
  type        = "service"
  node_pool   = "${node_pool}"

// TODO: Add rolling updates

%{ for i in range("${server_count}") }
  group "server-${i + 1}" {
    count = 1

// TODO: Set restarts

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

      # TODO: Ipv6 isn't working, will be fixed later (works like this for now)
      env {
           AWS_ENABLE_IPV6="false"
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
          "/clickhouse/data/clickhouse-server-${i + 1}:/var/lib/clickhouse",
          "local/config.xml:/etc/clickhouse-server/config.d/config.xml",
          "local/users.xml:/etc/clickhouse-server/users.d/users.xml",
        ]
      }

      resources {
        cpu    = 4000
        memory = 8192
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

    <storage_configuration>
         <disks>
            <s3>
                <type>s3</type>
                <endpoint>https://storage.googleapis.com/${gcs_bucket}/${gcs_folder}/server-${i + 1}/</endpoint>
                <access_key_id>${hmac_key}</access_key_id>
                <secret_access_key>${hmac_secret}</secret_access_key>
                <support_batch_delete>false</support_batch_delete>
                <object_removal_strategy>async</object_removal_strategy>
<!--            <metadata_type>plain_rewritable</metadata_type> -->
            </s3>
        </disks>
           <policies>
            <s3>
                <volumes>
                    <main>
                        <disk>s3</disk>
                    </main>
                </volumes>
            </s3>
        </policies>
    </storage_configuration>

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

    <tcp_port>${clickhouse_server_port}</tcp_port>
</clickhouse>
EOF
      }

# TODO: make sure default user isn't created or drop it (it has no password and it's superuser)
      template {
        destination = "local/users.xml"
        data        = <<EOF
<?xml version="1.0"?>
<clickhouse>
    <users>
        <${username}>
            <password>${password}</password>
            <networks>
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
  }
%{ endfor }
}
