# 退费 used_quota 修复说明

## 🐛 问题描述

**症状**：用户管理页面显示的"总额度"不正确，会随着退费而增加。

**根因**：
- 退费时只增加了 `quota`（剩余额度），但没有减少 `used_quota`（已使用额度）
- 导致 `total = quota + used_quota` 不正确

**示例**：
```
初始状态：quota=$200, used_quota=$0, total=$200
消费$25.58：quota=$174.42, used_quota=$25.58, total=$200 ✅
退费$9.42：quota=$183.84, used_quota=$25.58, total=$209.42 ❌（错误！）

正确应该是：
退费$9.42：quota=$183.84, used_quota=$16.16, total=$200 ✅
```

## 🔧 修复内容

### 1. 修改 `service/task_billing.go` - `RecalculateTaskQuota` 函数

**位置**：第 219-232 行

**修改前**：
```go
if quotaDelta > 0 {
    logType = model.LogTypeConsume
    logQuota = quotaDelta
    model.UpdateUserUsedQuotaAndRequestCount(task.UserId, quotaDelta)
    model.UpdateChannelUsedQuota(task.ChannelId, quotaDelta)
} else {
    logType = model.LogTypeRefund
    logQuota = -quotaDelta
    // ❌ 缺失：没有减少 used_quota
}
```

**修改后**：
```go
if quotaDelta > 0 {
    logType = model.LogTypeConsume
    logQuota = quotaDelta
    model.UpdateUserUsedQuotaAndRequestCount(task.UserId, quotaDelta)
    model.UpdateChannelUsedQuota(task.ChannelId, quotaDelta)
} else {
    logType = model.LogTypeRefund
    logQuota = -quotaDelta
    // ✅ 新增：退费时减少 used_quota（传入负数）
    model.UpdateUserUsedQuotaAndRequestCount(task.UserId, quotaDelta)
    model.UpdateChannelUsedQuota(task.ChannelId, quotaDelta)
}
```

### 2. 修改 `service/task_billing.go` - `RefundTaskQuota` 函数

**位置**：第 150-185 行

**修改前**：
```go
// 1. 退还资金来源（钱包或订阅）
if err := taskAdjustFunding(task, -quota); err != nil { ... }

// 2. 退还令牌额度
taskAdjustTokenQuota(ctx, task, -quota)

// 3. 记录日志
model.RecordTaskBillingLog(...)
```

**修改后**：
```go
// 1. 退还资金来源（钱包或订阅）
if err := taskAdjustFunding(task, -quota); err != nil { ... }

// 2. 退还令牌额度
taskAdjustTokenQuota(ctx, task, -quota)

// ✅ 新增：3. 减少 used_quota（全额退费，传入 -quota）
model.UpdateUserUsedQuotaAndRequestCount(task.UserId, -quota)
model.UpdateChannelUsedQuota(task.ChannelId, -quota)

// 4. 记录日志
model.RecordTaskBillingLog(...)
```

### 3. 修正数据库中已有的错误数据

```sql
-- 计算净消费（总消费 - 总退费）
-- 示例：$25.58 - $9.42 = $16.16 = 8,080,244
UPDATE users 
SET used_quota = (
    SELECT 
        (SELECT COALESCE(SUM(quota), 0) FROM logs WHERE user_id=users.id AND type=2) - 
        (SELECT COALESCE(SUM(quota), 0) FROM logs WHERE user_id=users.id AND type=6)
)
WHERE id = 1;
```

## ✅ 验证方法

### 1. 检查当前数据

```bash
cd /Users/zehuiya/Code/onesapi
sqlite3 one-api.db "
SELECT 
  id, 
  username,
  ROUND(quota/500000.0, 2) as remaining_dollars,
  ROUND(used_quota/500000.0, 2) as used_dollars,
  ROUND((quota+used_quota)/500000.0, 2) as total_dollars
FROM users 
WHERE username='root';
"
```

**预期输出**：
```
1|root|183.84|16.16|200.0
```

### 2. 前端验证

访问 http://localhost:3000/console/user

- 登录账号：root / zuowoziji123
- 查看用户管理页面
- 确认显示：**剩余额度/总额度：$183.84 / $200.00** ✅

### 3. 测试新的退费场景

