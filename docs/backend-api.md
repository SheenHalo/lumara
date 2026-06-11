# Lumara 后端接口文档

本文档描述 `cmd/lumara-web` 提供的 HTTP API。所有接口默认以同源 `/api` 开头，响应 JSON 使用 UTF-8。公网域名下的完整调用示例见 [api-calls.md](api-calls.md)。

## 基本信息

默认本地地址：

```text
http://127.0.0.1:8090
```

错误响应格式：

```json
{
  "error": "错误信息"
}
```

常见状态码：

| 状态码 | 含义 |
| --- | --- |
| `200` | 请求成功 |
| `202` | 任务已创建，正在后台处理 |
| `204` | CORS 预检成功 |
| `400` | 请求参数错误 |
| `404` | 资源不存在 |
| `409` | 当前状态不允许操作 |
| `405` | HTTP 方法不允许 |

## 数据模型

### DownloadJob

```json
{
  "id": "de78ec911e500cd4",
  "status": "done",
  "request": {
    "from": "2026-05-19",
    "to": "2026-05-19",
    "resolutions": ["512"],
    "channels": ["0304"],
    "limit": 1,
    "workers": 2,
    "verifyExisting": true
  },
  "createdAt": "2026-05-22T14:00:00+08:00",
  "startedAt": "2026-05-22T14:00:01+08:00",
  "finishedAt": "2026-05-22T14:00:06+08:00",
  "cleanedAt": "2026-05-23T02:00:06+08:00",
  "found": 1,
  "bytes": 56516,
  "downloaded": 1,
  "skipped": 0,
  "failed": 0,
  "current": "2026/05/19/512/0304/20260519_000830_512_0304.jpg",
  "message": "下载完成",
  "logs": []
}
```

任务状态：

| 状态 | 说明 |
| --- | --- |
| `queued` | 已创建，等待运行 |
| `running` | 正在读取目录或下载 |
| `done` | 下载结束 |
| `failed` | 部分或全部失败 |
| `canceled` | 已取消 |

任务结束 12 小时后，后端会自动删除任务对应图片和缩略图缓存。清理完成后，任务对象会包含 `cleanedAt`，并在 `logs` 中记录清理结果。任务记录本身不会被删除。

### ImageInfo

```json
{
  "path": "2026/05/19/512/0304/20260519_000830_512_0304.jpg",
  "filename": "20260519_000830_512_0304.jpg",
  "date": "2026-05-19",
  "time": "00:08:30",
  "resolution": "512",
  "channel": "0304",
  "channelName": "色球层",
  "size": 55216,
  "modifiedAt": "2026-05-22T14:00:06+08:00",
  "thumbnailUrl": "/api/thumb?path=2026%2F05%2F19%2F512%2F0304%2F20260519_000830_512_0304.jpg",
  "originalUrl": "/api/file?path=2026%2F05%2F19%2F512%2F0304%2F20260519_000830_512_0304.jpg"
}
```

### ArchiveJob

```json
{
  "id": "2062af574a998fc6",
  "sourceJobId": "de78ec911e500cd4",
  "status": "running",
  "name": "lumara-2026-05-19_512_0304.zip",
  "createdAt": "2026-05-22T14:03:00+08:00",
  "finishedAt": "0001-01-01T00:00:00Z",
  "total": 144,
  "done": 38,
  "bytes": 0,
  "message": "正在压缩 38/144",
  "downloadUrl": "/api/archive-jobs/2062af574a998fc6/file"
}
```

压缩任务状态：

| 状态 | 说明 |
| --- | --- |
| `queued` | 等待压缩 |
| `running` | 正在创建 ZIP |
| `done` | ZIP 已生成 |
| `failed` | 压缩失败 |

## 健康检查

### `GET /api/health`

检查服务是否在线。

响应：

```json
{
  "status": "ok"
}
```

## 选项

### `GET /api/options`

返回前端可选的分辨率和图片类型。

响应：

```json
{
  "resolutions": ["512", "1024", "2048", "4096"],
  "channels": [
    {
      "id": "0304",
      "name": "Chromosphere",
      "title": "色球层",
      "description": "常用来观察日珥、耀斑和太阳边缘活动"
    }
  ]
}
```

## 下载任务

### `POST /api/downloads`

创建下载任务。任务会在服务器后台运行。

请求：

```json
{
  "from": "2026-05-19",
  "to": "2026-05-19",
  "resolutions": ["512"],
  "channels": ["0304"],
  "limit": 1,
  "workers": 2,
  "verifyExisting": true
}
```

字段说明：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `from` | string | 是 | 开始日期，格式 `YYYY-MM-DD` |
| `to` | string | 否 | 结束日期，格式 `YYYY-MM-DD`，为空时等于 `from` |
| `resolutions` | string[] | 否 | 分辨率，默认 `["512"]` |
| `channels` | string[] | 否 | 图片类型，默认 `["0304"]` |
| `limit` | number | 否 | 最多处理多少张，`0` 表示不限制 |
| `workers` | number | 否 | 并发下载数，默认 `4`，最大 `16` |
| `verifyExisting` | boolean | 否 | 跳过已有文件前是否检查远端大小 |

响应：`202 Accepted`

