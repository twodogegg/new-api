# MiniMax H3 素材上传接口

这个接口用于把图片、音频或视频直接转发到 RunningHub，并返回 RunningHub 的素材文件名。New API 不保存文件副本，也不会向客户端返回 RunningHub 的临时下载地址。

## 接口地址

```http
POST /v1/h3/assets
```

请求地址以当前 New API 部署域名为准，例如：

```text
https://api.example.com/v1/h3/assets
```

## 鉴权

使用 New API 用户 API 密钥：

```http
Authorization: Bearer <NEW_API_API_KEY>
```

RunningHub 的密钥保存在 New API 渠道配置中，客户端不需要也不应该提供 RunningHub 密钥。

## 上传文件

请求类型必须是 `multipart/form-data`，文件字段名必须是 `file`，每次只能上传一个文件。

```bash
curl -X POST "https://api.example.com/v1/h3/assets" \
  -H "Authorization: Bearer $NEW_API_API_KEY" \
  -F "file=@./first-frame.png"
```

图片首帧或尾帧建议使用 JPG、PNG、JPEG 或 WEBP。请求体和单个文件的大小受 New API 文件上传限制约束，默认上限为 64 MB。

## 成功响应

HTTP 状态码为 `200` 时，响应格式如下：

```json
{
  "id": "openapi/61432ac1b3f3f5b19edcccff137d49d24a907b32ef3a737d1958e33aa91f1f44.png",
  "fileName": "openapi/61432ac1b3f3f5b19edcccff137d49d24a907b32ef3a737d1958e33aa91f1f44.png",
  "type": "image",
  "size": "3490",
  "expires_in": 86400
}
```

字段说明：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string | 可直接作为 H3 素材引用的 RunningHub 文件名 |
| `fileName` | string | 与 `id` 相同，保留该字段以便兼容 RunningHub 工作流参数 |
| `type` | string | RunningHub 返回的素材类型 |
| `size` | string | 文件大小，单位为字节；以 RunningHub 返回值为准 |
| `expires_in` | number | 有效期秒数，当前按约 24 小时返回 |

`id`/`fileName` 是 RunningHub 的工作流文件引用，不是永久公网 URL。不要把它拼接成下载地址，也不要把 RunningHub 返回的 `download_url` 当作长期存储地址。

## 在 H3 视频请求中使用

上传完成后，把返回的 `fileName` 放入现有 H3 视频接口的素材 URL 字段。例如首帧图生视频：

```http
POST /runninghub/v2/video_generation
Authorization: Bearer <NEW_API_API_KEY>
Content-Type: application/json
```

```json
{
  "model": "minimax-h3-flash",
  "content": [
    {
      "type": "text",
      "text": "镜头缓慢推进，人物自然转身"
    },
    {
      "type": "image_url",
      "role": "first_frame",
      "image_url": {
        "url": "openapi/61432ac1b3f3f5b19edcccff137d49d24a907b32ef3a737d1958e33aa91f1f44.png"
      }
    }
  ],
  "resolution": "480P",
  "duration": 4,
  "ratio": "adaptive"
}
```

尾帧使用 `role: "last_frame"`。首尾帧请求在 `content` 中放入两个 `image_url` 项，分别标记为 `first_frame` 和 `last_frame`。

## 常见错误

| HTTP 状态码 | 含义 |
| --- | --- |
| `400` | 缺少 `file`、上传了多个文件或请求格式不正确 |
| `401` | New API 用户 API 密钥无效或缺失 |
| `403` | 当前用户或渠道没有调用权限 |
| `413` | 文件超过上传限制 |
| `502` | RunningHub 上传失败或返回了无效响应 |

错误响应使用 New API 的统一错误格式：

```json
{
  "error": {
    "type": "bad_request_error",
    "message": "file field is required",
    "http_code": "400"
  }
}
```

## 生命周期和限制

- 上传内容会直接转发到 RunningHub，New API 不提供永久文件存储。
- RunningHub 的 `fileName` 通常只在有限时间内有效，当前按约一天使用。
- 过期后需要重新上传，不能依赖旧的 `fileName`。
- 该接口是 H3/RunningHub 专用接口，返回的 `fileName` 不能作为其他供应商的通用素材 ID。
- 其他供应商可能需要公网 URL、Base64、multipart 或各自的素材上传接口，不能直接复用这个文件名。
