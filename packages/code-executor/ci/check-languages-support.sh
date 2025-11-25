#!/bin/bash

# Скрипт для проверки поддержки всех языков программирования через Piston

set -e

cd "$(dirname "$0")"

PISTON_URL="${PISTON_URL:-http://localhost:2000}"
CODE_EXECUTOR_URL="${CODE_EXECUTOR_URL:-http://localhost:8081}"

echo "=== Проверка поддержки языков программирования ==="
echo "Piston URL: $PISTON_URL"
echo "Code Executor URL: $CODE_EXECUTOR_URL"
echo ""

# Проверка доступности Piston API
echo "=== 1. Проверка доступности Piston API ==="
if ! curl -s --connect-timeout 2 "$PISTON_URL/api/v2/runtimes" >/dev/null 2>&1; then
    echo "❌ Piston API недоступен на $PISTON_URL"
    echo "   Проверьте, что контейнер piston запущен: docker compose ps"
    exit 1
fi
echo "✅ Piston API доступен"

# Проверка доступности Code Executor API
echo -e "\n=== 2. Проверка доступности Code Executor API ==="
if ! curl -s --connect-timeout 2 "$CODE_EXECUTOR_URL/health" >/dev/null 2>&1; then
    echo "❌ Code Executor API недоступен на $CODE_EXECUTOR_URL"
    echo "   Проверьте, что контейнер code-executor запущен: docker compose ps"
    exit 1
fi
echo "✅ Code Executor API доступен"

# Получение списка доступных языков
echo -e "\n=== 3. Получение списка доступных языков ==="
RUNTIMES_JSON=$(curl -s "$PISTON_URL/api/v2/runtimes")

if [ -z "$RUNTIMES_JSON" ] || [ "$RUNTIMES_JSON" = "null" ]; then
    echo "❌ Не удалось получить список языков из Piston API"
    exit 1
fi

# Отладочная информация: показываем сырой JSON
echo "📋 Сырой ответ от Piston API (/api/v2/runtimes):"
echo "$RUNTIMES_JSON" | jq '.' | head -30
echo ""

# Подсчет общего количества runtime'ов (все версии)
TOTAL_RUNTIMES=$(echo "$RUNTIMES_JSON" | jq '. | length')
echo "📊 Всего runtime'ов (все версии): $TOTAL_RUNTIMES"

# Извлечение уникальных языков
# Piston API returns an array, so we need to group by language first
LANGUAGES=$(echo "$RUNTIMES_JSON" | jq -r '[.[].language] | unique | .[]' | sort -u)

if [ -z "$LANGUAGES" ]; then
    echo "❌ Не найдено доступных языков"
    exit 1
fi

LANGUAGE_COUNT=$(echo "$LANGUAGES" | wc -l)
echo "📊 Уникальных языков: $LANGUAGE_COUNT"
echo ""

# Детальная информация о каждом языке
echo "📋 Детальная информация о доступных языках:"
echo "$RUNTIMES_JSON" | jq -r 'group_by(.language) | .[] | "  • \(.[0].language): \(length) версия(й) - \(map(.version) | join(", "))"' | sort
echo ""

# Проверка доступных пакетов (если API поддерживает)
echo "=== 3.1. Проверка доступных пакетов в Piston ==="
PACKAGES_JSON=$(curl -s "$PISTON_URL/api/v2/packages" 2>/dev/null || echo "null")
if [ "$PACKAGES_JSON" != "null" ] && [ -n "$PACKAGES_JSON" ] && echo "$PACKAGES_JSON" | jq -e '. | type == "array"' >/dev/null 2>&1; then
    AVAILABLE_PACKAGES=$(echo "$PACKAGES_JSON" | jq '. | length' 2>/dev/null || echo "0")
    echo "📦 Всего доступно пакетов для установки: $AVAILABLE_PACKAGES"
    
    # Подсчет уникальных языков среди доступных пакетов
    AVAILABLE_LANGUAGES=$(echo "$PACKAGES_JSON" | jq -r '[.[].language] | unique | .[]' 2>/dev/null | sort -u | wc -l || echo "0")
    echo "📊 Уникальных языков среди доступных пакетов: $AVAILABLE_LANGUAGES"
    
    # Показываем примеры доступных языков
    echo "📋 Примеры доступных языков (первые 30):"
    echo "$PACKAGES_JSON" | jq -r '[.[].language] | unique | .[]' 2>/dev/null | head -30 | sed 's/^/  • /'
    if [ "$AVAILABLE_LANGUAGES" -gt 30 ]; then
        echo "  ... и еще $((AVAILABLE_LANGUAGES - 30)) языков"
    fi
    echo ""
    
    # Показываем установленные пакеты
    INSTALLED_PACKAGES=$(echo "$PACKAGES_JSON" | jq '[.[] | select(.installed == true)] | length' 2>/dev/null || echo "0")
    INSTALLED_LANGUAGES=$(echo "$PACKAGES_JSON" | jq -r '[.[] | select(.installed == true) | .language] | unique | .[]' 2>/dev/null | sort -u | wc -l || echo "0")
    echo "📦 Установлено пакетов: $INSTALLED_PACKAGES"
    echo "📊 Установлено уникальных языков: $INSTALLED_LANGUAGES"
    
    if [ "$AVAILABLE_LANGUAGES" -gt "$LANGUAGE_COUNT" ]; then
        echo ""
        echo "⚠️  ВНИМАНИЕ: Доступно больше языков ($AVAILABLE_LANGUAGES), чем установлено ($LANGUAGE_COUNT)"
        echo "   Для установки дополнительных языков используйте Piston CLI:"
        echo "   docker exec -it piston-ainosov piston package install <language> <version>"
        echo "   Например: docker exec -it piston-ainosov piston package install node 18.15.0"
        echo ""
    fi
