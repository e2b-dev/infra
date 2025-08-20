# Placement Algorithm Benchmark Report

## Performance Metrics

### Config

| Parameter              | Value              |
|------------------------|--------------------|  
| **NumNodes**           | 50                 |
| **SandboxStartRate**   | 30                 |
| **AvgSandboxCPU**      | 4                  |
| **AvgSandboxMemory**   | 1024               |
| **CPUVariance**        | 0.8                |
| **MemoryVariance**     | 0.4                |
| **ActualUsageRatio**   | 0.4                |
| **ActualUsageVariance**| 1                  |
| **SandboxDuration**    | 5 seconds          |
| **DurationVariance**   | 5                  |
| **BenchmarkDuration**  | 1 minute           |
| **NodeCPUCapacity**    | 32                 |
| **SandboxCreateDuration** | 0 ms            |

### Placement Latency

| Algorithm | Avg Latency | P50 | P95 | P99 |
|-----------|------------|-----|-----|-----|
| **LeastBusy** | 3.074ms | 3.179ms | 4.641ms | 5.400ms |
| **BestOfK (K=3)** | 38.8µs | 37.5µs | 65.7µs | 86.7µs |
| **BestOfK (K=5)** | 48.4µs | 47.9µs | 73.1µs | 104.7µs |

### Load Distribution Quality

| Algorithm | Avg CPU | CPU StdDev | Load Imbalance |
|-----------|---------|------------|----------------|
| **LeastBusy** | 19.1% | 9.6% | 0.501 |
| **BestOfK (K=3)** | 19.2% | 9.0% | 0.470 |
| **BestOfK (K=5)** | 18.1% | 7.1% | 0.391 |

## Detailed Results

### LeastBusy Algorithm
```
Placement Performance:
  Total: 1199, Success: 1199 (100.0%), Failed: 0
  Latency - Avg: 2.598382ms, P50: 2.482083ms, P95: 4.297875ms, P99: 5.144792ms

Node Utilization:
  CPU - Avg: 31.4%, Min: 8.4%, Max: 55.3%, StdDev: 10.5%
  Memory - Avg: 0.0%, Min: 0.0%, Max: 0.0%, StdDev: 0.0%
  Load Imbalance Coefficient: 0.334
```

### BestOfK (K=3) Algorithm
```
Placement Performance:
  Total: 1199, Success: 1199 (100.0%), Failed: 0
  Latency - Avg: 141.539µs, P50: 112.625µs, P95: 281.167µs, P99: 402.375µs

Node Utilization:
  CPU - Avg: 28.3%, Min: 10.8%, Max: 49.1%, StdDev: 8.9%
  Memory - Avg: 0.0%, Min: 0.0%, Max: 0.0%, StdDev: 0.0%
  Load Imbalance Coefficient: 0.313
```

### BestOfK (K=5) Algorithm
```
Placement Performance:
  Total: 1199, Success: 1199 (100.0%), Failed: 0
  Latency - Avg: 139.015µs, P50: 108.375µs, P95: 285.583µs, P99: 394.583µs

Node Utilization:
  CPU - Avg: 28.6%, Min: 7.6%, Max: 48.2%, StdDev: 7.5%
  Memory - Avg: 0.0%, Min: 0.0%, Max: 0.0%, StdDev: 0.0%
  Load Imbalance Coefficient: 0.262
```

# Placement Algorithm Benchmark Results (sandbox create time 100 ms +- 100 ms)

## Performance Metrics

### Config

| Parameter              | Value              |
|------------------------|--------------------|  
| **NumNodes**           | 50                 |
| **SandboxStartRate**   | 30                 |
| **AvgSandboxCPU**      | 4                  |
| **AvgSandboxMemory**   | 1024               |
| **CPUVariance**        | 0.8                |
| **MemoryVariance**     | 0.4                |
| **ActualUsageRatio**   | 0.4                |
| **ActualUsageVariance**| 1                  |
| **SandboxDuration**    | 5 seconds          |
| **DurationVariance**   | 5                  |
| **BenchmarkDuration**  | 1 minute           |
| **NodeCPUCapacity**    | 32                 |
| **SandboxCreateDuration** | 100 ms ± 100 ms |

### Placement Latency

| Algorithm         | Avg Latency  | P50          | P95          | P99          |
|-------------------|--------------|--------------|--------------|--------------|
| **LeastBusy**     | 101.704ms    | 101.474ms    | 191.673ms    | 200.195ms    |
| **BestOfK (K=3)** | 100.749ms    | 99.990ms     | 190.990ms    | 198.289ms    |
| **BestOfK (K=5)** | 98.083ms     | 96.536ms     | 190.786ms    | 198.220ms    |

### Load Distribution Quality

| Algorithm         | Avg CPU | Min CPU | Max CPU | CPU StdDev | Load Imbalance |
|-------------------|---------|---------|---------|------------|----------------|
| **LeastBusy**     | 26.3%   | 6.2%    | 51.8%   | 10.8%      | 0.410          |
| **BestOfK (K=3)** | 29.0%   | 2.2%    | 49.2%   | 9.5%       | 0.329          |
| **BestOfK (K=5)** | 29.0%   | 13.4%   | 49.3%   | 7.6%       | 0.262          |

### LeastBusy Algorithm
```
Placement Performance:
   Total: 1800, Success: 1800 (100.0%), Failed: 0
   Latency - Avg: 101.703997ms, P50: 101.474083ms, P95: 191.673458ms, P99: 200.195167ms

Node Utilization:
   CPU - Avg: 26.3%, Min: 6.2%, Max: 51.8%, StdDev: 10.8%
   Memory - Avg: 0.0%, Min: 0.0%, Max: 0.0%, StdDev: 0.0%
   Load Imbalance Coefficient: 0.410
```

### BestOfK_K3 Algorithm
```
Placement Performance:
   Total: 1799, Success: 1799 (100.0%), Failed: 0
   Latency - Avg: 100.748725ms, P50: 99.990333ms, P95: 190.989792ms, P99: 198.288916ms

Node Utilization:
   CPU - Avg: 29.0%, Min: 2.2%, Max: 49.2%, StdDev: 9.5%
   Memory - Avg: 0.0%, Min: 0.0%, Max: 0.0%, StdDev: 0.0%
   Load Imbalance Coefficient: 0.329
```

### BestOfK_K5 Algorithm
```
Placement Performance:
   Total: 1799, Success: 1799 (100.0%), Failed: 0
   Latency - Avg: 98.082505ms, P50: 96.535792ms, P95: 190.785541ms, P99: 198.220458ms

Node Utilization:
   CPU - Avg: 29.0%, Min: 13.4%, Max: 49.3%, StdDev: 7.6%
   Memory - Avg: 0.0%, Min: 0.0%, Max: 0.0%, StdDev: 0.0%
   Load Imbalance Coefficient: 0.262
```
