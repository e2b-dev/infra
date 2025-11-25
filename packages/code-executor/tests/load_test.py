#!/usr/bin/env python3
"""
Скрипт для нагрузочного тестирования code-executor сервиса.
Отправляет параллельные запросы с вычислительно сложными задачами.
"""

import asyncio
import aiohttp
import json
import time
import argparse
from collections import defaultdict
from typing import Dict, List, Tuple
import statistics


# Топ-5 самых популярных языков программирования
LANGUAGES = {
    "python": {
        "simple": "print('Hello, World!')",
        "complex": """
# Вычисление факториала больших чисел и поиск простых чисел
import math

def is_prime(n):
    if n < 2:
        return False
    if n == 2:
        return True
    if n % 2 == 0:
        return False
    for i in range(3, int(math.sqrt(n)) + 1, 2):
        if n % i == 0:
            return False
    return True

# Вычисляем факториал 1000
result = 1
for i in range(1, 1001):
    result *= i

# Ищем простые числа до 10000
primes = []
for num in range(2, 10000):
    if is_prime(num):
        primes.append(num)

print(f"Factorial of 1000 calculated, found {len(primes)} primes")
print(f"Last 5 primes: {primes[-5:]}")
"""
    },
    "node": {
        "simple": "console.log('Hello, World!');",
        "complex": """
// Вычисление чисел Фибоначчи и матричные операции
function fibonacci(n) {
    if (n <= 1) return n;
    let a = 0, b = 1;
    for (let i = 2; i <= n; i++) {
        [a, b] = [b, a + b];
    }
    return b;
}

// Вычисляем много чисел Фибоначчи
const fibResults = [];
for (let i = 0; i < 1000; i++) {
    fibResults.push(fibonacci(i % 50));
}

// Матричное умножение больших матриц
function matrixMultiply(a, b) {
    const n = a.length;
    const result = Array(n).fill(0).map(() => Array(n).fill(0));
    for (let i = 0; i < n; i++) {
        for (let j = 0; j < n; j++) {
            for (let k = 0; k < n; k++) {
                result[i][j] += a[i][k] * b[k][j];
            }
        }
    }
    return result;
}

const size = 50;
const matrixA = Array(size).fill(0).map(() => 
    Array(size).fill(0).map(() => Math.random())
);
const matrixB = Array(size).fill(0).map(() => 
    Array(size).fill(0).map(() => Math.random())
);

const result = matrixMultiply(matrixA, matrixB);
console.log(`Computed ${fibResults.length} Fibonacci numbers`);
console.log(`Matrix multiplication completed: ${size}x${size}`);
"""
    },
    "java": {
        "simple": """
public class Main {
    public static void main(String[] args) {
        System.out.println("Hello, World!");
    }
}
""",
        "complex": """
import java.math.BigInteger;
import java.util.ArrayList;
import java.util.List;

public class Main {
    public static void main(String[] args) {
        // Вычисление факториала больших чисел
        BigInteger factorial = BigInteger.ONE;
        for (int i = 1; i <= 1000; i++) {
            factorial = factorial.multiply(BigInteger.valueOf(i));
        }
        
        // Поиск простых чисел
        List<Integer> primes = new ArrayList<>();
        for (int num = 2; num < 10000; num++) {
            boolean isPrime = true;
            for (int i = 2; i * i <= num; i++) {
                if (num % i == 0) {
                    isPrime = false;
                    break;
                }
            }
            if (isPrime) {
                primes.add(num);
            }
        }
        
        // Вычисление чисел Фибоначчи
        BigInteger fib1 = BigInteger.ZERO;
        BigInteger fib2 = BigInteger.ONE;
        for (int i = 0; i < 1000; i++) {
            BigInteger temp = fib1.add(fib2);
            fib1 = fib2;
            fib2 = temp;
        }
        
        System.out.println("Factorial of 1000 calculated");
        System.out.println("Found " + primes.size() + " primes");
        System.out.println("Computed 1000 Fibonacci numbers");
    }
}
"""
    },
    "cpp": {
        "simple": """
#include <iostream>
int main() {
    std::cout << "Hello, World!" << std::endl;
    return 0;
}
""",
        "complex": """
#include <iostream>
#include <vector>
#include <cmath>
#include <algorithm>

bool isPrime(int n) {
    if (n < 2) return false;
    if (n == 2) return true;
    if (n % 2 == 0) return false;
    for (int i = 3; i * i <= n; i += 2) {
        if (n % i == 0) return false;
    }
    return true;
}

int main() {
    // Вычисление факториала
    long long factorial = 1;
    for (int i = 1; i <= 1000; i++) {
        factorial *= i;
        if (factorial < 0) break; // Переполнение
    }
    
    // Поиск простых чисел
    std::vector<int> primes;
    for (int num = 2; num < 10000; num++) {
        if (isPrime(num)) {
            primes.push_back(num);
        }
    }
    
    // Вычисление чисел Фибоначчи
    long long fib1 = 0, fib2 = 1;
    for (int i = 0; i < 1000; i++) {
        long long temp = fib1 + fib2;
        fib1 = fib2;
        fib2 = temp;
    }
    
    // Матричное умножение
    const int size = 50;
    std::vector<std::vector<double>> matrixA(size, std::vector<double>(size, 1.0));
    std::vector<std::vector<double>> matrixB(size, std::vector<double>(size, 1.0));
    std::vector<std::vector<double>> result(size, std::vector<double>(size, 0.0));
    
    for (int i = 0; i < size; i++) {
        for (int j = 0; j < size; j++) {
            for (int k = 0; k < size; k++) {
                result[i][j] += matrixA[i][k] * matrixB[k][j];
            }
        }
    }
    
    std::cout << "Factorial calculated" << std::endl;
    std::cout << "Found " << primes.size() << " primes" << std::endl;
    std::cout << "Matrix multiplication completed: " << size << "x" << size << std::endl;
    return 0;
}
"""
    },
    "go": {
        "simple": """
package main
import "fmt"
func main() {
    fmt.Println("Hello, World!")
}
""",
        "complex": """
package main
import (
    "fmt"
    "math/big"
)

func isPrime(n int) bool {
    if n < 2 {
        return false
    }
    if n == 2 {
        return true
    }
    if n%2 == 0 {
        return false
    }
    for i := 3; i*i <= n; i += 2 {
        if n%i == 0 {
            return false
        }
    }
    return true
}

func main() {
    // Вычисление факториала больших чисел
    factorial := big.NewInt(1)
    for i := 1; i <= 1000; i++ {
        factorial.Mul(factorial, big.NewInt(int64(i)))
    }
    
    // Поиск простых чисел
    primes := []int{}
    for num := 2; num < 10000; num++ {
        if isPrime(num) {
            primes = append(primes, num)
        }
    }
    
    // Вычисление чисел Фибоначчи
    fib1 := big.NewInt(0)
    fib2 := big.NewInt(1)
    for i := 0; i < 1000; i++ {
        temp := new(big.Int).Add(fib1, fib2)
        fib1.Set(fib2)
        fib2.Set(temp)
    }
    
    fmt.Printf("Factorial of 1000 calculated\\n")
    fmt.Printf("Found %d primes\\n", len(primes))
    fmt.Printf("Computed 1000 Fibonacci numbers\\n")
}
"""
    }
}


