# 缓存失效问题分析与解决方案

**问题**: Code Review 反馈 - 缓存失效逻辑不完整  
**日期**: 2026-05-07  
**状态**: 🔴 需要修复

---

## 📋 问题分析

### 问题描述

当引入组合缓存键 `sandboxID:teamID` 时，现有的 `Invalidate` 方法只删除简单的 `sandboxID` 键，导致：

1. **缓存污染**: 团队范围的缓存键 (`sandboxID:teamID`) 永远不会被清除
2. **数据过期**: 快照更新后，旧数据仍然存在于缓存中
3. **不一致性**: 不同团队可能看到相同的过期数据

### 代码现状

```go
// 旧的 Invalidate 方法 - 只删除简单键
func (c *SnapshotCache) Invalidate(ctx context.Context, sandboxID string) {
	c.cache.Delete(ctx, sandboxID)  // 只删除 "sandboxID"
	// 但 "sandboxID:teamID1", "sandboxID:teamID2" 等键仍然存在！
}
```

### 缓存键结构

```
旧方法 (Get):
  键: "snapshot:last:sandbox-123"
  
新方法 (GetWithTeamID):
  键: "snapshot:last:sandbox-123:team-uuid-1"
  键: "snapshot:last:sandbox-123:team-uuid-2"
  键: "snapshot:last:sandbox-123:team-uuid-3"
```

### 失效场景

```
时间线:
1. Team A 调用 GetWithTeamID(sandbox-123, team-a)
   → 缓存键: "snapshot:last:sandbox-123:team-a"
   → 缓存值: {SnapshotID: "snap-1", ...}

2. Team B 调用 GetWithTeamID(sandbox-123, team-b)
   → 缓存键: "snapshot:last:sandbox-123:team-b"
   → 缓存值: {SnapshotID: "snap-1", ...}

3. 快照更新，调用 Invalidate(sandbox-123)
   → 删除: "snapshot:last:sandbox-123" (旧键，不存在)
   → 未删除: "snapshot:last:sandbox-123:team-a" ❌
   → 未删除: "snapshot:last:sandbox-123:team-b" ❌

4. Team A 再次调用 GetWithTeamID(sandbox-123, team-a)
   → 返回过期的缓存值 ❌
```

---

## 🔍 根本原因

### 问题根源

1. **缓存键策略不一致**
   - `Get()` 使用简单键: `sandboxID`
   - `GetWithTeamID()` 使用组合键: `sandboxID:teamID`
   - `Invalidate()` 只知道简单键

2. **缺少 teamID 信息**
   - `Invalidate()` 方法只接收 `sandboxID`
   - 无法知道需要删除哪些 `teamID` 的缓存

3. **Redis 键模式问题**
   - 无法通过模式匹配删除所有相关键
   - 需要显式知道所有 `teamID`

---

## ✅ 解决方案

### 方案 1: 添加 InvalidateWithTeamID 方法（推荐）

**优点**:
- 精确删除特定 team 的缓存
- 不影响其他 team 的缓存
- 性能最优

**缺点**:
- 需要调用者提供 teamID
- 需要更新所有调用 Invalidate 的地方

**实现**:

```go
// Invalidate removes the cached snapshot for a sandbox (deprecated).
// Deprecated: Use InvalidateWithTeamID instead to ensure all cache entries are cleared.
func (c *SnapshotCache) Invalidate(ctx context.Context, sandboxID string) {
	c.cache.Delete(ctx, sandboxID)
}

// InvalidateWithTeamID removes the cached snapshot for a specific team.
// This ensures both old and new cache keys are properly cleared.
func (c *SnapshotCache) InvalidateWithTeamID(ctx context.Context, sandboxID string, teamID uuid.UUID) {
	// Delete the new team-scoped key
	cacheKey := fmt.Sprintf("%s:%s", sandboxID, teamID.String())
	c.cache.Delete(ctx, cacheKey)
	
	// Also delete the old key for backward compatibility
	c.cache.Delete(ctx, sandboxID)
}
```

### 方案 2: 使用 Redis 模式匹配

**优点**:
- 自动删除所有相关键
- 不需要知道所有 teamID

