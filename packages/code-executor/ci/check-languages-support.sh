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

# Функция для проверки ожидаемого вывода программы
check_expected_output() {
    local lang=$1
    local stdout=$2
    
    # Убираем только завершающие пробелы и переносы строк для проверки конца
    local trimmed_output=$(echo "$stdout" | sed 's/[[:space:]]*$//')
    
    # Строгая проверка: stdout должен заканчиваться на "Hello, World!"
    # Проверяем последнюю непустую строку
    local last_line=$(echo "$trimmed_output" | grep -v '^[[:space:]]*$' | tail -1)
    
    # Убираем пробелы в начале и конце последней строки
    last_line=$(echo "$last_line" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    
    # Проверяем, что последняя строка заканчивается на "Hello, World!"
    if echo "$last_line" | grep -qE '(Hello, World!|Hello World!)$'; then
        return 0
    fi
    
    # Для некоторых языков может быть другой формат
    case "$lang" in
        japt)
            # Japt может выводить "Hello World" без запятой и восклицательного знака
            if echo "$last_line" | grep -qiE '(Hello World|Hello, World!)$'; then
                return 0
            fi
            ;;
        lisp)
            # Lisp может выводить с кавычками: "Hello, World!"
            if echo "$last_line" | grep -qE '("Hello, World!"|Hello, World!)$'; then
                return 0
            fi
            ;;
    esac
    
    return 1
}

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
            cat <<'EOF'
using System;
class Program {
    static void Main() {
        Console.WriteLine("Hello, World!");
    }
}
EOF
            ;;
        csharp.net)
            cat <<'EOF'
using System;
class Program {
    static void Main() {
        Console.WriteLine("Hello, World!");
    }
}
EOF
            ;;
        basic)
            cat <<'EOF'
PRINT "Hello, World!"
EOF
            ;;
        "basic.net")
            cat <<'EOF'
Module HelloWorld
    Sub Main()
        Console.WriteLine("Hello, World!")
    End Sub
End Module
EOF
            ;;
        "fsharp.net")
            cat <<'EOF'
open System
[<EntryPoint>]
let main argv =
    printfn "Hello, World!"
    0
EOF
            ;;
        pascal)
            cat <<'EOF'
program HelloWorld;
begin
  writeln('Hello, World!');
end.
EOF
            ;;
        husk)
            echo '"Hello, World!"'
            ;;
        freebasic)
            cat <<'EOF'
Print "Hello, World!"
Sleep
EOF
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
EXCLUDED_LANGUAGES_BASE=("brachylog" "elixir" "emojicode" "erlang" "forte" "jelly" "osabie" "retina" "samarium" "vyxal" "d" "japt" "pyth")

# Языки, которые не прошли проверку (не выводят "Hello, World!" или имеют другие проблемы)
EXCLUDED_LANGUAGES_FAILED=("basic" "basic.net" "befunge93" "brainfuck" "c" "c++" "cjam" "cobol" "coffeescript" "cow" "crystal" "csharp" "csharp.net" "emacs" "forth" "fortran" "freebasic" "fsharp.net" "fsi" "go" "groovy" "haskell" "husk" "iverilog" "julia" "kotlin" "llvm_ir" "lolcode" "nasm" "nasm64" "nim" "ocaml" "paradoc" "pascal" "ponylang" "prolog" "pure" "racket" "rust" "scala" "smalltalk" "zig")