class LoadTestStats:
    """Класс для сбора статистики нагрузочного тестирования"""
    
    def __init__(self):
        self.total_requests = 0
        self.successful_requests = 0
        self.failed_requests = 0
        self.response_times: List[float] = []  # Все времена ответа
        self.success_response_times: List[float] = []  # Время успешных запросов
        self.failed_response_times: List[float] = []  # Время неудачных запросов
        self.errors_by_language: Dict[str, int] = defaultdict(int)
        self.success_by_language: Dict[str, int] = defaultdict(int)
        self.response_times_by_language: Dict[str, List[float]] = defaultdict(list)
        self.success_times_by_language: Dict[str, List[float]] = defaultdict(list)
        self.error_messages: List[str] = []  # Примеры ошибок
        self.timeout_count = 0
        self.start_time = None
        self.end_time = None
    
    def add_result(self, language: str, success: bool, response_time: float, error: str = ""):
        """Добавить результат запроса"""
        self.total_requests += 1
        self.response_times.append(response_time)
        self.response_times_by_language[language].append(response_time)
        
        if success:
            self.successful_requests += 1
            self.success_by_language[language] += 1
            self.success_response_times.append(response_time)
            self.success_times_by_language[language].append(response_time)
        else:
            self.failed_requests += 1
            self.errors_by_language[language] += 1
            self.failed_response_times.append(response_time)
            if "timeout" in error.lower() or "timeout" in error:
                self.timeout_count += 1
            # Сохраняем примеры ошибок (максимум 10)
            if len(self.error_messages) < 10:
                self.error_messages.append(f"{language}: {error[:200]}")
    
    def _percentile(self, data: List[float], percentile: float) -> float:
        """Вычислить процентиль"""
        if not data:
            return 0.0
        sorted_data = sorted(data)
        index = int(len(sorted_data) * percentile / 100)
        if index >= len(sorted_data):
            index = len(sorted_data) - 1
        return sorted_data[index]
    
    def print_summary(self):
        """Вывести итоговую статистику"""
        duration = (self.end_time - self.start_time) if self.start_time and self.end_time else 0
        
        print("\n" + "="*80)
        print("ИТОГОВАЯ СТАТИСТИКА НАГРУЗОЧНОГО ТЕСТИРОВАНИЯ")
        print("="*80)
        print(f"Общее время выполнения: {duration:.2f} секунд")
        print(f"Всего запросов: {self.total_requests}")
        print(f"Успешных запросов: {self.successful_requests} ({self.successful_requests/self.total_requests*100:.1f}%)")
        print(f"Неудачных запросов: {self.failed_requests} ({self.failed_requests/self.total_requests*100:.1f}%)")
        print(f"Таймаутов: {self.timeout_count}")
        
        if duration > 0:
            print(f"\nRPS (запросов в секунду): {self.total_requests/duration:.2f}")
            print(f"Среднее время на запрос: {duration/self.total_requests:.3f} секунд")
        
        # Статистика по времени для всех запросов
        if self.response_times:
            print(f"\nВремя отклика - ВСЕ ЗАПРОСЫ (секунды):")
            print(f"  Минимальное: {min(self.response_times):.3f}")
            print(f"  Максимальное: {max(self.response_times):.3f}")
            print(f"  Среднее: {statistics.mean(self.response_times):.3f}")
            print(f"  Медиана (p50): {statistics.median(self.response_times):.3f}")
            if len(self.response_times) > 1:
                print(f"  Стандартное отклонение: {statistics.stdev(self.response_times):.3f}")
                print(f"  p95: {self._percentile(self.response_times, 95):.3f}")
                print(f"  p99: {self._percentile(self.response_times, 99):.3f}")
        
        # Статистика по времени для успешных запросов
        if self.success_response_times:
            print(f"\nВремя отклика - УСПЕШНЫЕ ЗАПРОСЫ (секунды):")
            print(f"  Минимальное: {min(self.success_response_times):.3f}")
            print(f"  Максимальное: {max(self.success_response_times):.3f}")
            print(f"  Среднее: {statistics.mean(self.success_response_times):.3f}")
            print(f"  Медиана (p50): {statistics.median(self.success_response_times):.3f}")
            if len(self.success_response_times) > 1:
                print(f"  Стандартное отклонение: {statistics.stdev(self.success_response_times):.3f}")
                print(f"  p95: {self._percentile(self.success_response_times, 95):.3f}")
                print(f"  p99: {self._percentile(self.success_response_times, 99):.3f}")
        
        # Статистика по времени для неудачных запросов
        if self.failed_response_times:
            print(f"\nВремя отклика - НЕУДАЧНЫЕ ЗАПРОСЫ (секунды):")
            print(f"  Минимальное: {min(self.failed_response_times):.3f}")
            print(f"  Максимальное: {max(self.failed_response_times):.3f}")
            print(f"  Среднее: {statistics.mean(self.failed_response_times):.3f}")
            print(f"  Медиана (p50): {statistics.median(self.failed_response_times):.3f}")
            if len(self.failed_response_times) > 1:
                print(f"  Стандартное отклонение: {statistics.stdev(self.failed_response_times):.3f}")
        
        # Детальная статистика по языкам
        print(f"\nДетальная статистика по языкам:")
        for lang in LANGUAGES.keys():
            success = self.success_by_language.get(lang, 0)
            errors = self.errors_by_language.get(lang, 0)
            total = success + errors
            if total > 0:
                lang_times = self.response_times_by_language.get(lang, [])
                success_times = self.success_times_by_language.get(lang, [])
                
                print(f"\n  {lang.upper()}:")
                print(f"    Всего запросов: {total} (успешно: {success}, ошибок: {errors}, {success/total*100:.1f}%)")
                
                if lang_times:
                    avg_time = statistics.mean(lang_times)
                    print(f"    Среднее время ответа: {avg_time:.3f} сек")
                    print(f"    Мин/Макс: {min(lang_times):.3f} / {max(lang_times):.3f} сек")
                
                if success_times:
                    avg_success_time = statistics.mean(success_times)
                    print(f"    Среднее время успешных: {avg_success_time:.3f} сек")
                    if len(success_times) > 1:
                        print(f"    Медиана успешных: {statistics.median(success_times):.3f} сек")
        
        # Примеры ошибок
        if self.error_messages:
            print(f"\nПримеры ошибок (первые {len(self.error_messages)}):")
            for i, error_msg in enumerate(self.error_messages, 1):
                print(f"  {i}. {error_msg}")
        
        print("="*80)


