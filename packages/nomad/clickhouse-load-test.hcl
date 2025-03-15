job "clickhouse-load-test" {
  datacenters = ["${zone}"]
  type = "service"

  group "load-test" {
    count = 1

    task "runner" {
      driver = "raw_exec"

      config {
        command = "/bin/sh"
        args = ["-c", "echo 'SELECT * FROM LIMIT 1000000' | curl -m 300 -s -f 'http://clickhouse.service.consul:8123/' --data-binary @- -u '${username}:${password}'"]
      }

      resources {
        cpu    = 100
        memory = 128
      }
    }
  }
} 