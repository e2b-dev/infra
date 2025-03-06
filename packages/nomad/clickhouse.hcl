job "clickhouse" {
  datacenters = ["${zone}"]
  type        = "service"

  group "clickhouse" {
    count = 1

    network {
      port "clickhouse" {
        to = 9000
      }
      
      port "clickhouse_http" {
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

      config {
        image = "clickhouse/clickhouse-server:${clickhouse_version}"
        ports = ["clickhouse", "clickhouse_http"]

        ulimit {
          nofile = "262144:262144"
        }


        volumes = [
          "local/config.xml:/etc/clickhouse-server/config.d/gcs.xml",
          "local/users.xml:/etc/clickhouse-server/users.d/users.xml",
        ]
      }

      template {
        data = <<EOF
<?xml version="1.0"?>
<clickhouse>
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
            <gcs_cache>
                <type>cache</type>
                <disk>gcs</disk>
                <path>/var/lib/clickhouse/disks/gcs_cache/</path>
                <max_size>4Gi</max_size>
            </gcs_cache>
        </disks>
        <policies>
            <gcs_main>
                <volumes>
                    <main>
                        <disk>gcs_cache</disk>
                    </main>
                </volumes>
            </gcs_main>
        </policies>
    </storage_configuration>
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

      resources {
        cpu    = 2000
        memory = 4096
      }
    }
  }
} 