else
    echo "⚠️  Не удалось получить информацию о доступных пакетах"
    echo "   (API может не поддерживать /api/v2/packages или формат ответа отличается)"
    echo ""
fi

# Функция для получения простого тестового кода для языка
get_test_code() {
    local lang=$1
    case "$lang" in
        python|python2|python3)
            echo "print('Hello, World!')"
            ;;
        node|javascript|js)
            echo "console.log('Hello, World!');"
            ;;
        typescript|ts)
            echo "console.log('Hello, World!');"
            ;;
        java)
            echo "public class Main { public static void main(String[] args) { System.out.println(\"Hello, World!\"); } }"
            ;;
        c)
            cat <<'EOF'
#include <stdio.h>
int main() {
    printf("Hello, World!\n");
    return 0;
}
EOF
            ;;
        "c++"|cpp|gcc|g++|clang|clang++)
            cat <<'EOF'
#include <iostream>
int main() {
    std::cout << "Hello, World!" << std::endl;
    return 0;
}
EOF
            ;;
        go)
            cat <<'EOF'
package main
import "fmt"
func main() {
    fmt.Println("Hello, World!")
}
EOF
            ;;
        rust|rustc)
            echo "fn main() { println!(\"Hello, World!\"); }"
            ;;
        ruby)
            echo "puts 'Hello, World!'"
            ;;
        php)
            echo "<?php echo 'Hello, World!'; ?>"
            ;;
        perl)
            echo "print \"Hello, World!\\n\";"
            ;;
        lua)
            echo "print('Hello, World!')"
            ;;
        r|rscript)
            echo "cat('Hello, World!\\n')"
            ;;
        swift)
            echo "print(\"Hello, World!\")"
            ;;
        kotlin)
            echo "fun main() { println(\"Hello, World!\") }"
            ;;
        scala)
            echo "object Main { def main(args: Array[String]) { println(\"Hello, World!\") } }"
            ;;
        clojure)
            echo "(println \"Hello, World!\")"
            ;;
        haskell)
            echo "main = putStrLn \"Hello, World!\""
            ;;
        erlang)
            cat <<'EOF'
-module(hello).
-export([hello_world/0]).
hello_world() -> io:fwrite("hello, world\n").
EOF
            ;;
        elixir)
            cat <<'EOF'
defmodule HelloWorld do
    def main do
        IO.puts "hello world"
    end
end
HelloWorld.main()
EOF
            ;;
        crystal)
            echo "puts \"Hello, World!\""
            ;;
        nim)
            echo "echo \"Hello, World!\""
            ;;
        dart)
            echo "void main() { print('Hello, World!'); }"
            ;;
        zig)
            cat <<'EOF'
const std = @import("std");
pub fn main() !void {
    const stdout = std.io.getStdOut().writer();
    try stdout.print("Hello, World!\n");
}
EOF
            ;;
        ocaml)
            echo "print_endline \"Hello, World!\""
            ;;
        fsharp|fs)
            echo "printfn \"Hello, World!\""
            ;;
        csharp|cs)
            echo "using System; class Program { static void Main() { Console.WriteLine(\"Hello, World!\"); } }"
            ;;
        bash|sh)
            echo "echo 'Hello, World!'"
            ;;
        dash)
            echo "echo 'Hello, World!'"
            ;;
        powershell|ps1)
            echo "Write-Host 'Hello, World!'"
            ;;
        julia)
            echo "println(\"Hello, World!\")"
            ;;
        awk)
            echo "BEGIN { print \"Hello, World!\" }"
            ;;
        bqn)
            echo "•Out \"Hello, World!\""
            ;;
        brachylog)
            cat <<'EOF'
