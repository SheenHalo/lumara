# Lumara 公网调用文档

本文档面向 `https://lumara.aback.fun/` 域名下的 API 调用。接口字段的完整定义见 [backend-api.md](backend-api.md)。

## 基本信息

公网基础地址：

```text
https://lumara.aback.fun
```

API 前缀：

```text
https://lumara.aback.fun/api
```

当前接口没有登录鉴权。所有 JSON 请求建议带上：

```text
Content-Type: application/json
```

错误响应统一为：

```json
{
  "error": "错误信息"
}
```

## 快速检查

PowerShell：

```powershell
$BaseUrl = 'https://lumara.aback.fun'
Invoke-RestMethod "$BaseUrl/api/health"
Invoke-RestMethod "$BaseUrl/api/options"
```

curl：

```bash
BASE_URL='https://lumara.aback.fun'
curl "$BASE_URL/api/health"
curl "$BASE_URL/api/options"
```

健康检查正常时返回：

```json
{
  "status": "ok"
}
```

## 推荐调用流程

典型流程：

```text
1. GET  /api/options
2. POST /api/downloads
3. GET  /api/downloads/{id}
4. GET  /api/downloads/{id}/images
5. GET  /api/file?path=...
6. POST /api/downloads/{id}/archive-jobs
7. GET  /api/archive-jobs/{archiveId}
8. GET  /api/archive-jobs/{archiveId}/file
```

## 创建下载任务

PowerShell：

```powershell
$BaseUrl = 'https://lumara.aback.fun'

$Body = @{
  from = '2026-05-17'
  to = '2026-05-17'
  resolutions = @('512')
  channels = @('0304')
  limit = 1
  workers = 2
  verifyExisting = $true
} | ConvertTo-Json

$Job = Invoke-RestMethod `
  -Method Post `
  -Uri "$BaseUrl/api/downloads" `
  -ContentType 'application/json' `
  -Body $Body

$Job
$JobId = $Job.id
```

curl：

```bash
BASE_URL='https://lumara.aback.fun'

curl -X POST "$BASE_URL/api/downloads" \
  -H 'Content-Type: application/json' \
  -d '{
    "from": "2026-05-17",
    "to": "2026-05-17",
    "resolutions": ["512"],
    "channels": ["0304"],
    "limit": 1,
    "workers": 2,
    "verifyExisting": true
  }'
```

响应状态为 `202 Accepted`，返回下载任务对象。重点字段：

| 字段 | 说明 |
| --- | --- |
| `id` | 任务 ID，后续查询和取图都要使用 |
| `status` | `queued`、`running`、`done`、`failed`、`canceled` |
| `found` | 已发现的待处理文件数量 |
| `downloaded` | 已下载数量 |
| `skipped` | 本地已有并跳过数量 |
| `failed` | 下载失败数量 |
| `message` | 当前任务消息 |
| `cleanedAt` | 图片自动清理完成时间；未清理时为空 |

## 查询任务

获取任务列表：

```powershell
Invoke-RestMethod "$BaseUrl/api/downloads"
```

获取单个任务：

```powershell
Invoke-RestMethod "$BaseUrl/api/downloads/$JobId"
```

简单轮询直到任务结束：

```powershell
do {
  Start-Sleep -Seconds 2
  $Job = Invoke-RestMethod "$BaseUrl/api/downloads/$JobId"
  "{0} downloaded={1} skipped={2} failed={3} message={4}" -f `
    $Job.status, $Job.downloaded, $Job.skipped, $Job.failed, $Job.message
} while ($Job.status -in @('queued', 'running'))
```

取消任务：

```powershell
Invoke-RestMethod -Method Post "$BaseUrl/api/downloads/$JobId/cancel"
```

删除已结束任务记录：

```powershell
Invoke-RestMethod -Method Delete "$BaseUrl/api/downloads/$JobId"
```

删除任务记录不会删除服务器上的图片文件。

## 查询任务图片

分页查询任务图片：

```powershell
$Images = Invoke-RestMethod "$BaseUrl/api/downloads/$JobId/images?limit=12&offset=0"
$Images.items | Select-Object filename, date, time, resolution, channel, path
```

响应中的 `thumbnailUrl` 和 `originalUrl` 是相对 URL。外部调用时需要拼上基础域名：

```powershell
$Image = $Images.items[0]
$OriginalUrl = "$BaseUrl$($Image.originalUrl)"
$ThumbnailUrl = "$BaseUrl$($Image.thumbnailUrl)"
```

## 下载原图

使用接口返回的 `originalUrl`：

```powershell
$Image = $Images.items[0]
Invoke-WebRequest "$BaseUrl$($Image.originalUrl)" -OutFile $Image.filename
```

也可以手动传入相对图片路径。`path` 需要 URL 编码：

```powershell
$Path = '2026/05/17/512/0304/20260517_000854_512_0304.jpg'
$EncodedPath = [uri]::EscapeDataString($Path)
Invoke-WebRequest "$BaseUrl/api/file?path=$EncodedPath" -OutFile 'sdo.jpg'
```

缩略图接口：

```powershell
Invoke-WebRequest "$BaseUrl/api/thumb?path=$EncodedPath" -OutFile 'sdo-thumb.jpg'
```

## 下载已选图片 ZIP

适合少量图片。请求体传图片相对路径数组：

```powershell
$Paths = @($Images.items[0].path)