**缺点**:
- 性能较差（需要扫描所有键）
- 可能删除不相关的键

**实现**:

```go
// Invalidate removes all cached snapshots for a sandbox.
func (c *SnapshotCache) Invalidate(ctx context.Context, sandboxID string) {
	// Delete the simple key
	c.cache.Delete(ctx, sandboxID)
	
	// Delete all team-scoped keys using pattern matching
	pattern := fmt.Sprintf("%s:%s:*", snapshotCacheKeyPrefix, sandboxID)
	c.cache.DeletePattern(ctx, pattern)
}
```

### 方案 3: 统一缓存键策略

**优点**:
- 简单一致
- 易于维护

**缺点**:
- 需要修改 Get 方法
- 可能影响现有代码

**实现**:

```go
// 统一使用组合键
func (c *SnapshotCache) Get(ctx context.Context, sandboxID string, teamID uuid.UUID) (*SnapshotInfo, error) {
	// 使用相同的缓存键格式
	cacheKey := fmt.Sprintf("%s:%s", sandboxID, teamID.String())
	// ...
}

func (c *SnapshotCache) Invalidate(ctx context.Context, sandboxID string, teamID uuid.UUID) {
	cacheKey := fmt.Sprintf("%s:%s", sandboxID, teamID.String())
	c.cache.Delete(ctx, cacheKey)
}
```

---

## 🎯 推荐方案：方案 1 + 方案 2 的混合

结合两个方案的优点，提供最佳的安全性和性能：

```go
// Invalidate removes the cached snapshot for a sandbox.
// Deprecated: Use InvalidateWithTeamID instead to ensure proper cache cleanup.
func (c *SnapshotCache) Invalidate(ctx context.Context, sandboxID string) {
	// Delete the old simple key for backward compatibility
	c.cache.Delete(ctx, sandboxID)
	
	// Also attempt to delete team-scoped keys using pattern matching
	// This ensures we don't leave stale data in the cache
	pattern := fmt.Sprintf("%s:*", sandboxID)
	c.cache.DeletePattern(ctx, pattern)
}

// InvalidateWithTeamID removes the cached snapshot for a specific team.
// This is the preferred method for precise cache invalidation.
func (c *SnapshotCache) InvalidateWithTeamID(ctx context.Context, sandboxID string, teamID uuid.UUID) {
	// Delete the team-scoped key
	cacheKey := fmt.Sprintf("%s:%s", sandboxID, teamID.String())
	c.cache.Delete(ctx, cacheKey)
}
```

---

## 📝 实现步骤

### 步骤 1: 检查 RedisCache 是否支持 DeletePattern

```go
// 查看 packages/shared/pkg/cache/redis_cache.go
// 检查是否有 DeletePattern 方法
```

### 步骤 2: 如果不支持，添加 DeletePattern 方法

```go
// 在 RedisCache 中添加
func (rc *RedisCache[T]) DeletePattern(ctx context.Context, pattern string) error {
	// 使用 SCAN 命令扫描匹配的键
	var cursor uint64
	for {
		keys, newCursor, err := rc.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return err
		}
		
		if len(keys) > 0 {
			if err := rc.client.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		
		cursor = newCursor
		if cursor == 0 {
			break
		}
	}
	return nil
}
```

### 步骤 3: 更新 SnapshotCache

```go
// 修改 Invalidate 方法
func (c *SnapshotCache) Invalidate(ctx context.Context, sandboxID string) {
	c.cache.Delete(ctx, sandboxID)
	pattern := fmt.Sprintf("%s:*", sandboxID)
	c.cache.DeletePattern(ctx, pattern)
}

// 添加 InvalidateWithTeamID 方法
func (c *SnapshotCache) InvalidateWithTeamID(ctx context.Context, sandboxID string, teamID uuid.UUID) {
	cacheKey := fmt.Sprintf("%s:%s", sandboxID, teamID.String())
	c.cache.Delete(ctx, cacheKey)
}
```

### 步骤 4: 更新调用处

在 `sandbox_kill.go` 中：

