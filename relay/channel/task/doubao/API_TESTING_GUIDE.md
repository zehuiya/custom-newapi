# Doubao 视频生成 API 测试指南

## ✅ 正确的请求格式

### 基础文生视频（480p）

```json
POST http://localhost:3000/v1/video/generations

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

### 480p + 含视频输入（测试视频输入折扣）

```json
POST http://localhost:3000/v1/video/generations

{
  "model": "doubao-seedance-2-0-260128",
  "content": [
    {
      "type": "text",
      "text": "将视频中的猫咪换成小狗"
    },
    {
      "type": "video_url",
      "video_url": {
        "url": "https://example.com/test-video.mp4"
      },
      "role": "reference_video"
    }
  ],
  "resolution": "480p",
  "duration": 5,
  "watermark": false
}
```

### 1080p + 不含视频（测试分辨率加价）

```json
POST http://localhost:3000/v1/video/generations

{
  "model": "doubao-seedance-2-0-260128",
  "content": [
    {
      "type": "text",
      "text": "雪山日出，镜头缓慢推进"
    }
  ],
  "resolution": "1080p",
  "duration": 8,
  "watermark": false
}
```

### 1080p + 含视频（测试倍率叠加）

```json
POST http://localhost:3000/v1/video/generations

{
  "model": "doubao-seedance-2-0-260128",
  "content": [
    {
      "type": "text",
      "text": "延长视频，增加黄昏效果"
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
  "duration": 10,
  "watermark": false
}
```

### 图生视频（首帧）

```json
POST http://localhost:3000/v1/video/generations

{
  "model": "doubao-seedance-2-0-260128",
  "content": [
    {
      "type": "text",
      "text": "女孩睁开眼，温柔地看向镜头"
    },
    {
      "type": "image_url",
      "image_url": {
        "url": "https://example.com/girl.png"
      }
    }
  ],
  "resolution": "720p",
  "ratio": "16:9",
  "duration": 5
}
```

### 多模态参考生视频

```json
POST http://localhost:3000/v1/video/generations

{
  "model": "doubao-seedance-2-0-260128",
  "content": [
    {
      "type": "text",
      "text": "参考图片1中的女孩，参考视频1的运镜方式，配上音频1的背景音乐"
    },
    {
      "type": "image_url",
      "image_url": {
        "url": "https://example.com/reference1.jpg"
      },
      "role": "reference_image"
    },
    {
      "type": "video_url",
      "video_url": {
        "url": "https://example.com/reference-motion.mp4"
      },
      "role": "reference_video"
    },
    {
      "type": "audio_url",
      "audio_url": {
        "url": "https://example.com/background-music.mp3"
      },
      "role": "reference_audio"
    }
  ],
  "generate_audio": true,
  "resolution": "720p",
  "duration": 11
}
```

### seedance-1.5-pro 有声视频

```json
POST http://localhost:3000/v1/video/generations

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

### seedance-1.5-pro 无声视频（测试音频折扣）

```json
POST http://localhost:3000/v1/video/generations

{
  "model": "doubao-seedance-1-5-pro-251215",
  "content": [
    {
      "type": "text",
      "text": "静态风景展示"
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

## 🔧 使用 curl 测试

```bash
# 基础测试（480p 不含视频）
curl -X POST "http://localhost:3000/v1/video/generations" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
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
  }'

# 测试视频输入折扣（480p + 含视频）
curl -X POST "http://localhost:3000/v1/video/generations" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "doubao-seedance-2-0-260128",
    "content": [
      {
        "type": "text",
        "text": "编辑视频内容"
      },
      {
        "type": "video_url",
        "video_url": {
          "url": "https://example.com/test.mp4"
        },
        "role": "reference_video"
      }
    ],
    "resolution": "480p",
    "duration": 5
  }'
