# Doubao Seedance 计费测试场景

本文档提供实际测试场景，帮助验证差异化计费配置是否正确。

## 测试前准备

1. 确保已按照 `PRICING_CONFIG.md` 配置好各模型的基准倍率
2. 启用详细日志以便查看 `OtherRatios`
3. 准备测试素材（图片、视频、音频 URL）

## 测试场景 1: doubao-seedance-2-0-260128

### 场景 1.1: 480p + 不含视频（基准场景）

**请求示例**：
```json
{
  "model": "doubao-seedance-2-0-260128",
  "content": [
    {
      "type": "text",
      "text": "小猫对着镜头打哈欠"
    }
  ],
  "resolution": "480p",
  "duration": 5,
  "watermark": false
}
```

**预期计费**：
- ModelRatio: 3.15
- OtherRatios: 无（或为空）
- 最终倍率: 3.15
- 对应价格: 46元/百万token ✅

### 场景 1.2: 480p + 含视频输入

**请求示例**：
```json
{
  "model": "doubao-seedance-2-0-260128",
  "content": [
    {
      "type": "text",
      "text": "将视频中的猫咪换成狗"
    },
    {
      "type": "video_url",
      "video_url": {
        "url": "https://example.com/cat.mp4"
      },
      "role": "reference_video"
    }
  ],
  "resolution": "480p",
  "duration": 5
}
```

**预期计费**：
- ModelRatio: 3.15
- OtherRatios: `{"video_input": 0.609}`
- 最终倍率: 3.15 × 0.609 ≈ 1.92
- 对应价格: 28元/百万token ✅

### 场景 1.3: 1080p + 不含视频

**请求示例**：
```json
{
  "model": "doubao-seedance-2-0-260128",
  "content": [
    {
      "type": "text",
      "text": "雪山日出，镜头缓慢上升"
    }
  ],
  "resolution": "1080p",
  "duration": 8
}
```

**预期计费**：
- ModelRatio: 3.15
- OtherRatios: `{"resolution": 1.109}`
- 最终倍率: 3.15 × 1.109 ≈ 3.49
- 对应价格: 51元/百万token ✅

### 场景 1.4: 1080p + 含视频输入（叠加倍率）

**请求示例**：
```json
{
  "model": "doubao-seedance-2-0-260128",
  "content": [
    {
      "type": "text",
      "text": "延长视频，增加日落效果"
    },
    {
      "type": "video_url",
      "video_url": {
        "url": "https://example.com/mountain.mp4"
      },
      "role": "reference_video"
    }
  ],
  "resolution": "1080p",
  "duration": 10
}
```

**预期计费**：
- ModelRatio: 3.15
- OtherRatios: `{"resolution": 1.109, "video_input": 0.609}`
- 最终倍率: 3.15 × 1.109 × 0.609 ≈ 2.13
- 对应价格: 31元/百万token ✅

## 测试场景 2: doubao-seedance-2-0-fast-260128

### 场景 2.1: 720p + 不含视频（基准）

**请求示例**：
```json
{
  "model": "doubao-seedance-2-0-fast-260128",
  "content": [
    {
      "type": "text",
      "text": "快速生成：城市街景，人流穿梭"
    }
  ],
  "resolution": "720p",
  "duration": 5
}
```

**预期计费**：
- ModelRatio: 2.52
- OtherRatios: 无
- 最终倍率: 2.52
- 对应价格: 37元/百万token ✅

### 场景 2.2: 720p + 含视频输入

**请求示例**：
```json
{
  "model": "doubao-seedance-2-0-fast-260128",
  "content": [
    {
      "type": "text",
      "text": "编辑视频，将天空换成黄昏"
    },
    {
      "type": "video_url",
      "video_url": {
        "url": "https://example.com/city.mp4"
      },
      "role": "reference_video"
    }
  ],
  "resolution": "720p",
  "duration": 6
}
```

**预期计费**：
- ModelRatio: 2.52
- OtherRatios: `{"video_input": 0.595}`
- 最终倍率: 2.52 × 0.595 ≈ 1.50
- 对应价格: 22元/百万token ✅

## 测试场景 3: doubao-seedance-1-5-pro-251215

### 场景 3.1: 有声视频（基准）

**请求示例**：
```json
{
  "model": "doubao-seedance-1-5-pro-251215",
  "content": [
    {
      "type": "text",
      "text": "海浪拍打礁石，配自然音效"
    },
    {
      "type": "image_url",
      "image_url": {
        "url": "https://example.com/ocean.jpg"
      }
    }
  ],
  "generate_audio": true,
  "duration": 8
}
```

**预期计费**：
- ModelRatio: 1.09
- OtherRatios: 无
- 最终倍率: 1.09
- 对应价格: 16元/百万token ✅

### 场景 3.2: 无声视频

**请求示例**：
```json
{
  "model": "doubao-seedance-1-5-pro-251215",
  "content": [
    {
      "type": "text",
      "text": "静态风景展示，无需音频"
    },
    {
      "type": "image_url",
      "image_url": {
        "url": "https://example.com/landscape.jpg"
      }
    }
  ],
  "generate_audio": false,
  "duration": 6
}
```

