job "clickhouse" {
  datacenters = ["${zone}"]
  type        = "service"
  node_pool = "api"


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
        to = 9000
        static = 9000
      }
      
      port "clickhouse_http" {
        static = 8123
        to = 8123
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
        data = <<EOF
<?xml version="1.0"?>
<clickhouse>
     # this is undocumented but needed to enable waiting for for shutdown for a custom amount of time 
     # see https://github.com/ClickHouse/ClickHouse/pull/77515 for more details
    <shutdown_wait_unfinished>60</shutdown_wait_unfinished>
    <shutdown_wait_unfinished_queries>1</shutdown_wait_unfinished_queries>
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
EOF
        destination = "local/config.xml"
      }

      template {
        data = <<EOF
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

    task "metrics-collector" {
      driver = "docker"

      lifecycle {
        hook = "poststart"
        sidecar = false
      }

            env {
        CLICKHOUSE_CONNECTION_STRING  = "${clickhouse_connection_string}"
        CLICKHOUSE_USERNAME           = "${clickhouse_username}"
        CLICKHOUSE_PASSWORD           = "${clickhouse_password}"
        CLICKHOUSE_DATABASE           = "${clickhouse_database}"
       
      }

      config {
        image = "golang:1.23"
        # go run github.com/e2b-dev/infra/packages/shared@test-collecting-clickhouse-metrics-on-local-cluster-e2b-1756 -direction up 
        command = "go"
        args = ["run", "github.com/e2b-dev/infra/packages/shared@test-collecting-clickhouse-metrics-on-local-cluster-e2b-1756", "-direction", "up"]
      }

      resources {
        cpu    = 500
        memory = 2048
      }
    }
  }
} 