```

## 📋 字段说明

### 必填字段

- `model`: 模型名称（如 `doubao-seedance-2-0-260128`）
- `content`: 内容数组，至少包含一个 `type: "text"` 的对象

### content 数组元素类型

#### 1. 文本（必须至少有一个）
```json
{
  "type": "text",
  "text": "提示词内容"
}
```

#### 2. 图片
```json
{
  "type": "image_url",
  "image_url": {
    "url": "https://example.com/image.jpg"
  },
  "role": "reference_image"  // 可选：reference_image, first_frame, last_frame
}
```

#### 3. 视频
```json
{
  "type": "video_url",
  "video_url": {
    "url": "https://example.com/video.mp4"
  },
  "role": "reference_video"
}
```

#### 4. 音频
```json
{
  "type": "audio_url",
  "audio_url": {
    "url": "https://example.com/audio.mp3"
  },
  "role": "reference_audio"
}
```

### 可选参数

- `resolution`: 分辨率（`480p`, `720p`, `1080p`）
- `ratio`: 宽高比（`16:9`, `4:3`, `1:1`, `3:4`, `9:16`, `21:9`, `adaptive`）
- `duration`: 视频时长（秒），范围因模型而异
- `generate_audio`: 是否生成音频（布尔值，仅 1.5 pro 支持）
- `watermark`: 是否添加水印（布尔值）
- `seed`: 随机种子（整数）
- `camera_fixed`: 是否固定镜头（布尔值）
- `service_tier`: 服务层级（`default` 或 `flex`）

## 🎯 测试计费逻辑

### 测试场景 1: 基准价格（480p 不含视频）

**请求**：只有文本，resolution=480p

**预期**：
- ModelRatio: 3.15
- OtherRatios: 无
- 对应价格: 46元/百万token

### 测试场景 2: 视频输入折扣（480p + 含视频）

**请求**：文本 + video_url，resolution=480p

**预期**：
- ModelRatio: 3.15
- OtherRatios: `{"video_input": 0.609}`
- 对应价格: 28元/百万token

### 测试场景 3: 分辨率加价（1080p 不含视频）

**请求**：只有文本，resolution=1080p

**预期**：
- ModelRatio: 3.15
- OtherRatios: `{"resolution": 1.109}`
- 对应价格: 51元/百万token

### 测试场景 4: 倍率叠加（1080p + 含视频）

**请求**：文本 + video_url，resolution=1080p

**预期**：
- ModelRatio: 3.15
- OtherRatios: `{"resolution": 1.109, "video_input": 0.609}`
- 对应价格: 31元/百万token

## ✅ 验证响应

### 成功响应（任务创建）
```json
{
  "id": "cgt-2025xxxx-xxxxx"
}
```

### 查询任务状态
```bash
curl -X GET "http://localhost:3000/v1/video/generations/cgt-2025xxxx-xxxxx" \
  -H "Authorization: Bearer YOUR_API_KEY"
```

### 成功响应（任务完成）
```json
{
  "id": "cgt-2025xxxx-xxxxx",
  "model": "doubao-seedance-2-0-260128",
  "status": "succeeded",
  "content": {
    "video_url": "https://..."
  },
  "usage": {
    "completion_tokens": 1000,
    "total_tokens": 1000
  },
  "resolution": "480p",
  "duration": 5,
  "ratio": "16:9"
}
```

## ❌ 常见错误

### 错误 1: prompt is required
**原因**: 使用了旧版代码，未更新到支持 content 数组的版本
**解决**: 更新代码到最新版本

### 错误 2: content is required
**原因**: 请求体中没有 content 字段
**解决**: 添加 content 数组

### 错误 3: content must contain at least one text item
**原因**: content 数组中没有 text 类型的元素
**解决**: 至少添加一个 `{"type": "text", "text": "..."}`

### 错误 4: model is required
**原因**: 请求体中没有 model 字段
**解决**: 添加 model 字段

## 📊 查看计费日志

```bash
# 查看最近的计费记录
tail -f /var/log/app.log | grep other_ratios

# 预期输出示例（480p + 含视频）
{
  "model": "doubao-seedance-2-0-260128",
  "billing_context": {
    "model_ratio": 3.15,
    "other_ratios": {
      "video_input": 0.609
    }
  },
  "tokens": 1000,
  "quota": 1920  // 1000 × 3.15 × 0.609 ≈ 1920
}
```

## 🎓 总结

1. ✅ **必须使用 content 数组格式**，不要使用 prompt 字段
2. ✅ **content 中至少要有一个 text 类型**的元素
3. ✅ **通过 resolution 参数控制分辨率计费**
4. ✅ **通过添加 video_url 触发视频输入折扣**
5. ✅ **通过 generate_audio 控制音频计费（仅 1.5 pro）**
6. ✅ **多个倍率会自动叠加**（resolution × video_input）

测试愉快！🚀