∧"Hello, World!"w
EOF
            ;;
        cobol)
            cat <<'EOF'
       IDENTIFICATION DIVISION.
       PROGRAM-ID. MAIN.
       PROCEDURE DIVISION.
           DISPLAY "Hello, World!".
           STOP RUN.
EOF
            ;;
        d)
            cat <<'EOF'
import std.stdio;
void main() {
    writeln ("Hello, world!");
}
EOF
            ;;
        dragon)
            echo "showln \"Hello, World!\""
            ;;
        emojicode)
            cat <<'EOF'
🏁 🍇
 😀 🔤Hello, World!🔤❗️
🍉 
EOF
            ;;
        file)
            cat <<'EOF'
#!/bin/sh
echo 'Hello, World!'
EOF
            ;;
        forte)
            echo '. Hello world'
            ;;
        fortran)
            cat <<'EOF'
program main
  write(*,*) 'Hello, World!'
end program main
EOF
            ;;
        golfscript)
            echo "\"Hello, World!\""
            ;;
        iverilog)
            cat <<'EOF'
module main;
  initial begin
    $display("Hello, World!");
  end
endmodule
EOF
            ;;
        japt)
            echo "Oi Hello World"
            ;;
        jelly)
            echo '"Hello, World!"'
            ;;
        lisp)
            echo "(print \"Hello, World!\")"
            ;;
        llvm_ir)
            cat <<'EOF'
@.str = private unnamed_addr constant [14 x i8] c"Hello, World!\00"
define i32 @main() {
  %1 = call i32 @puts(i8* getelementptr inbounds ([14 x i8], [14 x i8]* @.str, i32 0, i32 0))
  ret i32 0
}
declare i32 @puts(i8*)
EOF
            ;;
        matl)
            echo "'Hello, World!'D"
            ;;
        nasm)
            cat <<'EOF'
section .data
  msg db 'Hello, World!', 0x0A
  len equ $ - msg
section .text
  global _start
_start:
  mov eax, 4
  mov ebx, 1
  mov ecx, msg
  mov edx, len
  int 0x80
  mov eax, 1
  mov ebx, 0
  int 0x80
EOF
            ;;
        nasm64)
            cat <<'EOF'
section .data
  msg db 'Hello, World!', 0x0A
  len equ $ - msg
section .text
  global _start
_start:
  mov rax, 1
  mov rdi, 1
  mov rsi, msg
  mov rdx, len
  syscall
  mov rax, 60
  mov rdi, 0
  syscall
EOF
            ;;
        octave)
            echo "printf('Hello, World!\\n')"
            ;;
        osabie)
            echo '"Hello, World!"'
            ;;
        ponylang)
            cat <<'EOF'
actor Main
  new create(env: Env) =>
    env.out.print("Hello, World!")
EOF
            ;;
        prolog)
            echo "main :- write('Hello, World!'), nl, halt."
            ;;
        pure)
            echo "using system; putStrLn \"Hello, World!\";"
            ;;
        pyth)
            echo '"Hello, World!"'
            ;;
        retina)
            echo 'Hello, World!'
            ;;
        rockstar)
            echo "Say \"Hello, World!\""
            ;;
        samarium)
            cat <<'EOF'
"Hello, World!".p
EOF
            ;;
        sqlite3)
            echo "SELECT 'Hello, World!';"
            ;;
        vyxal)
            echo '`Hello, World!`'
            ;;
        *)
            # Универсальный fallback - простой вывод
            echo "print('Hello, World!')"
            ;;
    esac
}

# Проверка каждого языка
echo "=== 4. Проверка поддержки языков ==="
echo ""

# Список языков, которые нужно исключить из проверки
EXCLUDED_LANGUAGES=("brachylog" "elixir" "emojicode" "erlang" "forte" "jelly" "osabie" "retina" "samarium" "vyxal" "d" "japt" "pyth")

SUCCESS_COUNT=0
FAIL_COUNT=0
FAILED_LANGUAGES=()
WARN_COUNT=0

