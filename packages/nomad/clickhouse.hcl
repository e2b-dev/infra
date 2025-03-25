job "clickhouse" {
  datacenters = ["${zone}"]
  type        = "service"
  node_pool   = "api"


  group "clickhouse" {

    update {
      max_parallel     = 2
      min_healthy_time = "30s"
      healthy_deadline = "4m"

      auto_revert = true
    }

    count = 1

    network {
      port "clickhouse" {
        to     = 9000
        static = 9000
      }

      port "clickhouse_http" {
        static = 8123
        to     = 8123
      }

      port "clickhouse-keeper" {
        static = 9181
        to     = 9181
      }
    }

    service {
      name = "clickhouse"
      port = "clickhouse"

      check {
        type     = "http"
        path     = "/ping"
        port     = "clickhouse_http"
        interval = "10s"
        timeout  = "5s"
      }

      tags = [
        "traefik.enable=true",
        "traefik.http.routers.clickhouse.rule=Host(`clickhouse.service.consul`)",
      ]
    }


    volume "clickhouse" {
      type      = "host"
      read_only = false
      source    = "clickhouse"
    }

    task "clickhouse-server" {
      driver = "docker"

      kill_timeout = "120s"

      resources {
        cpu    = 500
        memory = 2048
      }

      config {
        image = "clickhouse/clickhouse-server:${clickhouse_version}"
        ports = ["clickhouse", "clickhouse_http"]

        ulimit {
          nofile = "262144:262144"
        }


        volumes = [
          "local/config.xml:/etc/clickhouse-server/config.d/gcs.xml",
          # disabled while testing but will pass password to orchestrator in the future
          "local/users.xml:/etc/clickhouse-server/users.d/users.xml",
        ]
      }

      template {
        data        = <<EOF
<?xml version="1.0"?>
<clickhouse>
     # this is undocumented but needed to enable waiting for for shutdown for a custom amount of time 
     # see https://github.com/ClickHouse/ClickHouse/pull/77515 for more details
    <shutdown_wait_unfinished>60</shutdown_wait_unfinished>
    <shutdown_wait_unfinished_queries>1</shutdown_wait_unfinished_queries>
    <path>/clickhouse/data</path>

    <default_replica_path>/clickhouse/tables/{shard}/{database}/{table}</default_replica_path>

    <logger>
        <console>1</console>
         <level>information</level>
    </logger>

    <replicated_merge_tree>
        <storage_policy>s3</storage_policy>
    </replicated_merge_tree>

    <distributed_ddl>
        <path>/clickhouse/task_queue/ddl</path>
    </distributed_ddl>

    <zookeeper>
        <node>
            <host>clickhouse-keeper</host>
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
        <cluster_1>
        <secret>mysecretphrase</secret>
            <shard>
                <internal_replication>true</internal_replication>
                <replica>
                    <host>clickhouse-1</host>
                    <port>9000</port>
                </replica>
                <replica>
                    <host>clickhouse-2</host>
                    <port>9000</port>
                </replica>
            </shard>
        </cluster_1>
    </remote_servers>
    <listen_host>0.0.0.0</listen_host>
    <interserver_http_port>9010</interserver_http_port>
    <interserver_http_host>clickhouse-1</interserver_http_host>
</clickhouse>
EOF
        destination = "local/config.xml"
      }

      template {
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
        destination = "local/users.xml"
      }

      template {
        data        = <<EOF
<?xml version="1.0"?>
<clickhouse>
    <macros>
        <cluster>my_cluster</cluster>
        <shard>01</shard>
        <replica>01</replica>
    </macros>
</clickhouse> 
EOF
        destination = "local/macros.xml"
      }
    }


    task "clickhouse-keeper" {
      driver = "docker"

      resources {
        cpu    = 500
        memory = 2048
      }

      config {
        image = "clickhouse/clickhouse-server:latest"

        ports = ["clickhouse-keeper"]

        volumes = [
          "local/keeper.xml:/etc/clickhouse-server/config.d/keeper.xml",
        ]
      }

      template {
        data        = <<EOF
<?xml version="1.0"?>
<clickhouse>
    <path>/clickhouse/data</path>

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
        <server_id>1</server_id>

         <raft_configuration>
            <server>
                <id>1</id>
                <hostname>clickhouse-keeper</hostname>
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
            <log_local>
                <type>local</type>
                <path>/tmp/lib/clickhouse/coordination/logs/</path>
            </log_local>
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
        destination = "local/keeper.xml"
      }
    }
  }
} 