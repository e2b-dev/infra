job "clickhouse" {
  datacenters = ["${zone}"]
  type        = "service"
  node_pool   = "api"

  group "keeper" {
    count = 1

    update {
      max_parallel = 0
    }

    network {
      mode = "bridge"
      hostname = "clickhouse-keeper-1.service.consul"

      dns {
        servers = ["172.17.0.1", "8.8.8.8", "8.8.4.4", "169.254.169.254"]
      }

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
      name = "clickhouse-keeper-1"
      port = "keeper"
    }

    service {
      name = "clickhouse-keeper-raft-1"
      port = "raft"
    }

    task "clickhouse-keeper-1" {
      driver = "docker"

      config {
        image = "clickhouse/clickhouse-server:latest"
        ports = [
          "keeper",
          "raft",
        ]

        extra_hosts = [
          "clickhouse-keeper-raft-1.service.consul:127.0.0.1",
          "clickhouse-keeper-1.service.consul:127.0.0.1",
        ]

        mount {
          type = "bind"
          target = "/var/lib/clickhouse"
          source = "/mnt/nfs/clickhouse/data/clickhouse-keeper"
          readonly = false

          bind_options {
            propagation = "rshared"
          }
        }

        volumes = [
          "local/keeper.xml:/etc/clickhouse-server/config.d/keeper_config.xml",
        ]
      }

      resources {
        cpu    = 500
        memory = 2048
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
        <snapshot_storage_disk>snapshot_s3</snapshot_storage_disk>
        <latest_snapshot_storage_disk>snapshot_s3</latest_snapshot_storage_disk>

        <state_storage_disk>state_s3</state_storage_disk>
        <latest_state_storage_disk>state_s3</latest_state_storage_disk>

        <tcp_port>9181</tcp_port>
        <server_id>1</server_id>

         <raft_configuration>
            <server>
                <id>1</id>
                <hostname>clickhouse-keeper-raft-1.service.consul</hostname>
                <port>9234</port>
            </server>
        </raft_configuration>

        <coordination_settings>
            <operation_timeout_ms>10000</operation_timeout_ms>
            <session_timeout_ms>30000</session_timeout_ms>
        </coordination_settings>
    </keeper_server>

    <storage_configuration>
        <disks>
            <log_s3>
                <type>s3</type>
                <endpoint>https://storage.googleapis.com/${gcs_bucket}/clickhouse-data/keeper/logs/</endpoint>
                <access_key_id>${hmac_key}</access_key_id>
                <secret_access_key>${hmac_secret}</secret_access_key>
                <support_batch_delete>false</support_batch_delete>
            </log_s3>
            <snapshot_s3>
                <type>s3</type>
                <endpoint>https://storage.googleapis.com/${gcs_bucket}/clickhouse-data/keeper/snapshots/</endpoint>
                <access_key_id>${hmac_key}</access_key_id>
                <secret_access_key>${hmac_secret}</secret_access_key>
                <support_batch_delete>false</support_batch_delete>
            </snapshot_s3>
            <state_s3>
                <type>s3</type>
                <endpoint>https://storage.googleapis.com/${gcs_bucket}/clickhouse-data/keeper/state/</endpoint>
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

  group "server" {
    count = 1

    update {
      max_parallel = 0
    }

    network {
      mode = "bridge"
      hostname = "clickhouse-server-1.service.consul"

      dns {
        servers = ["172.17.0.1", "8.8.8.8", "8.8.4.4", "169.254.169.254"]
      }

      port "native" {
        static = 9000
        to = 9000
      }

      port "http" {
        static = 8123
        to = 8123
      }

      port "interserver" {
        static = 9009
        to = 9009
      }
    }

    service {
      name = "clickhouse-1"
      port = "http"
    }

    service {
      name = "clickhouse-interserver-1"
      port = "interserver"
    }

    service {
      name = "clickhouse-native-1"
      port = "native"
    }

    task "clickhouse-server-1" {
      driver = "docker"

      config {
        image = "clickhouse/clickhouse-server:latest"
        ports = [
          "http",
          "native",
          "interserver",
        ]

        extra_hosts = [
          "clickhouse-1.service.consul:127.0.0.1",
          "clickhouse-interserver-1.service.consul:127.0.0.1",
          "clickhouse-native-1.service.consul:127.0.0.1",
        ]

        mount {
          type = "bind"
          target = "/var/lib/clickhouse"
          source = "/mnt/nfs/clickhouse/data/clickhouse-server"
          readonly = false

          bind_options {
            propagation = "rshared"
          }
        }

        volumes = [
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
        memory = 2048
      }

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
        <node>
            <host>clickhouse-keeper-1.service.consul</host>
            <port>9181</port>
        </node>
    </zookeeper>
    <storage_configuration>
         <disks>
            <s3>
                <support_batch_delete>false</support_batch_delete>
                <type>s3</type>
                <endpoint>https://storage.googleapis.com/${gcs_bucket}/${gcs_folder}/</endpoint>
                <access_key_id>${hmac_key}</access_key_id>
                <secret_access_key>${hmac_secret}</secret_access_key>
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
                <replica>
                    <host>clickhouse-native-1.service.consul</host>
                    <port>9000</port>
                </replica>
            </shard>
        </cluster>
    </remote_servers>

    <listen_host>0.0.0.0</listen_host>

    <interserver_http_port>9009</interserver_http_port>
    <interserver_http_host>clickhouse-interserver-1.service.consul</interserver_http_host>
</clickhouse>
EOF
      }

      template {
        destination = "local/users.xml"
        data        = <<EOF
<?xml version="1.0"?>
<clickhouse>
    <users>
        <bar>
            <password>password</password>
            <networks>
                <ip>::/0</ip>
            </networks>
            <profile>default</profile>
            <quota>default</quota>
            <access_management>1</access_management>
        </bar>
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
        <replica>01</replica>
    </macros>
</clickhouse> 
EOF
      }
    }
  }
} 