```json
{
  "id": "de78ec911e500cd4",
  "status": "queued",
  "request": {
    "from": "2026-05-19",
    "to": "2026-05-19",
    "resolutions": ["512"],
    "channels": ["0304"],
    "limit": 1,
    "workers": 2,
    "verifyExisting": true
  },
  "found": 0,
  "downloaded": 0,
  "skipped": 0,
  "failed": 0,
  "message": ""
}
```

### `GET /api/downloads`

获取下载任务列表，按创建时间倒序。

响应：

```json
{
  "items": [
    {
      "id": "de78ec911e500cd4",
      "status": "done",
      "found": 1,
      "downloaded": 1,
      "skipped": 0,
      "failed": 0,
      "message": "下载完成"
    }
  ]
}
```

### `GET /api/downloads/{id}`

获取单个下载任务详情。

响应：`DownloadJob`

### `DELETE /api/downloads/{id}`

删除下载任务记录。

限制：

- 只能删除已结束任务。
- `queued` 和 `running` 状态不能删除。
- 只删除任务记录，不删除图片文件。

响应：

```json
{
  "status": "deleted"
}
```

任务正在运行时响应 `409`：

```json
{
  "error": "下载中的任务不能删除"
}
```

### `POST /api/downloads/{id}/cancel`

取消下载任务。

响应：`DownloadJob`

### `GET /api/downloads/{id}/images`

按任务分页获取图片。

查询参数：

| 参数 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `limit` | number | `24` | 每页数量，最大 `1000` |
| `offset` | number | `0` | 偏移量 |

示例：

```text
GET /api/downloads/de78ec911e500cd4/images?limit=12&offset=0
```

响应：

```json
{
  "items": [],
  "total": 144,
  "limit": 12,
  "offset": 0
}
```

## 压缩任务

### `POST /api/downloads/{id}/archive-jobs`

为某个下载任务创建服务器端 ZIP 压缩任务。

响应：`202 Accepted`

```json
{
  "id": "2062af574a998fc6",
  "sourceJobId": "de78ec911e500cd4",
  "status": "queued",
  "name": "lumara-2026-05-19_512_0304.zip",
  "total": 144,
  "done": 0,
  "bytes": 0,
  "message": "等待压缩",
  "downloadUrl": "/api/archive-jobs/2062af574a998fc6/file"
}
```

### `GET /api/archive-jobs/{id}`

查询压缩进度。

响应：`ArchiveJob`

前端可使用：

```text
percent = done / total * 100
```

### `GET /api/archive-jobs/{id}/file`

下载已完成的 ZIP 文件。

要求：

- 压缩任务状态必须是 `done`。
- 状态不是 `done` 时返回 `409`。

响应：

```text
Content-Type: application/zip
Content-Disposition: attachment; filename="lumara-2026-05-19_512_0304.zip"
```

## 图片文件

### `GET /api/file?path={path}`

下载或查看原图。

示例：

```text
GET /api/file?path=2026%2F05%2F19%2F512%2F0304%2F20260519_000830_512_0304.jpg
```

说明：

- `path` 必须是相对于输出目录的路径。
- 后端会阻止绝对路径和 `..` 越界路径。

### `GET /api/thumb?path={path}`

获取缩略图。

行为：

- 第一次请求会在 `.thumbnails` 中生成缩略图。
- 缩略图生成失败时会回退返回原图。

## 全局图片查询

### `GET /api/images`

按条件分页查询服务器输出目录中的全部图片。

查询参数：

| 参数 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `from` | string | 空 | 开始日期，格式 `YYYY-MM-DD` |
| `to` | string | 空 | 结束日期，格式 `YYYY-MM-DD` |
| `resolution` | string | 空 | 分辨率 |
| `channel` | string | 空 | 图片类型 |
| `limit` | number | `24` | 每页数量，最大 `1000` |
| `offset` | number | `0` | 偏移量 |

示例：

```text
GET /api/images?from=2026-05-19&to=2026-05-19&resolution=512&channel=0304&limit=12&offset=0
```

响应：

```json
{
  "items": [],
  "total": 144,
  "limit": 12,
  "offset": 0
}
```

## 同步 ZIP 下载

### `POST /api/archive`

把指定图片路径或筛选结果同步打包成 ZIP 并直接返回。

这个接口适合少量图片。大量图片建议使用：

```text
POST /api/downloads/{id}/archive-jobs
```

按路径打包：

```json
{
  "paths": [
    "2026/05/19/512/0304/20260519_000830_512_0304.jpg"
  ],
  "name": "lumara-selected.zip"
}
```

按条件打包：

```json
{
  "from": "2026-05-19",
  "to": "2026-05-19",
  "resolution": "512",
  "channel": "0304",
  "name": "lumara-filtered.zip"
}
```

响应：

```text
Content-Type: application/zip
Content-Disposition: attachment; filename="lumara-selected.zip"
```

## CORS

后端会为 API 响应添加：

```text
Access-Control-Allow-Origin: *
Access-Control-Allow-Methods: GET,POST,DELETE,OPTIONS
Access-Control-Allow-Headers: Content-Type
```

生产部署默认同源访问；如果前后端跨域分开部署，也可以直接调用当前 API 方法。