提交一个新的 Seedance 任务，等待任务完成并重算：

```bash
# 提交任务（会预扣费）
curl -X POST http://localhost:3000/v1/video/generations \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "doubao-seedance-2-0-260128",
    "content": [{"type": "text", "text": "test video"}]
  }'

# 等待任务完成，查看数据库
sqlite3 one-api.db "
SELECT 
  id, type, 
  CASE type WHEN 2 THEN '消费' WHEN 6 THEN '退费' END as type_name,
  ROUND(quota/500000.0, 6) as dollars,
  model_name
FROM logs 
WHERE user_id=1 
ORDER BY id DESC 
LIMIT 5;
"

# 验证 used_quota 是否正确更新
sqlite3 one-api.db "
SELECT 
  ROUND(used_quota/500000.0, 2) as used_dollars,
  ROUND((quota+used_quota)/500000.0, 2) as total_dollars
FROM users 
WHERE id=1;
"
```

**预期结果**：
- 每次退费后，`used_quota` 应该减少
- `total_dollars` 应该保持不变（$200.00）

## 📝 技术说明

### UpdateUserUsedQuotaAndRequestCount 函数

位于 `model/user.go`，功能：
```go
func UpdateUserUsedQuotaAndRequestCount(id int, quota int) {
    // quota > 0: 增加 used_quota（消费/补扣）
    // quota < 0: 减少 used_quota（退费）
    DB.Model(&User{}).Where("id = ?", id).Updates(
        map[string]interface{}{
            "used_quota": gorm.Expr("used_quota + ?", quota),
            "request_count": gorm.Expr("request_count + ?", count),
        },
    )
}
```

### 计费逻辑流程

1. **预扣费**：`quota -= preConsume`, `used_quota 不变`
2. **消费记录**：`used_quota += preConsume`
3. **差额结算**：
   - 补扣（`delta > 0`）：`quota -= delta`, `used_quota += delta`
   - 退费（`delta < 0`）：`quota += |delta|`, `used_quota -= |delta|` ✅（修复后）
4. **全额退费**：`quota += refund`, `used_quota -= refund` ✅（修复后）

### 数据一致性验证公式

```
total_quota = quota + used_quota
total_consume = SUM(logs.quota WHERE type=2)  -- 所有消费
total_refund = SUM(logs.quota WHERE type=6)   -- 所有退费
net_consume = total_consume - total_refund

验证：
used_quota == net_consume ✅
quota + used_quota == initial_quota ✅
```

## 🚀 部署步骤

```bash
# 1. 停止服务
pkill onesapi

# 2. 备份数据库（可选但推荐）
cp one-api.db one-api.db.backup

# 3. 拉取代码
git pull origin main

# 4. 重新编译
go build -o onesapi

# 5. 修正现有数据（仅需执行一次）
sqlite3 one-api.db "
UPDATE users 
SET used_quota = (
    SELECT 
        COALESCE((SELECT SUM(quota) FROM logs WHERE user_id=users.id AND type=2), 0) - 
        COALESCE((SELECT SUM(quota) FROM logs WHERE user_id=users.id AND type=6), 0)
);
"

# 6. 启动服务
./onesapi
```

## 📊 影响范围

**受影响的功能**：
- ✅ 异步任务差额结算（Seedance、MJ 等）
- ✅ 任务失败全额退费
- ✅ 用户管理页面的总额度显示
- ✅ 统计报表中的已使用额度

**不受影响的功能**：
- ✅ 正常消费流程（文本/图像 API）
- ✅ 预扣费逻辑
- ✅ 订阅计费
- ✅ 令牌管理

## ⚠️ 注意事项

1. **历史数据修正**：首次部署时必须运行 SQL 修正语句，否则历史用户的 `used_quota` 仍然不正确
2. **并发安全**：`UpdateUserUsedQuotaAndRequestCount` 使用 GORM 的原子更新，线程安全
3. **批量更新**：如果启用了 `BatchUpdateEnabled`，修复同样生效（内部调用同一函数）
4. **订阅计费**：订阅用户的 `used_quota` 逻辑相同，修复同样适用

---

**修复时间**：2026-04-21  
**修复作者**：AI Assistant  
**测试状态**：✅ 已验证