**预期计费**：
- ModelRatio: 1.09
- OtherRatios: `{"audio": 0.5}`
- 最终倍率: 1.09 × 0.5 ≈ 0.55
- 对应价格: 8元/百万token ✅

## 测试场景 4: doubao-seedance-1-0-pro-250528

### 场景 4.1: 标准图生视频（无差异化）

**请求示例**：
```json
{
  "model": "doubao-seedance-1-0-pro-250528",
  "content": [
    {
      "type": "text",
      "text": "女孩抱着狐狸，温柔地看向镜头"
    },
    {
      "type": "image_url",
      "image_url": {
        "url": "https://example.com/girl.png"
      }
    }
  ],
  "ratio": "16:9",
  "duration": 5
}
```

**预期计费**：
- ModelRatio: 1.02
- OtherRatios: 无（该模型无差异化计费）
- 最终倍率: 1.02
- 对应价格: 15元/百万token ✅

## 验证方法

### 方法 1: 查看日志

在日志文件中搜索关键字段：

```bash
# 查看计费上下文
grep -A 10 "billing_context" /path/to/log/file.log

# 查看 OtherRatios
grep "other_ratios" /path/to/log/file.log | jq .
```

**示例日志输出**：
```json
{
  "time": "2025-01-20T10:30:00Z",
  "level": "info",
  "msg": "task billing recorded",
  "task_id": "cgt-2025xxxx",
  "model": "doubao-seedance-2-0-260128",
  "origin_model_name": "doubao-seedance-2-0-260128",
  "billing_context": {
    "model_ratio": 3.15,
    "group_ratio": 1.0,
    "other_ratios": {
      "resolution": 1.109,
      "video_input": 0.609
    }
  },
  "tokens": 1000,
  "quota": 2130
}
```

### 方法 2: 数据库查询

查询 `tasks` 表的 `private_data` 字段：

```sql
SELECT 
    id,
    model,
    JSON_EXTRACT(private_data, '$.billing_context.model_ratio') as model_ratio,
    JSON_EXTRACT(private_data, '$.billing_context.other_ratios') as other_ratios,
    quota
FROM tasks 
WHERE model LIKE 'doubao-seedance%'
ORDER BY created_at DESC
LIMIT 10;
```

### 方法 3: API 响应验证

通过查询任务详情 API，检查 `usage` 字段：

```bash
curl -X GET "https://your-api-endpoint/v1/contents/generations/tasks/{task_id}" \
  -H "Authorization: Bearer YOUR_API_KEY"
```

## 常见问题排查

### Q1: OtherRatios 始终为空

**可能原因**：
1. 请求参数与基准场景完全一致
2. 参数提取失败（如 `resolution` 字段格式不对）
3. 模型配置错误

**排查步骤**：
```go
// 在 adaptor.go 的 EstimateBilling 方法中添加调试日志
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	
	// 调试日志
	fmt.Printf("DEBUG: model=%s, resolution=%s, hasVideo=%v, generateAudio=%v\n",
		info.OriginModelName,
		parseResolution(req),
		hasVideoInMetadata(req.Metadata),
		parseGenerateAudio(req))
	
	// ... 原有逻辑
}
```

### Q2: 实际计费与预期不符

**可能原因**：
1. ModelRatio 配置错误
2. OtherRatios 计算错误
3. GroupRatio 影响

**验证公式**：
```
最终 quota = tokens × ModelRatio × GroupRatio × (OtherRatios 乘积)
```

**示例计算**：
```
tokens = 1000
ModelRatio = 3.15
GroupRatio = 1.0
OtherRatios = {"resolution": 1.109, "video_input": 0.609}

OtherRatios 乘积 = 1.109 × 0.609 = 0.675
最终 quota = 1000 × 3.15 × 1.0 × 0.675 = 2126.25 ≈ 2126
```

### Q3: 分辨率始终识别为 720p

**可能原因**：
1. 请求中未传递 `resolution` 或 `size` 参数
2. 参数格式不正确

**解决方案**：
- 确保在请求的 `metadata` 中包含 `resolution` 字段：
  ```json
  {
    "model": "doubao-seedance-2-0-260128",
    "content": [...],
    "resolution": "1080p"  // 添加此字段
  }
  ```

## 性能测试建议

建议对每个模型的各种场景组合进行压测，验证：

1. **准确性**: 计费金额是否与官方价格一致
2. **性能**: OtherRatios 计算是否影响响应时间
3. **稳定性**: 高并发下计费逻辑是否稳定

**压测脚本示例**：
```bash
#!/bin/bash

# 测试 2.0 模型的 4 种场景组合
for resolution in "480p" "1080p"; do
  for has_video in true false; do
    echo "Testing resolution=$resolution, has_video=$has_video"
    # 发送请求并验证
    # ...
  done
done
```

## 总结

通过上述测试场景，可以全面验证 Seedance 系列模型的差异化计费逻辑。建议在生产环境上线前，完整执行所有测试场景，确保计费准确无误。
