# Code Executor Service

Сервис для безопасного выполнения кода с использованием [Piston](https://github.com/engineer-man/piston).

## Возможности

- ✅ Выполнение кода через эндпоинт `/execute`
- ✅ Параллельное выполнение тестов через эндпоинт `/tests`
- ✅ Ограничение количества параллельных воркеров (параметр N)
- ✅ Обработка таймаутов выполнения
- ✅ Безопасная изоляция через Piston
- ✅ Быстрая пересборка контейнера с новыми зависимостями

## API

### POST `/execute`

Выполняет код и возвращает результат.

**Input:**
```json
{
  "lang": "python",
  "code": "print('Hello, World!')",
  "timeout": 10
}
```

**Output:**
```json
{
  "stdout": "Hello, World!\n",
  "stderr": ""
}
```

Если выполнение превысило таймаут, в `stderr` будет сообщение об этом.

### POST `/tests`

Выполняет код с несколькими тестами в параллель.

**Input:**
```json
{
  "lang": "python",
  "code": "import sys\nprint(sys.stdin.read())",
  "timeout": 10,
  "tests": [
    {
      "id": 0,
      "input": "test1"
    },
    {
      "id": 1,
      "input": "test2"
    }
  ]
}
```

**Output:**
```json
[
  {
    "id": 0,
    "stdout": "test1\n",
    "stderr": ""
  },
  {
    "id": 1,
    "stdout": "test2\n",
    "stderr": ""
  }
]
```

## Установка и запуск

### Требования

1. **Piston** должен быть запущен и доступен. См. [инструкции по установке Piston](https://github.com/engineer-man/piston).
2. **Docker Compose** для автоматической установки всех языков программирования.

### Автоматическая установка всех языков

При запуске через `docker compose up`, все доступные языки программирования автоматически устанавливаются в Piston:

1. Контейнер `piston` запускается и ждет готовности API
2. Контейнер `piston-install-languages` автоматически устанавливает все доступные языки (около 76 языков, 114 пакетов)
3. Контейнер `code-executor` запускается только после успешной установки всех языков

Это происходит автоматически при первом запуске или при пересоздании контейнеров.

### Локальная разработка

```bash
# Установить зависимости
make deps

# Запустить в режиме разработки
make run
```

Сервис будет доступен на `http://localhost:8080`.

### Запуск с Docker Compose (рекомендуется)

```bash
# Запустить все сервисы (включая автоматическую установку языков)
docker compose up -d

# Просмотр логов установки языков
docker compose logs -f piston-install-languages

# Проверка установленных языков
./check-languages-support.sh
```

При первом запуске установка всех языков может занять несколько минут (в зависимости от скорости интернета и количества языков).

### Запуск с Docker (без автоматической установки)

```bash
# Собрать образ
docker build -t code-executor-ainosov:latest -f Dockerfile ..

# Запустить контейнер
docker run -p 8080:8080 \
  -e PISTON_URL=http://piston:2000 \
  code-executor-ainosov:latest \
  --port 8080 \
  --workers 10 \
  --piston-url http://piston:2000
```

### Параметры запуска

- `--port` — порт для HTTP сервера (по умолчанию: 8080). Если порт занят, автоматически будет выбран свободный порт
- `--workers` — количество воркеров для параллельного выполнения (по умолчанию: 10)
- `--piston-url` — URL Piston API (по умолчанию: http://localhost:2000)
- `--debug` — включить режим отладки

**Примечание:** Если указанный порт занят, сервис автоматически найдет и использует свободный порт. В логах будет предупреждение о смене порта.

## Настройка Piston

Piston должен быть запущен отдельно. Рекомендуется использовать Docker Compose:

```yaml
version: '3.8'
services:
  piston:
    image: ghcr.io/engineer-man/piston:latest
    ports:
      - "2000:2000"
    volumes:
      - piston-data:/piston
    restart: unless-stopped

volumes:
  piston-data:
```

## Быстрая пересборка контейнера

Для добавления новых зависимостей или изменения конфигурации:

1. Обновите код
2. Пересоберите образ:
   ```bash
   docker build -t code-executor-ainosov:latest -f Dockerfile ..
   ```
3. Перезапустите контейнер

## Безопасность

Сервис использует Piston для изоляции выполнения кода. Piston обеспечивает:
- Изоляцию через Docker контейнеры
- Ограничение ресурсов (CPU, память)
- Таймауты выполнения
- Ограничение системных вызовов

Для дополнительной безопасности рекомендуется:
- Использовать seccomp профили
- Настроить сетевую изоляцию
- Ограничить доступ к файловой системе

## Примеры использования

### Выполнение Python кода

```bash
curl -X POST http://localhost:8080/execute \
  -H "Content-Type: application/json" \
  -d '{
    "lang": "python",
    "code": "print(2 + 2)",
    "timeout": 5
  }'
```

### Выполнение тестов

```bash
curl -X POST http://localhost:8080/tests \
  -H "Content-Type: application/json" \
  -d '{
    "lang": "python",
    "code": "import sys\nx = int(sys.stdin.read())\nprint(x * 2)",
    "timeout": 5,
    "tests": [
      {"id": 0, "input": "5"},
      {"id": 1, "input": "10"}
    ]
  }'
```

## Поддерживаемые языки

Сервис поддерживает все языки, доступные в Piston, включая:
- Python
- JavaScript/TypeScript
- Java
- C/C++
- Go
- Rust
- Ruby
- PHP
- И другие

Список доступных языков можно получить через Piston API: `GET /api/v2/runtimes`

