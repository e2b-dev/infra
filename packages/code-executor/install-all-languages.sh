#!/bin/bash

# Скрипт для установки всех доступных языков программирования в Piston

set -e

# Убеждаемся, что используем bash
if [ -z "$BASH_VERSION" ]; then
    exec /bin/bash "$0" "$@"
fi

PISTON_URL="${PISTON_URL:-http://localhost:2000}"
MAX_WAIT_TIME=300  # Максимальное время ожидания готовности API (5 минут)
WAIT_INTERVAL=2     # Интервал проверки (2 секунды)

echo "=== Установка всех языков программирования в Piston ==="
echo "Piston URL: $PISTON_URL"
echo ""

# Ожидание готовности Piston API
echo "⏳ Ожидание готовности Piston API..."
WAIT_TIME=0
while ! curl -s --connect-timeout 2 "$PISTON_URL/api/v2/packages" >/dev/null 2>&1; do
    if [ $WAIT_TIME -ge $MAX_WAIT_TIME ]; then
        echo "❌ Таймаут ожидания Piston API"
        exit 1
    fi
    sleep $WAIT_INTERVAL
    WAIT_TIME=$((WAIT_TIME + WAIT_INTERVAL))
    echo "   Ожидание... (${WAIT_TIME}s)"
done
echo "✅ Piston API готов"
echo ""

# Получение списка всех доступных пакетов
echo "📦 Получение списка доступных пакетов..."
PACKAGES_JSON=$(curl -s "$PISTON_URL/api/v2/packages")

if [ -z "$PACKAGES_JSON" ] || [ "$PACKAGES_JSON" = "null" ]; then
    echo "❌ Не удалось получить список пакетов"
    exit 1
fi

# Фильтруем только неустановленные пакеты
UNINSTALLED_PACKAGES=$(echo "$PACKAGES_JSON" | jq -r '[.[] | select(.installed == false)] | .[] | "\(.language)|\(.language_version)"')

TOTAL_PACKAGES=$(echo "$UNINSTALLED_PACKAGES" | wc -l)
echo "📊 Найдено неустановленных пакетов: $TOTAL_PACKAGES"
echo ""

if [ "$TOTAL_PACKAGES" -eq 0 ]; then
    echo "✅ Все пакеты уже установлены!"
    exit 0
fi

# Установка пакетов
echo "🚀 Начинаем установку пакетов..."
echo ""

SUCCESS_COUNT=0
FAIL_COUNT=0
FAILED_PACKAGES=()

# Устанавливаем пакеты по одному
while IFS='|' read -r language version; do
    if [ -z "$language" ] || [ -z "$version" ]; then
        continue
    fi
    
    echo -n "📦 Установка $language-$version... "
    
    # Установка через API
    RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "$PISTON_URL/api/v2/packages" \
        -H "Content-Type: application/json" \
        -d "{\"language\":\"$language\",\"version\":\"$version\"}" 2>&1)
    
    HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
    BODY=$(echo "$RESPONSE" | sed '$d')
    
    if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "201" ]; then
        echo "✅ OK"
        SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
    else
        echo "❌ ОШИБКА (HTTP $HTTP_CODE)"
        echo "   Ответ: $BODY"
        FAIL_COUNT=$((FAIL_COUNT + 1))
        FAILED_PACKAGES+=("$language-$version")
    fi
    
    # Небольшая задержка между установками, чтобы не перегружать систему
    sleep 0.5
    
done <<< "$UNINSTALLED_PACKAGES"

# Итоговая статистика
echo ""
echo "=== Итоговая статистика ==="
echo "✅ Успешно установлено: $SUCCESS_COUNT"
echo "❌ Ошибок: $FAIL_COUNT"

if [ $FAIL_COUNT -gt 0 ]; then
    echo ""
    echo "❌ Пакеты с ошибками:"
    for pkg in "${FAILED_PACKAGES[@]}"; do
        echo "  - $pkg"
    done
    echo ""
    echo "⚠️  Некоторые пакеты не удалось установить"
    exit 1
else
    echo ""
    echo "✅ Все пакеты успешно установлены!"
    exit 0
fi

