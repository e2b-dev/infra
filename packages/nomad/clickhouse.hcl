job "clickhouse" {
  type        = "service"
  node_pool   = "monitoring"

  %{ for i in range("${keeper_count}") }
  group "keeper-${i + 1}" {
    count = 1

    constraint {
      attribute = "${meta.clickhouse_keeper_index_attribute}"
      value     = "${i + 1}"
    }

    update {
      // TODO: Can we use this to roll updates properly?
      max_parallel = 0
    }

    network {
      port "keeper" {
        static = 9181
        to = 9181
      }

      port "raft" {
        static = 9234
        to = 9234
      }
    }

    service {
      name = "clickhouse-keeper-${i + 1}"
      port = "keeper"
    }

    service {
      name = "clickhouse-keeper-raft-${i + 1}"
      port = "raft"
    }

    task "clickhouse-keeper" {
      driver = "docker"

      config {
        image = "clickhouse/clickhouse-server:25.3"
        ports = ["keeper", "raft"]

        extra_hosts = [
          "clickhouse-keeper-${i + 1}.service.consul:127.0.0.1",
          "clickhouse-keeper-raft-${i + 1}.service.consul:127.0.0.1",
        ]

        volumes = [
          "/clickhouse/data/clickhouse-keeper-${i + 1}:/var/lib/clickhouse",
          "local/keeper.xml:/etc/clickhouse-server/config.d/keeper_config.xml",
        ]
      }

      resources {
        cpu    = 400
        memory = 512
      }

      template {
        destination = "local/keeper.xml"
        data        = <<EOF
<?xml version="1.0"?>
<clickhouse>

    <logger>
        <console>1</console>
        <level>information</level>
    </logger>

    <keeper_server>
        <log_storage_disk>log_s3</log_storage_disk>
        <latest_log_storage_disk>log_local</latest_log_storage_disk>

        <snapshot_storage_disk>snapshot_s3</snapshot_storage_disk>
        <latest_snapshot_storage_disk>snapshot_s3</latest_snapshot_storage_disk>

        <state_storage_disk>state_s3</state_storage_disk>
        <latest_state_storage_disk>state_s3</latest_state_storage_disk>

        <tcp_port>9181</tcp_port>
        <server_id>${i + 1}</server_id>

         <raft_configuration>
         %{ for j in range("${keeper_count}") }
            <server>
                <id>${j + 1}</id>
                <hostname>clickhouse-keeper-raft-${j + 1}.service.consul</hostname>
                <port>9234</port>
            </server>
            %{ endfor }
        </raft_configuration>

        <coordination_settings>
            <operation_timeout_ms>10000</operation_timeout_ms>
            <session_timeout_ms>30000</session_timeout_ms>
        </coordination_settings>
    </keeper_server>

    <storage_configuration>
        <disks>
            <log_local>
                <type>local</type>
                <path>/var/lib/clickhouse/coordination/logs/</path>
            </log_local>
            <log_s3>
                <type>s3</type>
                <endpoint>https://storage.googleapis.com/${gcs_bucket}/${gcs_folder}/keeper-${i + 1}/logs/</endpoint>
                <access_key_id>${hmac_key}</access_key_id>
                <secret_access_key>${hmac_secret}</secret_access_key>
                <support_batch_delete>false</support_batch_delete>
            </log_s3>
            <snapshot_s3>
                <type>s3</type>
                <endpoint>https://storage.googleapis.com/${gcs_bucket}/${gcs_folder}/keeper-${i + 1}/snapshots/</endpoint>
                <access_key_id>${hmac_key}</access_key_id>
                <secret_access_key>${hmac_secret}</secret_access_key>
                <support_batch_delete>false</support_batch_delete>
            </snapshot_s3>
            <state_s3>
                <type>s3</type>
                <endpoint>https://storage.googleapis.com/${gcs_bucket}/${gcs_folder}/keeper-${i + 1}/state/</endpoint>
                <access_key_id>${hmac_key}</access_key_id>
                <secret_access_key>${hmac_secret}</secret_access_key>
                <support_batch_delete>false</support_batch_delete>
            </state_s3>
        </disks>
    </storage_configuration>
</clickhouse> 
EOF
      }
    }
  }
  %{ endfor }

%{ for i in range("${server_count}") }
  group "server-${i + 1}" {
    count = 1

    constraint {
      attribute = "${meta.clickhouse_server_index_attribute}"
      value     = "${i + 1}"
    }

    update {
      max_parallel = 0
    }

    network {
      port "http" {
        static = 8123
        to = 8123
      }

      port "clickhouse" {
        static = 9000
        to = 9000
      }

      port "interserver" {
        static = 9009
        to = 9009
      }
    }

    service {
      name = "clickhouse-http-${i + 1}"
      port = "http"
    }

    service {
      name = "clickhouse-${i + 1}"
      port = "clickhouse"
    }

    service {
      name = "clickhouse-interserver-${i + 1}"
      port = "interserver"
    }

    task "clickhouse-server" {
      driver = "docker"

      config {
        image = "clickhouse/clickhouse-server:25.3"
        ports = ["http", "clickhouse", "interserver"]

        extra_hosts = [
          "clickhouse-http-${i + 1}.service.consul:127.0.0.1",
          "clickhouse-${i + 1}.service.consul:127.0.0.1",
          "clickhouse-interserver-${i + 1}.service.consul:127.0.0.1",
        ]

        volumes = [
          "/clickhouse/data/clickhouse-server-${i + 1}:/var/lib/clickhouse",
          "local/config.xml:/etc/clickhouse-server/config.d/config.xml",
          "local/users.xml:/etc/clickhouse-server/users.d/users.xml",
          "local/macros.xml:/etc/clickhouse-server/config.d/macros.xml",
        ]

        ulimit {
          nofile = "262144:262144"
        }
      }

      resources {
        cpu    = 500
        memory = 1024
      }

      // TODO: Join the configs for server
      template {
        destination = "local/config.xml"
        data        = <<EOF
<?xml version="1.0"?>
<clickhouse>
     # this is undocumented but needed to enable waiting for for shutdown for a custom amount of time 
     # see https://github.com/ClickHouse/ClickHouse/pull/77515 for more details
    <shutdown_wait_unfinished>60</shutdown_wait_unfinished>
    <shutdown_wait_unfinished_queries>1</shutdown_wait_unfinished_queries>

    <logger>
        <console>1</console>
         <level>information</level>
    </logger>

    <replicated_merge_tree>
        <storage_policy>s3</storage_policy>
    </replicated_merge_tree>

    <distributed_ddl>
        <path>/var/lib/clickhouse/task_queue/ddl</path>
    </distributed_ddl>

    <default_replica_path>/var/lib/clickhouse/tables/{shard}/{database}/{table}</default_replica_path>

    <zookeeper>
        %{ for j in range("${keeper_count}") }
        <node>
            <host>clickhouse-keeper-${j + 1}.service.consul</host>
            <port>9181</port>
        </node>
        %{ endfor }
    </zookeeper>
    <storage_configuration>
         <disks>
            <s3>
                <type>s3</type>
                <endpoint>https://storage.googleapis.com/${gcs_bucket}/${gcs_folder}/server-${i + 1}/</endpoint>
                <access_key_id>${hmac_key}</access_key_id>
                <secret_access_key>${hmac_secret}</secret_access_key>
                <support_batch_delete>false</support_batch_delete>
                # <metadata_type>plain_rewritable</metadata_type>
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
        <secret>mysecretphrase</secret>
            <shard>
                <internal_replication>true</internal_replication>
                %{ for j in range("${server_count}") }
                <replica>
                    <host>clickhouse-${j + 1}.service.consul</host>
                    <port>9000</port>
                </replica>
                %{ endfor }
            </shard>
        </cluster>
    </remote_servers>

    <listen_host>0.0.0.0</listen_host>

    <http_port>8123</http_port>
    <tcp_port>9000</tcp_port>

    <interserver_http_host>clickhouse-interserver-${i + 1}.service.consul</interserver_http_host>
    <interserver_http_port>9009</interserver_http_port>
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
EOF
      }

      template {
        destination = "local/macros.xml"
        data        = <<EOF
<?xml version="1.0"?>
<clickhouse>
    <macros>
        <cluster>cluster</cluster>
        <shard>01</shard>
        <replica>0${i + 1}</replica>
    </macros>
</clickhouse> 
EOF
      }
    }
  }
%{ endfor }
}
