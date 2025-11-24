#!/bin/bash

# Скрипт для проверки работы сервисов code-executor

set -e

cd "$(dirname "$0")"

# Проверка прав доступа к Docker
echo "=== 0. Проверка доступа к Docker ==="
if ! docker info >/dev/null 2>&1; then
    echo "❌ ОШИБКА: Нет доступа к Docker daemon!"
    echo ""
    echo "Решения:"
    echo "  1. Запустите скрипт с sudo: sudo ./check-services.sh"
    echo "  2. Или добавьте пользователя в группу docker:"
    echo "     sudo usermod -aG docker \$USER"
    echo "     (затем выйдите и войдите заново)"
    echo ""
    exit 1
fi
echo "✅ Docker доступен"

echo -e "\n=== 1. Проверка статуса контейнеров ==="
if docker compose ps 2>/dev/null; then
    echo ""
else
    echo "⚠️  Контейнеры не запущены или docker-compose не найден"
    echo "   Запустите: docker compose up -d"
fi

echo -e "\n=== 2. Проверка логов code-executor (последние 20 строк) ==="
if docker compose ps code-executor 2>/dev/null | grep -q "Up"; then
    docker compose logs --tail=20 code-executor 2>/dev/null || echo "⚠️  Не удалось получить логи"
else
    echo "⚠️  Контейнер code-executor не запущен"
fi

echo -e "\n=== 3. Проверка логов piston (последние 20 строк) ==="
if docker compose ps piston 2>/dev/null | grep -q "Up"; then
    docker compose logs --tail=20 piston 2>/dev/null || echo "⚠️  Не удалось получить логи"
else
    echo "⚠️  Контейнер piston не запущен"
fi

echo -e "\n=== 4. Проверка доступности Piston API (порт 2000) ==="
if curl -s --connect-timeout 2 http://localhost:2000/api/v2/runtimes >/dev/null 2>&1; then
    echo "✅ Piston API доступен"
    curl -s http://localhost:2000/api/v2/runtimes | head -20
else
    echo "❌ Piston API недоступен на localhost:2000"
    echo "   Проверьте, что контейнер piston запущен: docker compose ps"
fi

echo -e "\n=== 5. Проверка доступности Code Executor API (порт 8081) ==="
RESPONSE=$(curl -s --connect-timeout 2 -X POST http://localhost:8081/execute \
  -H "Content-Type: application/json" \
  -d '{"lang":"python","code":"print(\"Hello from code-executor!\")","timeout":5}' 2>&1)

if echo "$RESPONSE" | grep -q "stdout"; then
    echo "✅ Code Executor API доступен"
    echo "$RESPONSE" | jq . 2>/dev/null || echo "$RESPONSE"
else
    echo "❌ Code Executor API недоступен или вернул ошибку"
    echo "   Ответ: $RESPONSE"
    echo "   Проверьте, что контейнер code-executor запущен: docker compose ps"
fi

echo -e "\n=== 6. Проверка выполнения тестов ==="
TEST_RESPONSE=$(curl -s --connect-timeout 2 -X POST http://localhost:8081/tests \
  -H "Content-Type: application/json" \
  -d '{
    "lang": "python",
    "code": "import sys\nx = int(sys.stdin.read())\nprint(x * 2)",
    "timeout": 5,
    "tests": [
      {"id": 0, "input": "5"},
      {"id": 1, "input": "10"}
    ]
  }' 2>&1)

if echo "$TEST_RESPONSE" | grep -q '"id":0'; then
    echo "✅ Тесты выполнены успешно"
    echo "$TEST_RESPONSE" | jq . 2>/dev/null || echo "$TEST_RESPONSE"
else
    echo "❌ Тесты не выполнились или вернули ошибку"
    echo "   Ответ: $TEST_RESPONSE"
fi

echo -e "\n=== Проверка завершена ==="