```go
// 之前
a.snapshotCache.Invalidate(context.WithoutCancel(ctx), sandboxID)

// 之后（如果有 teamID）
a.snapshotCache.InvalidateWithTeamID(context.WithoutCancel(ctx), sandboxID, teamID)

// 或保持原样（使用模式匹配）
a.snapshotCache.Invalidate(context.WithoutCancel(ctx), sandboxID)
```

---

## 🧪 测试用例

```go
func TestInvalidate_RemovesTeamScopedKeys(t *testing.T) {
	ctx := context.Background()
	sandboxID := "test-sandbox"
	teamID1 := uuid.New()
	teamID2 := uuid.New()

	// 1. 添加两个 team 的缓存
	cache.GetWithTeamID(ctx, sandboxID, teamID1)
	cache.GetWithTeamID(ctx, sandboxID, teamID2)

	// 2. 验证缓存存在
	snap1, _ := cache.GetWithTeamID(ctx, sandboxID, teamID1)
	snap2, _ := cache.GetWithTeamID(ctx, sandboxID, teamID2)
	assert.NotNil(t, snap1)
	assert.NotNil(t, snap2)

	// 3. 调用 Invalidate
	cache.Invalidate(ctx, sandboxID)

	// 4. 验证所有缓存都被删除
	snap1, err1 := cache.GetWithTeamID(ctx, sandboxID, teamID1)
	snap2, err2 := cache.GetWithTeamID(ctx, sandboxID, teamID2)
	assert.Error(t, err1)
	assert.Error(t, err2)
}

func TestInvalidateWithTeamID_RemovesOnlySpecificTeam(t *testing.T) {
	ctx := context.Background()
	sandboxID := "test-sandbox"
	teamID1 := uuid.New()
	teamID2 := uuid.New()

	// 1. 添加两个 team 的缓存
	cache.GetWithTeamID(ctx, sandboxID, teamID1)
	cache.GetWithTeamID(ctx, sandboxID, teamID2)

	// 2. 只删除 team1 的缓存
	cache.InvalidateWithTeamID(ctx, sandboxID, teamID1)

	// 3. 验证 team1 的缓存被删除，team2 的保留
	snap1, err1 := cache.GetWithTeamID(ctx, sandboxID, teamID1)
	snap2, err2 := cache.GetWithTeamID(ctx, sandboxID, teamID2)
	assert.Error(t, err1)
	assert.NotNil(t, snap2)
}
```

---

## 📊 影响分析

### 受影响的文件

1. **packages/api/internal/cache/snapshots/snapshot_cache.go**
   - 修改 `Invalidate` 方法
   - 添加 `InvalidateWithTeamID` 方法

2. **packages/shared/pkg/cache/redis_cache.go**（可选）
   - 添加 `DeletePattern` 方法

3. **packages/api/internal/handlers/sandbox_kill.go**
   - 更新调用 `Invalidate` 的地方

### 向后兼容性

- ✅ 旧的 `Invalidate` 方法保持可用
- ✅ 标记为已弃用
- ✅ 新方法提供更好的功能

---

## 🎯 最终建议

**采用推荐方案**（方案 1 + 方案 2 的混合）：

1. **立即修复**: 更新 `Invalidate` 方法使用模式匹配
2. **添加新方法**: 提供 `InvalidateWithTeamID` 用于精确删除
3. **逐步迁移**: 在后续 PR 中更新调用处使用新方法
4. **标记弃用**: 标记旧方法为已弃用

这样既能解决当前问题，又能为未来提供更好的 API。

---

## 📝 修复提交信息

```
fix(cache): fix cache invalidation for team-scoped snapshot keys

- Update Invalidate method to delete team-scoped cache keys using pattern matching
- Add InvalidateWithTeamID method for precise cache invalidation
- Prevent stale snapshot data from persisting in cache after invalidation
- Fixes cache pollution issue identified in code review

The composite cache key (sandboxID:teamID) introduced in the previous commit
required updating the invalidation logic to ensure all related cache entries
are properly cleared, not just the simple sandboxID key.
```

---

**状态**: 🔴 需要修复  
**优先级**: 🔴 高  
**建议**: 采用推荐方案立即修复
