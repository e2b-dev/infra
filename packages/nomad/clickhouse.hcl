job "clickhouse" {
  datacenters = ["${zone}"]
  type        = "service"
  node_pool   = "api"


  group "clickhouse" {

    update {
      max_parallel     = 1
      min_healthy_time = "30s"
      healthy_deadline = "4m"

      auto_revert = true
    }

    count = 2

    network {
      port "clickhouse" {
        to     = 9000
        static = 9000
      }

      port "clickhouse_http" {
        static = 8123
        to     = 8123
      }
    }

    service {
      name = "clickhouse"
      port = "clickhouse"


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
        image = "clickhouse/clickhouse-server:25.1.5.31"

        # image = "clickhouse/clickhouse-server:25.2.2.39"
        ports = ["clickhouse", "clickhouse_http"]

        ulimit {
          nofile = "262144:262144"
        }


        volumes = [
          "local/config.xml:/etc/clickhouse-server/config.d/gcs.xml",
          "local/users.xml:/etc/clickhouse-server/users.d/users.xml",
        ]
        volume_mount {
          volume      = "clickhouse"
          destination = "/var/lib/clickhouse"
          read_only   = false
        }
      }

      template {
        data        = <<EOF
<?xml version="1.0"?>
<clickhouse>
     # this is undocumented but needed to enable waiting for for shutdown for a custom amount of time 
     # see https://github.com/ClickHouse/ClickHouse/pull/77515 for more details
    <shutdown_wait_unfinished>60</shutdown_wait_unfinished>
    <shutdown_wait_unfinished_queries>1</shutdown_wait_unfinished_queries>
    <storage_configuration>
        <disks>
            <gcs>
                <support_batch_delete>false</support_batch_delete>
                <type>s3</type>
                <endpoint>https://storage.googleapis.com/${gcs_bucket}/${gcs_folder}/</endpoint>
                <access_key_id>${hmac_key}</access_key_id>
                <secret_access_key>${hmac_secret}</secret_access_key>
                <metadata_path>/var/lib/clickhouse/disks/gcs/</metadata_path>
            </gcs>
        </disks>
        <policies>
            <gcs_main>
                <volumes>
                    <main>
                        <disk>gcs</disk>
                    </main>
                </volumes>
            </gcs_main>
        </policies>
    </storage_configuration>
    <merge_tree>
        <storage_policy>gcs_main</storage_policy>
    </merge_tree>
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

    }

  }
} 