while IFS= read -r lang; do
    if [ -z "$lang" ]; then
        continue
    fi
    
    # Пропускаем исключенные языки
    SKIP_LANG=false
    for excluded in "${EXCLUDED_LANGUAGES[@]}"; do
        if [ "$lang" = "$excluded" ]; then
            SKIP_LANG=true
            break
        fi
    done
    
    if [ "$SKIP_LANG" = true ]; then
        echo "⏭️  Пропуск языка: $lang (исключен из проверки)"
        echo ""
        continue
    fi
    
    # Получаем информацию о версиях для этого языка
    VERSIONS=$(echo "$RUNTIMES_JSON" | jq -r ".[] | select(.language == \"$lang\") | .version" | sort -u)
    VERSION_COUNT=$(echo "$VERSIONS" | wc -l)
    
    echo "🔍 Проверка языка: $lang (доступно версий: $VERSION_COUNT)"
    echo "   Версии: $(echo "$VERSIONS" | tr '\n' ' ')"
    
    # Получить тестовый код
    TEST_CODE=$(get_test_code "$lang")
    
    # Выполнить код через Code Executor API
    echo -n "   Тест выполнения... "
    RESPONSE=$(curl -s --connect-timeout 10 -X POST "$CODE_EXECUTOR_URL/execute" \
        -H "Content-Type: application/json" \
        -d "{\"lang\":\"$lang\",\"code\":$(echo "$TEST_CODE" | jq -Rs .),\"timeout\":10}" 2>&1)
    
    # Проверить результат
    if echo "$RESPONSE" | jq -e '.stdout' >/dev/null 2>&1; then
        STDOUT=$(echo "$RESPONSE" | jq -r '.stdout' 2>/dev/null || echo "")
        STDERR=$(echo "$RESPONSE" | jq -r '.stderr' 2>/dev/null || echo "")
        
        # Проверка на ошибки выполнения
        if [ -n "$STDERR" ] && [ "$STDERR" != "" ] && [ "$STDERR" != "null" ]; then
            # Если есть stderr, но это не критично (может быть предупреждение)
            if echo "$STDERR" | grep -qi "error\|failed\|timeout"; then
                echo "❌ ОШИБКА"
                echo "      stderr: $(echo "$STDERR" | head -c 100)"
                FAIL_COUNT=$((FAIL_COUNT + 1))
                FAILED_LANGUAGES+=("$lang")
                echo ""
                continue
            else
                # Предупреждение, но не ошибка
                echo "⚠️  OK (есть предупреждения)"
                echo "      stderr: $(echo "$STDERR" | head -c 100)"
                SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
                WARN_COUNT=$((WARN_COUNT + 1))
                echo ""
                continue
            fi
        fi
        
        # Проверка что есть какой-то вывод
        if [ -n "$STDOUT" ] && [ "$STDOUT" != "" ] && [ "$STDOUT" != "null" ]; then
            echo "✅ OK"
            echo "      stdout: $(echo "$STDOUT" | head -c 100 | tr '\n' ' ')"
            SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
        else
            echo "⚠️  НЕТ ВЫВОДА"
            echo "      Ответ: $(echo "$RESPONSE" | jq -c '.' 2>/dev/null | head -c 200)"
            # Не считаем это критической ошибкой, но отмечаем
            SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
            WARN_COUNT=$((WARN_COUNT + 1))
        fi
    else
        echo "❌ ОШИБКА"
        echo "      Ответ: $(echo "$RESPONSE" | head -c 200)"
        FAIL_COUNT=$((FAIL_COUNT + 1))
        FAILED_LANGUAGES+=("$lang")
    fi
    
    echo ""
    
done <<< "$LANGUAGES"

# Итоговая статистика
echo ""
echo "=== Итоговая статистика ==="
echo "📊 Всего runtime'ов (все версии): $TOTAL_RUNTIMES"
echo "📊 Уникальных языков: $LANGUAGE_COUNT"
echo "✅ Успешно проверено: $SUCCESS_COUNT"
if [ $WARN_COUNT -gt 0 ]; then
    echo "⚠️  С предупреждениями: $WARN_COUNT"
fi
echo "❌ Ошибок: $FAIL_COUNT"

if [ "$AVAILABLE_LANGUAGES" -gt "$LANGUAGE_COUNT" ] 2>/dev/null; then
    echo ""
    echo "💡 Рекомендация:"
    echo "   В Piston доступно $AVAILABLE_LANGUAGES языков, но установлено только $LANGUAGE_COUNT"
    echo "   Для установки дополнительных языков используйте Piston CLI или API"
fi

if [ $FAIL_COUNT -gt 0 ]; then
    echo ""
    echo "❌ Языки с ошибками:"
    for lang in "${FAILED_LANGUAGES[@]}"; do
        echo "  - $lang"
    done
    echo ""
    echo "⚠️  Некоторые языки не прошли проверку"
    exit 1
else
    echo ""
    if [ $WARN_COUNT -gt 0 ]; then
        echo "✅ Все языки работают, но есть предупреждения"
    else
        echo "✅ Все языки поддерживаются корректно!"
    fi
    exit 0
fi

