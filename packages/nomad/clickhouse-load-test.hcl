job "clickhouse-load-test" {
  datacenters = ["${zone}"]
  type = "service"

  group "load-test" {
    count = 1

    task "runner" {
      driver = "raw_exec"

      config {
        command = "/bin/sh"
        args = ["-c", "while true; do echo 'SELECT number, sum(number) OVER (ORDER BY number) as running_sum FROM (SELECT number FROM system.numbers LIMIT 10000000) GROUP BY number ORDER BY number DESC LIMIT 1000000 FORMAT Null' | (date '+%Y-%m-%d %H:%M:%S' && curl -m 300 -s -f 'http://clickhouse.service.consul:8123/' --data-binary @- -u '${username}:${password}' && echo 'true' || echo 'fail') | paste -d' ' - -; sleep 1; done"]
      }

      resources {
        cpu    = 100
        memory = 128
      }
    }
  }
} 