async def execute_code(
    session: aiohttp.ClientSession,
    url: str,
    language: str,
    code: str,
    timeout: int,
    stats: LoadTestStats
) -> Tuple[bool, float, str]:
    """Выполнить код через API"""
    start_time = time.time()
    
    payload = {
        "lang": language,
        "code": code,
        "timeout": timeout
    }
    
    try:
        async with session.post(
            f"{url}/execute",
            json=payload,
            timeout=aiohttp.ClientTimeout(total=timeout + 5)
        ) as response:
            response_time = time.time() - start_time
            
            if response.status == 200:
                result = await response.json()
                stderr = result.get("stderr", "").strip()
                stdout = result.get("stdout", "").strip()
                
                # Проверяем на критические ошибки в stderr
                critical_errors = [
                    "timeout exceeded",
                    "execution timeout",
                    "failed to execute",
                    "execution failed",
                    "fatal error",
                    "cannot execute"
                ]
                
                stderr_lower = stderr.lower()
                has_critical_error = any(err in stderr_lower for err in critical_errors)
                
                # Если есть stdout и нет критических ошибок - считаем успехом
                # (stderr может содержать предупреждения компилятора, которые не критичны)
                if stdout and not has_critical_error:
                    stats.add_result(language, True, response_time)
                    return True, response_time, ""
                elif has_critical_error:
                    # Реальная ошибка выполнения
                    error_msg = stderr if stderr else "Critical error detected"
                    stats.add_result(language, False, response_time, error_msg)
                    return False, response_time, error_msg
                elif not stdout and stderr:
                    # Нет stdout, но есть stderr - вероятно ошибка
                    error_msg = stderr
                    stats.add_result(language, False, response_time, error_msg)
                    return False, response_time, error_msg
                else:
                    # Пустой ответ (нет ни stdout, ни stderr) - тоже ошибка
                    stats.add_result(language, False, response_time, "Empty response (no stdout/stderr)")
                    return False, response_time, "Empty response"
            else:
                error_text = await response.text()
                error_msg = f"HTTP {response.status}: {error_text[:200]}"
                stats.add_result(language, False, response_time, error_msg)
                return False, response_time, error_msg
    
    except asyncio.TimeoutError:
        response_time = time.time() - start_time
        error_msg = "Request timeout"
        stats.add_result(language, False, response_time, error_msg)
        return False, response_time, error_msg
    
    except Exception as e:
        response_time = time.time() - start_time
        error_msg = str(e)
        stats.add_result(language, False, response_time, error_msg)
        return False, response_time, error_msg