$Body = @{
  paths = $Paths
  name = 'lumara-selected.zip'
} | ConvertTo-Json

Invoke-WebRequest `
  -Method Post `
  -Uri "$BaseUrl/api/archive" `
  -ContentType 'application/json' `
  -Body $Body `
  -OutFile 'lumara-selected.zip'
```

也可以按条件同步打包：

```powershell
$Body = @{
  from = '2026-05-17'
  to = '2026-05-17'
  resolution = '512'
  channel = '0304'
  name = 'lumara-filtered.zip'
} | ConvertTo-Json

Invoke-WebRequest `
  -Method Post `
  -Uri "$BaseUrl/api/archive" `
  -ContentType 'application/json' `
  -Body $Body `
  -OutFile 'lumara-filtered.zip'
```

## 下载整个任务目录 ZIP

大任务建议使用异步压缩接口。先创建压缩任务：

```powershell
$ArchiveJob = Invoke-RestMethod `
  -Method Post `
  -Uri "$BaseUrl/api/downloads/$JobId/archive-jobs" `
  -ContentType 'application/json' `
  -Body '{}'

$ArchiveJobId = $ArchiveJob.id
```

轮询压缩进度：

```powershell
do {
  Start-Sleep -Seconds 2
  $ArchiveJob = Invoke-RestMethod "$BaseUrl/api/archive-jobs/$ArchiveJobId"
  "{0} {1}/{2} {3}" -f $ArchiveJob.status, $ArchiveJob.done, $ArchiveJob.total, $ArchiveJob.message
} while ($ArchiveJob.status -in @('queued', 'running'))

if ($ArchiveJob.status -ne 'done') {
  throw $ArchiveJob.error
}
```

下载生成好的 ZIP：

```powershell
Invoke-WebRequest "$BaseUrl$($ArchiveJob.downloadUrl)" -OutFile $ArchiveJob.name
```

## 全局图片查询

如果不按任务查询，也可以扫描服务器输出目录中的全部图片：

```powershell
$Query = 'from=2026-05-17&to=2026-05-17&resolution=512&channel=0304&limit=12&offset=0'
Invoke-RestMethod "$BaseUrl/api/images?$Query"
```

这个接口适合做全局检索。按任务取图时，优先使用：

```text
GET /api/downloads/{id}/images
```

## 常见问题

### 路径参数怎么传

图片路径必须是相对输出目录的路径，例如：

```text
2026/05/17/512/0304/20260517_000854_512_0304.jpg
```

调用 `/api/file` 和 `/api/thumb` 时要 URL 编码。不要传绝对路径，也不要传带 `..` 的路径。

### 同步 ZIP 和异步 ZIP 怎么选

- 少量图片：使用 `POST /api/archive`。
- 整个任务目录或大量图片：使用 `POST /api/downloads/{id}/archive-jobs`，再轮询进度并下载文件。

### 为什么图片或 ZIP 可能下载失败

常见原因：

- 任务还没完成，图片尚未下载到服务器。
- 任务结束超过 12 小时后，原图和缩略图已经被自动清理。
- 图片路径写错或未 URL 编码。
- 异步压缩任务还不是 `done` 状态。
- 服务重启后，内存中的压缩任务状态会丢失；已生成的 ZIP 文件仍可能留在服务器 `.archives` 目录中。