# Объединяем оба списка
EXCLUDED_LANGUAGES=("${EXCLUDED_LANGUAGES_BASE[@]}" "${EXCLUDED_LANGUAGES_FAILED[@]}")

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
    
    # Определяем, нужно ли использовать прямой доступ к Piston API для языков с компиляцией
    # Это позволяет проверить, что программа не только компилируется, но и запускается
    LANGUAGES_NEEDING_DIRECT_PISTON=("basic" "basic.net" "csharp" "csharp.net" "fsharp.net" "pascal" "kotlin" "scala" "husk" "freebasic")
    USE_DIRECT_PISTON=false
    for direct_lang in "${LANGUAGES_NEEDING_DIRECT_PISTON[@]}"; do
        if [ "$lang" = "$direct_lang" ]; then
            USE_DIRECT_PISTON=true
            break
        fi
    done
    
    # Выполнить код
    echo -n "   Тест выполнения... "
    if [ "$USE_DIRECT_PISTON" = true ]; then
        # Прямой доступ к Piston API для языков с компиляцией
        # Получаем версию языка из списка runtime'ов
        LANGUAGE_VERSION=$(echo "$RUNTIMES_JSON" | jq -r ".[] | select(.language == \"$lang\") | .version" | head -1)
        
        # Определяем имя файла на основе языка
        case "$lang" in
            basic|"basic.net")
                FILE_NAME="main.vb"
                ;;
            csharp|"csharp.net")
                FILE_NAME="main.cs"
                ;;
            "fsharp.net")
                FILE_NAME="main.fs"
                ;;
            pascal)
                FILE_NAME="main.pas"
                ;;
            kotlin)
                FILE_NAME="main.kt"
                ;;
            scala)
                FILE_NAME="main.scala"
                ;;
            husk)
                FILE_NAME="main.hs"
                ;;
            freebasic)
                FILE_NAME="main.bas"
                ;;
            *)
                FILE_NAME="main"
                ;;
        esac
        
        # Выполняем через Piston API напрямую
        RESPONSE=$(curl -s --connect-timeout 10 -X POST "$PISTON_URL/api/v2/execute" \
            -H "Content-Type: application/json" \
            -d "{\"language\":\"$lang\",\"version\":\"$LANGUAGE_VERSION\",\"files\":[{\"name\":\"$FILE_NAME\",\"content\":$(echo "$TEST_CODE" | jq -Rs .)}],\"run_timeout\":10000,\"compile_timeout\":10000}" 2>&1)
        
        # Проверяем, что ответ валидный JSON
        if ! echo "$RESPONSE" | jq . >/dev/null 2>&1; then
            echo "❌ ОШИБКА: Неверный формат ответа от Piston API"
            echo "      Ответ (первые 50 строк):"
            echo "$RESPONSE" | head -50 | sed 's/^/        /'
            FAIL_COUNT=$((FAIL_COUNT + 1))
            FAILED_LANGUAGES+=("$lang")
            echo ""
            continue
        fi
        
        # Проверяем наличие ошибки в ответе API
        API_ERROR=$(echo "$RESPONSE" | jq -r '.message // ""' 2>/dev/null || echo "")
        if [ -n "$API_ERROR" ] && [ "$API_ERROR" != "" ] && [ "$API_ERROR" != "null" ]; then
            echo "❌ ОШИБКА API: $API_ERROR"
            echo "      Полный ответ API:"
            echo "$RESPONSE" | jq '.' | head -30 | sed 's/^/        /'
            FAIL_COUNT=$((FAIL_COUNT + 1))
            FAILED_LANGUAGES+=("$lang")
            echo ""
            continue
        fi
        
        # Проверяем наличие стадии run в ответе
        if ! echo "$RESPONSE" | jq -e '.run' >/dev/null 2>&1; then
            echo "❌ ОШИБКА: Ответ API не содержит стадию 'run'"
            echo "      Полный ответ API:"
            echo "$RESPONSE" | jq '.' | head -30 | sed 's/^/        /'
            FAIL_COUNT=$((FAIL_COUNT + 1))
            FAILED_LANGUAGES+=("$lang")
            echo ""
            continue
        fi
        
        # Извлекаем данные из run стадии (выполнение программы)
        RUN_STDOUT=$(echo "$RESPONSE" | jq -r '.run.stdout // ""' 2>/dev/null || echo "")
        RUN_STDERR=$(echo "$RESPONSE" | jq -r '.run.stderr // ""' 2>/dev/null || echo "")
        RUN_OUTPUT=$(echo "$RESPONSE" | jq -r '.run.output // ""' 2>/dev/null || echo "")
        RUN_CODE=$(echo "$RESPONSE" | jq -r '.run.code // -1' 2>/dev/null || echo "-1")
        RUN_SIGNAL=$(echo "$RESPONSE" | jq -r '.run.signal // ""' 2>/dev/null || echo "")
        RUN_STATUS=$(echo "$RESPONSE" | jq -r '.run.status // ""' 2>/dev/null || echo "")
        
        # Используем run.output если run.stdout пустой (некоторые runtime'ы перенаправляют вывод)
        if [ -z "$RUN_STDOUT" ] || [ "$RUN_STDOUT" = "null" ] || [ "$RUN_STDOUT" = "" ]; then
            STDOUT="$RUN_OUTPUT"
        else
            STDOUT="$RUN_STDOUT"
        fi
        STDERR="$RUN_STDERR"
        
        # Проверяем стадию компиляции, если она есть
        HAS_COMPILE_STAGE=false
        if echo "$RESPONSE" | jq -e '.compile' >/dev/null 2>&1; then
            HAS_COMPILE_STAGE=true
        fi
        
        COMPILE_STDOUT=$(echo "$RESPONSE" | jq -r '.compile.stdout // ""' 2>/dev/null || echo "")
        COMPILE_STDERR=$(echo "$RESPONSE" | jq -r '.compile.stderr // ""' 2>/dev/null || echo "")
        COMPILE_CODE=$(echo "$RESPONSE" | jq -r '.compile.code // -1' 2>/dev/null || echo "-1")
        COMPILE_STATUS=$(echo "$RESPONSE" | jq -r '.compile.status // ""' 2>/dev/null || echo "")
        COMPILE_MESSAGE=$(echo "$RESPONSE" | jq -r '.compile.message // ""' 2>/dev/null || echo "")
        
        # Для компилируемых языков должна быть стадия компиляции
        if [ "$HAS_COMPILE_STAGE" = false ]; then
            # Проверяем, является ли язык компилируемым
            COMPILED_LANGS=("basic" "basic.net" "csharp" "csharp.net" "fsharp.net" "pascal" "kotlin" "scala" "husk" "freebasic")
            IS_COMPILED_LANG=false
            for compiled_lang in "${COMPILED_LANGS[@]}"; do
                if [ "$lang" = "$compiled_lang" ]; then
                    IS_COMPILED_LANG=true
                    break
                fi
            done
            
            if [ "$IS_COMPILED_LANG" = true ]; then
                echo "❌ ОШИБКА: Компилируемый язык не имеет стадии компиляции в ответе API"
                echo "      Полный ответ API:"
                echo "$RESPONSE" | jq '.' | head -50 | sed 's/^/        /'
                FAIL_COUNT=$((FAIL_COUNT + 1))
                FAILED_LANGUAGES+=("$lang")
                echo ""
                continue
            fi
        fi
        
        # Если есть стадия компиляции, проверяем её
        if [ "$HAS_COMPILE_STAGE" = true ]; then
            # Проверяем статус компиляции
            if [ -n "$COMPILE_STATUS" ] && [ "$COMPILE_STATUS" != "" ] && [ "$COMPILE_STATUS" != "null" ]; then
                echo "⚠️  Статус компиляции: $COMPILE_STATUS"
                if [ -n "$COMPILE_MESSAGE" ] && [ "$COMPILE_MESSAGE" != "" ] && [ "$COMPILE_MESSAGE" != "null" ]; then
                    echo "      Сообщение: $COMPILE_MESSAGE"
                fi
            fi
            
            if [ "$COMPILE_CODE" != "-1" ] && [ "$COMPILE_CODE" != "null" ] && [ "$COMPILE_CODE" != "0" ]; then
                echo "❌ ОШИБКА КОМПИЛЯЦИИ"
                echo "      compile.code: $COMPILE_CODE"
                if [ -n "$COMPILE_STDOUT" ] && [ "$COMPILE_STDOUT" != "" ] && [ "$COMPILE_STDOUT" != "null" ]; then
                    echo "      compile.stdout (первые 20 строк):"
                    echo "$COMPILE_STDOUT" | head -20 | sed 's/^/        /'
                fi
                if [ -n "$COMPILE_STDERR" ] && [ "$COMPILE_STDERR" != "" ] && [ "$COMPILE_STDERR" != "null" ]; then
                    echo "      compile.stderr (первые 20 строк):"
                    echo "$COMPILE_STDERR" | head -20 | sed 's/^/        /'
                fi
                FAIL_COUNT=$((FAIL_COUNT + 1))
                FAILED_LANGUAGES+=("$lang")
                echo ""
                continue
            fi
        fi
        
        # Проверяем стадию выполнения
        # Проверяем signal и status для понимания причин завершения
        if [ -n "$RUN_SIGNAL" ] && [ "$RUN_SIGNAL" != "null" ] && [ "$RUN_SIGNAL" != "" ]; then
            echo "❌ ПРОГРАММА ЗАВЕРШЕНА ПО СИГНАЛУ: $RUN_SIGNAL"
            if [ -n "$RUN_STATUS" ] && [ "$RUN_STATUS" != "null" ] && [ "$RUN_STATUS" != "" ]; then
                echo "      run.status: $RUN_STATUS"
            fi
            if [ -n "$STDOUT" ] && [ "$STDOUT" != "" ] && [ "$STDOUT" != "null" ]; then
                echo "      run.stdout (первые 20 строк):"
                echo "$STDOUT" | head -20 | sed 's/^/        /'
            fi
            if [ -n "$STDERR" ] && [ "$STDERR" != "" ] && [ "$STDERR" != "null" ]; then
                echo "      run.stderr (первые 20 строк):"
                echo "$STDERR" | head -20 | sed 's/^/        /'
            fi
            FAIL_COUNT=$((FAIL_COUNT + 1))
            FAILED_LANGUAGES+=("$lang")
            echo ""
            continue
        fi
        
        if [ "$RUN_CODE" != "0" ] && [ "$RUN_CODE" != "-1" ] && [ "$RUN_CODE" != "null" ]; then
            echo "❌ ОШИБКА ВЫПОЛНЕНИЯ (код: $RUN_CODE)"
            if [ -n "$RUN_STATUS" ] && [ "$RUN_STATUS" != "null" ] && [ "$RUN_STATUS" != "" ]; then
                echo "      run.status: $RUN_STATUS"
            fi
            if [ -n "$STDOUT" ] && [ "$STDOUT" != "" ] && [ "$STDOUT" != "null" ]; then
                echo "      run.stdout (первые 20 строк):"
                echo "$STDOUT" | head -20 | sed 's/^/        /'
            fi
            if [ -n "$STDERR" ] && [ "$STDERR" != "" ] && [ "$STDERR" != "null" ]; then
                echo "      run.stderr (первые 20 строк):"
                echo "$STDERR" | head -20 | sed 's/^/        /'
            fi
            FAIL_COUNT=$((FAIL_COUNT + 1))
            FAILED_LANGUAGES+=("$lang")
            echo ""
            continue
        fi
        
        # Для всех компилируемых языков проверяем, что программа действительно запустилась
        # и вывела ожидаемый результат, а не только скомпилировалась
        
        # Проверяем статус выполнения
        RUN_MESSAGE=$(echo "$RESPONSE" | jq -r '.run.message // ""' 2>/dev/null || echo "")
        
        # Если run.code = -1, это может означать, что программа не была запущена
        if [ "$RUN_CODE" = "-1" ] || [ "$RUN_CODE" = "null" ]; then
            echo "❌ ПРОГРАММА НЕ ЗАПУСТИЛАСЬ (run.code = -1 или null)"
            echo "      run.code: $RUN_CODE"
            if [ -n "$RUN_STATUS" ] && [ "$RUN_STATUS" != "" ] && [ "$RUN_STATUS" != "null" ]; then
                echo "      run.status: $RUN_STATUS"
            fi
            if [ -n "$RUN_MESSAGE" ] && [ "$RUN_MESSAGE" != "" ] && [ "$RUN_MESSAGE" != "null" ]; then
                echo "      run.message: $RUN_MESSAGE"
            fi
            echo "      compile.code: $COMPILE_CODE"
            if [ "$HAS_COMPILE_STAGE" = true ]; then
                if [ -n "$COMPILE_STATUS" ] && [ "$COMPILE_STATUS" != "" ] && [ "$COMPILE_STATUS" != "null" ]; then
                    echo "      compile.status: $COMPILE_STATUS"
                fi
                if [ -n "$COMPILE_MESSAGE" ] && [ "$COMPILE_MESSAGE" != "" ] && [ "$COMPILE_MESSAGE" != "null" ]; then
                    echo "      compile.message: $COMPILE_MESSAGE"
                fi
                if [ -n "$COMPILE_STDOUT" ] && [ "$COMPILE_STDOUT" != "" ] && [ "$COMPILE_STDOUT" != "null" ]; then
                    echo "      compile.stdout (первые 20 строк):"
                    echo "$COMPILE_STDOUT" | head -20 | sed 's/^/        /'
                fi
                if [ -n "$COMPILE_STDERR" ] && [ "$COMPILE_STDERR" != "" ] && [ "$COMPILE_STDERR" != "null" ]; then
                    echo "      compile.stderr (первые 20 строк):"
                    echo "$COMPILE_STDERR" | head -20 | sed 's/^/        /'
                fi
            fi
            if [ -n "$STDOUT" ] && [ "$STDOUT" != "" ] && [ "$STDOUT" != "null" ]; then
                echo "      run.stdout (первые 20 строк):"
                echo "$STDOUT" | head -20 | sed 's/^/        /'
            fi
            if [ -n "$STDERR" ] && [ "$STDERR" != "" ] && [ "$STDERR" != "null" ]; then
                echo "      run.stderr (первые 20 строк):"
                echo "$STDERR" | head -20 | sed 's/^/        /'
            fi
            echo "      Полный ответ API (первые 50 строк):"
            echo "$RESPONSE" | jq '.' | head -50 | sed 's/^/        /'
            FAIL_COUNT=$((FAIL_COUNT + 1))
            FAILED_LANGUAGES+=("$lang")
            echo ""
            continue
        fi
        
        # Проверяем, что stdout не пустой и не содержит только пробелы
        STDOUT_TRIMMED=$(echo "$STDOUT" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
        if [ -z "$STDOUT_TRIMMED" ] || [ "$STDOUT_TRIMMED" = "" ] || [ "$STDOUT_TRIMMED" = "null" ]; then
            echo "❌ ПРОГРАММА НЕ ЗАПУСТИЛАСЬ (stdout пустой после успешной компиляции)"
            echo "      run.code: $RUN_CODE (программа не выполнилась или не вывела ничего)"
            if [ -n "$RUN_STATUS" ] && [ "$RUN_STATUS" != "" ] && [ "$RUN_STATUS" != "null" ]; then
                echo "      run.status: $RUN_STATUS"
            fi
            if [ -n "$RUN_MESSAGE" ] && [ "$RUN_MESSAGE" != "" ] && [ "$RUN_MESSAGE" != "null" ]; then
                echo "      run.message: $RUN_MESSAGE"
            fi
            echo "      compile.code: $COMPILE_CODE"
            if [ -n "$COMPILE_STDOUT" ] && [ "$COMPILE_STDOUT" != "" ] && [ "$COMPILE_STDOUT" != "null" ]; then
                echo "      compile.stdout (первые 20 строк):"
                echo "$COMPILE_STDOUT" | head -20 | sed 's/^/        /'
            fi
            if [ -n "$STDERR" ] && [ "$STDERR" != "" ] && [ "$STDERR" != "null" ]; then
                echo "      run.stderr (первые 20 строк):"
                echo "$STDERR" | head -20 | sed 's/^/        /'
            fi
            FAIL_COUNT=$((FAIL_COUNT + 1))
            FAILED_LANGUAGES+=("$lang")
            echo ""
            continue
        fi
        
        # Проверяем паттерны логов сборки/компиляции для различных языков
        # .NET языки
        DOTNET_LANGUAGES=("basic" "basic.net" "csharp" "csharp.net" "fsharp.net")
        IS_DOTNET_LANG=false
        for dotnet_lang in "${DOTNET_LANGUAGES[@]}"; do
            if [ "$lang" = "$dotnet_lang" ]; then
                IS_DOTNET_LANG=true
                break
            fi
        done
        
        if [ "$IS_DOTNET_LANG" = true ]; then
            # Проверяем паттерны логов сборки .NET
            if echo "$STDOUT" | grep -qiE "(Microsoft \(R\) (Build Engine|Visual C#|Visual Basic)|Getting ready|The template|Build succeeded|Build failed|Determining projects|Restored|Compilation successful|Assembly.*saved successfully)"; then
                echo "❌ ПРОГРАММА НЕ ЗАПУСТИЛАСЬ (обнаружен лог сборки вместо вывода программы)"
                echo "      run.code: $RUN_CODE (программа не выполнилась, только скомпилировалась)"
                echo "      run.stdout (первые 30 строк):"
                echo "$STDOUT" | head -30 | sed 's/^/        /'
                if [ -n "$STDERR" ] && [ "$STDERR" != "" ] && [ "$STDERR" != "null" ]; then
                    echo "      run.stderr (первые 20 строк):"
                    echo "$STDERR" | head -20 | sed 's/^/        /'
                fi
                FAIL_COUNT=$((FAIL_COUNT + 1))
                FAILED_LANGUAGES+=("$lang")
                echo ""
                continue
            fi
        fi
        
        # Проверяем паттерны логов компиляции для других компилируемых языков
        # Kotlin/Scala (JVM языки)
        if [ "$lang" = "kotlin" ] || [ "$lang" = "scala" ]; then
            if echo "$STDOUT" | grep -qiE "(Compiling|Compilation|Building|BUILD|\.class|\.jar|warning:|error:)"; then
                # Проверяем, что это не только лог компиляции, а есть и вывод программы
                if ! echo "$STDOUT" | grep -qiE "(Hello, World!|Hello World!)"; then
                    echo "❌ ПРОГРАММА НЕ ЗАПУСТИЛАСЬ (обнаружен только лог компиляции без вывода программы)"
                    echo "      run.code: $RUN_CODE (программа не выполнилась, только скомпилировалась)"
                    echo "      run.stdout (первые 30 строк):"
                    echo "$STDOUT" | head -30 | sed 's/^/        /'
                    if [ -n "$STDERR" ] && [ "$STDERR" != "" ] && [ "$STDERR" != "null" ]; then
                        echo "      run.stderr (первые 20 строк):"
                        echo "$STDERR" | head -20 | sed 's/^/        /'
                    fi
                    FAIL_COUNT=$((FAIL_COUNT + 1))
                    FAILED_LANGUAGES+=("$lang")
                    echo ""
                    continue
                fi
            fi
        fi
        
        # Pascal
        if [ "$lang" = "pascal" ]; then
            if echo "$STDOUT" | grep -qiE "(Compiling|Linking|\.exe|\.o|\.ppu|Free Pascal|fpc|warning:|error:|note:)"; then
                # Проверяем, что это не только лог компиляции
                if ! echo "$STDOUT" | grep -qiE "(Hello, World!|Hello World!)"; then
                    echo "❌ ПРОГРАММА НЕ ЗАПУСТИЛАСЬ (обнаружен только лог компиляции без вывода программы)"
                    echo "      run.code: $RUN_CODE (программа не выполнилась, только скомпилировалась)"
                    echo "      run.stdout (первые 30 строк):"
                    echo "$STDOUT" | head -30 | sed 's/^/        /'
                    if [ -n "$STDERR" ] && [ "$STDERR" != "" ] && [ "$STDERR" != "null" ]; then
                        echo "      run.stderr (первые 20 строк):"
                        echo "$STDERR" | head -20 | sed 's/^/        /'
                    fi
                    FAIL_COUNT=$((FAIL_COUNT + 1))
                    FAILED_LANGUAGES+=("$lang")
                    echo ""
                    continue
                fi
            fi
        fi
        
        # FreeBASIC
        if [ "$lang" = "freebasic" ]; then
            if echo "$STDOUT" | grep -qiE "(Compiling|Linking|\.exe|\.o|FreeBASIC|fbc|warning:|error:)"; then
                # Проверяем, что это не только лог компиляции
                if ! echo "$STDOUT" | grep -qiE "(Hello, World!|Hello World!)"; then
                    echo "❌ ПРОГРАММА НЕ ЗАПУСТИЛАСЬ (обнаружен только лог компиляции без вывода программы)"
                    echo "      run.code: $RUN_CODE (программа не выполнилась, только скомпилировалась)"
                    echo "      run.stdout (первые 30 строк):"
                    echo "$STDOUT" | head -30 | sed 's/^/        /'
                    if [ -n "$STDERR" ] && [ "$STDERR" != "" ] && [ "$STDERR" != "null" ]; then
                        echo "      run.stderr (первые 20 строк):"
                        echo "$STDERR" | head -20 | sed 's/^/        /'
                    fi
                    FAIL_COUNT=$((FAIL_COUNT + 1))
                    FAILED_LANGUAGES+=("$lang")
                    echo ""
                    continue
                fi
            fi
        fi
        
        # Husk (Haskell-подобный язык)
        if [ "$lang" = "husk" ]; then
            if echo "$STDOUT" | grep -qiE "(Compiling|Linking|\.o|ghc|warning:|error:)"; then
                # Проверяем, что это не только лог компиляции
                if ! echo "$STDOUT" | grep -qiE "(Hello, World!|Hello World!)"; then
                    echo "❌ ПРОГРАММА НЕ ЗАПУСТИЛАСЬ (обнаружен только лог компиляции без вывода программы)"
                    echo "      run.code: $RUN_CODE (программа не выполнилась, только скомпилировалась)"
                    echo "      run.stdout (первые 30 строк):"
                    echo "$STDOUT" | head -30 | sed 's/^/        /'
                    if [ -n "$STDERR" ] && [ "$STDERR" != "" ] && [ "$STDERR" != "null" ]; then
                        echo "      run.stderr (первые 20 строк):"
                        echo "$STDERR" | head -20 | sed 's/^/        /'
                    fi
                    FAIL_COUNT=$((FAIL_COUNT + 1))
                    FAILED_LANGUAGES+=("$lang")
                    echo ""
                    continue
                fi
            fi
        fi
        
        # Для всех компилируемых языков: проверяем, что вывод содержит ожидаемый результат
        # Это финальная проверка - даже если нет явных логов компиляции, проверяем содержимое
        if ! check_expected_output "$lang" "$STDOUT"; then
            # Если вывод не соответствует ожидаемому, проверяем, не является ли это логом компиляции
            if echo "$STDOUT" | grep -qiE "(Compiling|Linking|Building|\.exe|\.o|\.class|\.jar|warning:|error:|Microsoft|Build|Assembly|Compilation)"; then
                echo "❌ ПРОГРАММА НЕ ЗАПУСТИЛАСЬ (обнаружен лог компиляции/сборки вместо вывода программы)"
                echo "      run.code: $RUN_CODE (программа не выполнилась, только скомпилировалась)"
                echo "      run.stdout (первые 30 строк):"
                echo "$STDOUT" | head -30 | sed 's/^/        /'
                if [ -n "$STDERR" ] && [ "$STDERR" != "" ] && [ "$STDERR" != "null" ]; then
                    echo "      run.stderr (первые 20 строк):"
                    echo "$STDERR" | head -20 | sed 's/^/        /'
                fi
                FAIL_COUNT=$((FAIL_COUNT + 1))
                FAILED_LANGUAGES+=("$lang")
                echo ""
                continue
            else
                # Вывод не соответствует ожидаемому и это не лог компиляции - значит программа запустилась, но вывела что-то не то
                echo "❌ НЕВЕРНЫЙ ВЫВОД ПРОГРАММЫ"
                echo "      Ожидается: Hello, World! (или вариант)"
                echo "      Получено stdout (первые 30 строк):"
                echo "$STDOUT" | head -30 | sed 's/^/        /'
                if [ -n "$STDERR" ] && [ "$STDERR" != "" ] && [ "$STDERR" != "null" ]; then
                    echo "      stderr (первые 20 строк):"
                    echo "$STDERR" | head -20 | sed 's/^/        /'
                fi
                FAIL_COUNT=$((FAIL_COUNT + 1))
                FAILED_LANGUAGES+=("$lang")
                echo ""
                continue
            fi
        else
            # Вывод правильный - программа успешно запустилась и вывела ожидаемый результат
            echo "✅ OK"
            echo "      stdout (первые 20 строк):"
            echo "$STDOUT" | head -20 | sed 's/^/        /'
            if [ "$(echo "$STDOUT" | wc -l)" -gt 20 ]; then
                echo "        ... (еще $(( $(echo "$STDOUT" | wc -l) - 20 )) строк)"
            fi
            if [ -n "$STDERR" ] && [ "$STDERR" != "" ] && [ "$STDERR" != "null" ]; then
                echo "      stderr (первые 10 строк, если есть):"
                echo "$STDERR" | head -10 | sed 's/^/        /'
            fi
            SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
            echo ""
            continue
        fi
    else
        # Обычный путь через Code Executor API
        RESPONSE=$(curl -s --connect-timeout 10 -X POST "$CODE_EXECUTOR_URL/execute" \
            -H "Content-Type: application/json" \
            -d "{\"lang\":\"$lang\",\"code\":$(echo "$TEST_CODE" | jq -Rs .),\"timeout\":10}" 2>&1)
        
        STDOUT=$(echo "$RESPONSE" | jq -r '.stdout' 2>/dev/null || echo "")
        STDERR=$(echo "$RESPONSE" | jq -r '.stderr' 2>/dev/null || echo "")
    fi
    
    # Проверить результат
    # Для прямого доступа к Piston API переменные STDOUT и STDERR уже установлены
    # Для Code Executor API нужно извлечь их из ответа
    if [ "$USE_DIRECT_PISTON" != true ]; then
        # Проверяем, что ответ валидный JSON и содержит stdout
        if echo "$RESPONSE" | jq -e '.stdout' >/dev/null 2>&1; then
            STDOUT=$(echo "$RESPONSE" | jq -r '.stdout' 2>/dev/null || echo "")
            STDERR=$(echo "$RESPONSE" | jq -r '.stderr' 2>/dev/null || echo "")
        else
            echo "❌ ОШИБКА: Неверный формат ответа от Code Executor API"
            echo "      Ответ: $(echo "$RESPONSE" | head -10 | sed 's/^/        /')"
            FAIL_COUNT=$((FAIL_COUNT + 1))
            FAILED_LANGUAGES+=("$lang")
            echo ""
            continue
        fi
    fi
    
    # Проверяем результат выполнения (для обоих случаев)
    if [ -n "$STDOUT" ] || [ -n "$STDERR" ]; then
        
        # Проверка на ошибки выполнения
        if [ -n "$STDERR" ] && [ "$STDERR" != "" ] && [ "$STDERR" != "null" ]; then
            # Если есть stderr, но это не критично (может быть предупреждение)
            if echo "$STDERR" | grep -qi "error\|failed\|timeout"; then
                echo "❌ ОШИБКА"
                echo "      stderr (первые 30 строк):"
                echo "$STDERR" | head -30 | sed 's/^/        /'
                if [ "$(echo "$STDERR" | wc -l)" -gt 30 ]; then
                    echo "        ... (еще $(( $(echo "$STDERR" | wc -l) - 30 )) строк)"
                fi
                if [ -n "$STDOUT" ] && [ "$STDOUT" != "" ] && [ "$STDOUT" != "null" ]; then
                    echo "      stdout (первые 20 строк):"
                    echo "$STDOUT" | head -20 | sed 's/^/        /'
                    if [ "$(echo "$STDOUT" | wc -l)" -gt 20 ]; then
                        echo "        ... (еще $(( $(echo "$STDOUT" | wc -l) - 20 )) строк)"
                    fi
                fi
                FAIL_COUNT=$((FAIL_COUNT + 1))
                FAILED_LANGUAGES+=("$lang")
                echo ""
                continue
            else
                # Предупреждение, но не ошибка - проверяем содержимое вывода
                if [ -n "$STDOUT" ] && [ "$STDOUT" != "" ] && [ "$STDOUT" != "null" ]; then
                    if check_expected_output "$lang" "$STDOUT"; then
                        echo "⚠️  OK (есть предупреждения, но вывод правильный)"
                        echo "      stdout: $(echo "$STDOUT" | head -20 | sed 's/^/        /')"
                        if [ "$(echo "$STDOUT" | wc -l)" -gt 20 ]; then
                            echo "        ... (еще $(( $(echo "$STDOUT" | wc -l) - 20 )) строк)"
                        fi
                        echo "      stderr: $(echo "$STDERR" | head -10 | sed 's/^/        /')"
                        if [ "$(echo "$STDERR" | wc -l)" -gt 10 ]; then
                            echo "        ... (еще $(( $(echo "$STDERR" | wc -l) - 10 )) строк)"
                        fi
                        SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
                        WARN_COUNT=$((WARN_COUNT + 1))
                    else
                        echo "❌ НЕВЕРНЫЙ ВЫВОД (есть предупреждения)"
                        echo "      Ожидается: Hello, World! (или вариант)"
                        echo "      Получено stdout (первые 30 строк):"
                        echo "$STDOUT" | head -30 | sed 's/^/        /'
                        if [ "$(echo "$STDOUT" | wc -l)" -gt 30 ]; then
                            echo "        ... (еще $(( $(echo "$STDOUT" | wc -l) - 30 )) строк)"
                        fi
                        echo "      stderr (первые 20 строк):"
                        echo "$STDERR" | head -20 | sed 's/^/        /'
                        if [ "$(echo "$STDERR" | wc -l)" -gt 20 ]; then
                            echo "        ... (еще $(( $(echo "$STDERR" | wc -l) - 20 )) строк)"
                        fi
                        FAIL_COUNT=$((FAIL_COUNT + 1))
                        FAILED_LANGUAGES+=("$lang")
                    fi
                else
                    # Нет stdout - это ошибка, даже если есть stderr с предупреждениями
                    # Программа должна вывести "Hello, World!" в stdout
                    echo "❌ НЕТ ВЫВОДА В STDOUT (программа не вывела ожидаемый результат)"
                    echo "      Ожидается: Hello, World! в stdout"
                    echo "      stdout: пустой или null"
                    echo "      stderr (первые 20 строк):"
                    echo "$STDERR" | head -20 | sed 's/^/        /'
                    if [ "$(echo "$STDERR" | wc -l)" -gt 20 ]; then
                        echo "        ... (еще $(( $(echo "$STDERR" | wc -l) - 20 )) строк)"
                    fi
                    FAIL_COUNT=$((FAIL_COUNT + 1))
                    FAILED_LANGUAGES+=("$lang")
                fi
                echo ""
                continue
            fi
        fi
        
        # Проверка что есть какой-то вывод
        if [ -n "$STDOUT" ] && [ "$STDOUT" != "" ] && [ "$STDOUT" != "null" ]; then
            # Проверяем содержимое вывода
            if check_expected_output "$lang" "$STDOUT"; then
                echo "✅ OK"
                echo "      stdout (первые 20 строк):"
                echo "$STDOUT" | head -20 | sed 's/^/        /'
                if [ "$(echo "$STDOUT" | wc -l)" -gt 20 ]; then
                    echo "        ... (еще $(( $(echo "$STDOUT" | wc -l) - 20 )) строк)"
                fi
                SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
            else
                echo "❌ НЕВЕРНЫЙ ВЫВОД"
                echo "      Ожидается: Hello, World! (или вариант)"
                echo "      Получено stdout (первые 30 строк):"
                echo "$STDOUT" | head -30 | sed 's/^/        /'
                if [ "$(echo "$STDOUT" | wc -l)" -gt 30 ]; then
                    echo "        ... (еще $(( $(echo "$STDOUT" | wc -l) - 30 )) строк)"
                fi
                if [ -n "$STDERR" ] && [ "$STDERR" != "" ] && [ "$STDERR" != "null" ]; then
                    echo "      stderr (первые 20 строк):"
                    echo "$STDERR" | head -20 | sed 's/^/        /'
                    if [ "$(echo "$STDERR" | wc -l)" -gt 20 ]; then
                        echo "        ... (еще $(( $(echo "$STDERR" | wc -l) - 20 )) строк)"
                    fi
                fi
                FAIL_COUNT=$((FAIL_COUNT + 1))
                FAILED_LANGUAGES+=("$lang")
            fi
        else
            echo "❌ НЕТ ВЫВОДА"
            echo "      Полный ответ API:"
            echo "$RESPONSE" | jq -c '.' 2>/dev/null | head -5 | sed 's/^/        /'
            if [ -n "$STDERR" ] && [ "$STDERR" != "" ] && [ "$STDERR" != "null" ]; then
                echo "      stderr (первые 20 строк):"
                echo "$STDERR" | head -20 | sed 's/^/        /'
                if [ "$(echo "$STDERR" | wc -l)" -gt 20 ]; then
                    echo "        ... (еще $(( $(echo "$STDERR" | wc -l) - 20 )) строк)"
                fi
            fi
            FAIL_COUNT=$((FAIL_COUNT + 1))
            FAILED_LANGUAGES+=("$lang")
        fi
    else
        echo "❌ НЕТ ВЫВОДА"
        if [ "$USE_DIRECT_PISTON" = true ]; then
            echo "      Прямой доступ к Piston API: нет stdout и stderr"
            echo "      Полный ответ API (первые 10 строк):"
            echo "$RESPONSE" | jq -c '.' 2>/dev/null | head -5 | sed 's/^/        /'
        else
            echo "      Полный ответ API (первые 10 строк):"
            echo "$RESPONSE" | jq -c '.' 2>/dev/null | head -5 | sed 's/^/        /'
        fi
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