async def check_service_health(url: str) -> bool:
    """Проверить доступность сервиса"""
    try:
        async with aiohttp.ClientSession() as session:
            async with session.get(
                f"{url}/health",
                timeout=aiohttp.ClientTimeout(total=5)
            ) as response:
                return response.status == 200
    except Exception:
        return False


async def run_load_test(
    url: str,
    total_requests: int,
    concurrent_requests: int,
    complex_ratio: float,
    timeout: int
):
    """Запустить нагрузочное тестирование"""
    # Проверка доступности сервиса
    print("Проверка доступности сервиса...")
    if not await check_service_health(url):
        print(f"❌ ОШИБКА: Сервис недоступен по адресу {url}")
        print(f"   Убедитесь, что code-executor запущен и доступен.")
        print(f"   Проверьте: curl {url}/health")
        return
    
    print("✅ Сервис доступен")
    print()
    
    stats = LoadTestStats()
    stats.start_time = time.time()
    
    # Создаем список задач
    tasks = []
    languages_list = list(LANGUAGES.keys())
    
    # Генерируем задачи
    for i in range(total_requests):
        language = languages_list[i % len(languages_list)]
        use_complex = (i % 100) < (complex_ratio * 100)
        code_type = "complex" if use_complex else "simple"
        code = LANGUAGES[language][code_type]
        
        tasks.append((language, code))
    
    print(f"Начало нагрузочного тестирования:")
    print(f"  URL: {url}")
    print(f"  Всего запросов: {total_requests}")
    print(f"  Параллельных запросов: {concurrent_requests}")
    print(f"  Доля сложных задач: {complex_ratio*100:.1f}%")
    print(f"  Таймаут: {timeout} секунд")
    print(f"  Языки: {', '.join(LANGUAGES.keys())}")
    print()
    
    # Создаем семафор для ограничения параллельности
    semaphore = asyncio.Semaphore(concurrent_requests)
    
    async def execute_with_semaphore(language: str, code: str):
        async with semaphore:
            async with aiohttp.ClientSession() as session:
                return await execute_code(session, url, language, code, timeout, stats)
    
    # Выполняем все задачи
    print("Выполнение запросов...")
    start_execution = time.time()
    
    # Создаем корутины для всех задач
    coroutines = [execute_with_semaphore(lang, code) for lang, code in tasks]
    
    # Выполняем с прогресс-баром
    completed = 0
    for coro in asyncio.as_completed(coroutines):
        await coro
        completed += 1
        if completed % max(1, total_requests // 20) == 0:
            progress = completed / total_requests * 100
            print(f"Прогресс: {completed}/{total_requests} ({progress:.1f}%)")
    
    stats.end_time = time.time()
    execution_time = stats.end_time - start_execution
    
    print(f"\nВыполнение завершено за {execution_time:.2f} секунд")
    
    # Выводим статистику
    stats.print_summary()


def main():
    parser = argparse.ArgumentParser(
        description="Нагрузочное тестирование code-executor сервиса",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Примеры использования:
  # Базовый тест: 100 запросов, 10 параллельно
  python3 load_test.py --url http://localhost:8081 --requests 100 --concurrent 10
  
  # Интенсивный тест: 1000 запросов, 50 параллельно, 50% сложных задач
  python3 load_test.py --url http://localhost:8081 --requests 1000 --concurrent 50 --complex-ratio 0.5
  
  # Тест с большим таймаутом для сложных задач
  python3 load_test.py --url http://localhost:8081 --requests 200 --concurrent 20 --timeout 30
        """
    )
    
    parser.add_argument(
        "--url",
        type=str,
        default="http://localhost:8081",
        help="URL code-executor сервиса (по умолчанию: http://localhost:8081)"
    )
    
    parser.add_argument(
        "--requests",
        type=int,
        default=100,
        help="Общее количество запросов (по умолчанию: 100)"
    )
    
    parser.add_argument(
        "--concurrent",
        type=int,
        default=10,
        help="Количество параллельных запросов (по умолчанию: 10)"
    )
    
    parser.add_argument(
        "--complex-ratio",
        type=float,
        default=0.3,
        help="Доля сложных вычислительных задач (0.0-1.0, по умолчанию: 0.3)"
    )
    
    parser.add_argument(
        "--timeout",
        type=int,
        default=30,
        help="Таймаут выполнения в секундах (по умолчанию: 30)"
    )
    
    args = parser.parse_args()
    
    # Валидация аргументов
    if args.requests < 1:
        print("Ошибка: количество запросов должно быть больше 0")
        return
    
    if args.concurrent < 1:
        print("Ошибка: количество параллельных запросов должно быть больше 0")
        return
    
    if args.concurrent > args.requests:
        print("Предупреждение: количество параллельных запросов больше общего количества запросов")
        args.concurrent = args.requests
    
    if not 0 <= args.complex_ratio <= 1:
        print("Ошибка: доля сложных задач должна быть между 0.0 и 1.0")
        return
    
    if args.timeout < 1:
        print("Ошибка: таймаут должен быть больше 0")
        return
    
    # Запускаем тест
    try:
        asyncio.run(run_load_test(
            url=args.url,
            total_requests=args.requests,
            concurrent_requests=args.concurrent,
            complex_ratio=args.complex_ratio,
            timeout=args.timeout
        ))
    except KeyboardInterrupt:
        print("\n\nТестирование прервано пользователем")
    except Exception as e:
        print(f"\n\nОшибка при выполнении теста: {e}")
        import traceback
        traceback.print_exc()


if __name__ == "__main__":
    main()

