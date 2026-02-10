job "canary-check" {
  datacenters = ["${datacenter}"]
  type        = "batch"
  node_pool   = "${node_pool}"

  periodic {
    cron             = "*/1 * * * *"
    prohibit_overlap = true
  }

  group "canary" {
    count = 1

    restart {
      interval = "5m"
      attempts = 2
      delay    = "15s"
      mode     = "delay"
    }

    task "health-check" {
      driver = "raw_exec"

      config {
        command = "local/health_check.sh"
      }

      template {
        data        = <<EOH
#!/bin/bash
# Don't use set -e so we can run all checks
# set -eo pipefail

FAILED=0

echo "Starting Canary Health Check..."

# 1. Check API Health
echo "Checking API Health..."
if curl -f -s -m 5 http://api.service.consul:${api_port}/health >/dev/null; then
    echo "API OK"
else
    echo "API Health Failed"
    FAILED=1
fi

# 2. Check Orchestrator Health
echo "Checking Orchestrator Health..."
# Use Consul DNS SRV lookup to find the port, or use the service name directly if supported by curl/system
# Since we are in a raw_exec driver, we might not have 'dig' or similar tools guaranteed.
# However, we can use the localhost Consul HTTP API to find the service port.

ORCHESTRATOR_SERVICE=$(curl -s http://localhost:8500/v1/catalog/service/orchestrator)
if [ -z "$ORCHESTRATOR_SERVICE" ] || [ "$ORCHESTRATOR_SERVICE" == "[]" ]; then
    echo "Orchestrator service not found in Consul"
    FAILED=1
else
    # Extract address and port from the first service instance
    # We assume there is at least one instance.
    # In a real cluster, we might want to check all instances.
    # For simplicity, we check the service health endpoint via Consul DNS if possible, but curl doesn't support SRV.
    
    # Alternative: Use the variable passed from Terraform if it matches the registered port.
    # But orchestrator uses dynamic ports or host networking.
    # Let's try to get the port from Consul API using a simple grep/sed/awk approach since jq might not be present.
    
    ORCHESTRATOR_PORT=$(echo $ORCHESTRATOR_SERVICE | grep -o '"ServicePort":[0-9]*' | head -1 | awk -F: '{print $2}')
    ORCHESTRATOR_ADDRESS=$(echo $ORCHESTRATOR_SERVICE | grep -o '"ServiceAddress":"[^"]*"' | head -1 | awk -F'"' '{print $4}')
    
    # If ServiceAddress is empty, use Address
    if [ -z "$ORCHESTRATOR_ADDRESS" ]; then
        ORCHESTRATOR_ADDRESS=$(echo $ORCHESTRATOR_SERVICE | grep -o '"Address":"[^"]*"' | head -1 | awk -F'"' '{print $4}')
    fi

    if [ -n "$ORCHESTRATOR_PORT" ] && [ -n "$ORCHESTRATOR_ADDRESS" ]; then
        echo "Found Orchestrator at $ORCHESTRATOR_ADDRESS:$ORCHESTRATOR_PORT"
        if curl -f -s -m 5 http://$ORCHESTRATOR_ADDRESS:$ORCHESTRATOR_PORT/health >/dev/null; then
            echo "Orchestrator OK"
        else
            echo "Orchestrator Health Failed (http://$ORCHESTRATOR_ADDRESS:$ORCHESTRATOR_PORT/health)"
            FAILED=1
        fi
    else
         echo "Could not determine Orchestrator address/port from Consul"
         FAILED=1
    fi
fi

# 3. Check Template Manager Health
echo "Checking Template Manager Health..."
TEMPLATE_MANAGER_SERVICE=$(curl -s http://localhost:8500/v1/catalog/service/template-manager)
if [ -z "$TEMPLATE_MANAGER_SERVICE" ] || [ "$TEMPLATE_MANAGER_SERVICE" == "[]" ]; then
    echo "Template Manager service not found in Consul"
    FAILED=1
else
    TEMPLATE_MANAGER_PORT=$(echo $TEMPLATE_MANAGER_SERVICE | grep -o '"ServicePort":[0-9]*' | head -1 | awk -F: '{print $2}')
    TEMPLATE_MANAGER_ADDRESS=$(echo $TEMPLATE_MANAGER_SERVICE | grep -o '"ServiceAddress":"[^"]*"' | head -1 | awk -F'"' '{print $4}')
    
    if [ -z "$TEMPLATE_MANAGER_ADDRESS" ]; then
        TEMPLATE_MANAGER_ADDRESS=$(echo $TEMPLATE_MANAGER_SERVICE | grep -o '"Address":"[^"]*"' | head -1 | awk -F'"' '{print $4}')
    fi

    if [ -n "$TEMPLATE_MANAGER_PORT" ] && [ -n "$TEMPLATE_MANAGER_ADDRESS" ]; then
        echo "Found Template Manager at $TEMPLATE_MANAGER_ADDRESS:$TEMPLATE_MANAGER_PORT"
        if curl -f -s -m 5 http://$TEMPLATE_MANAGER_ADDRESS:$TEMPLATE_MANAGER_PORT/health >/dev/null; then
            echo "Template Manager OK"
        else
            echo "Template Manager Health Failed (http://$TEMPLATE_MANAGER_ADDRESS:$TEMPLATE_MANAGER_PORT/health)"
            FAILED=1
        fi
    else
         echo "Could not determine Template Manager address/port from Consul"
         FAILED=1
    fi
fi

# 4. Check Redis (via Consul DNS)
echo "Checking Redis connectivity..."
# Using bash's built-in TCP check feature
if timeout 2 bash -c 'cat < /dev/null > /dev/tcp/redis.service.consul/6379' 2>/dev/null; then
    echo "Redis OK"
else
    echo "Redis Connection Failed"
    FAILED=1
fi

# 5. Check Consul Service Catalog
echo "Checking Consul Service Catalog..."
SERVICES="api orchestrator template-manager redis"
for service in $SERVICES; do
    if curl -s -f "http://localhost:8500/v1/health/service/$service?passing=true" | grep -q "Service"; then
        echo "Service $service is registered and passing checks."
    else
        echo "Service $service is missing or critical in Consul."
        FAILED=1
    fi
done

if [ $FAILED -ne 0 ]; then
    echo "----------------------------------------"
    echo "Canary Check FAILED: Some components are unhealthy."
    exit 1
else
    echo "----------------------------------------"
    echo "Canary Check PASSED: All components are healthy."
    exit 0
fi
EOH
        destination = "local/health_check.sh"
        perms       = "0755"
      }

      resources {
        cpu    = 100
        memory = 64
      }
    }
  }
}
