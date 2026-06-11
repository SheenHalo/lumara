# Lumara

Lumara 是一个用于下载、管理和取回 NASA SDO 太阳图片的前后端分离项目。

项目的核心流程是：先让服务器高速下载图片，再通过网页按任务查看图片、生成压缩包，并从服务器取回本地。这样可以绕开本地网络慢、长时间直连 NASA 不稳定的问题。

## 功能

- 创建服务器下载任务：选择日期范围、分辨率、图片类型、数量限制和并发数。
- 查看下载任务：任务状态、进度、下载数量、跳过数量、失败数量。
- 删除已完成任务记录：只删除任务记录，不删除服务器上的图片文件。
- 按任务查看图片：默认缩略图，点击查看原图。
- 图片区域独立滚动：页面整体固定，只有图片区域滚动。
- 分页查看图片：避免一次性渲染大量图片。
- 下载已选图片：把当前选择的图片打成 ZIP。
- 下载整个任务目录：服务器先异步压缩并显示进度，完成后浏览器下载 ZIP。
- 图片类型展示页：纯前端介绍各 SDO 图片类型，并展示对应的 512 示例图。
- 断点续传：图片下载使用 `.part` 临时文件。
- 已有文件复用：同一图片已存在时可以跳过，必要时可校验远端大小。

## 技术栈

- 后端：Go，标准库 HTTP 服务。
- 前端：Vue 3 + Vite。
- 图片来源：NASA SDO browse archive。
- 部署方式：Go 单二进制同时提供 API 和前端静态资源。

## 目录结构

```text
.
├── cmd
│   ├── lumara-sdo
│   │   └── main.go              # Go 命令行下载器
│   └── lumara-web
│       └── main.go              # Go Web API 和静态资源服务
├── docs
│   ├── backend-api.md           # 后端接口文档
│   └── deployment.md            # 线上部署和反向代理文档
├── frontend
│   ├── index.html
│   ├── package.json
│   ├── vite.config.js
│   └── src
│       ├── App.vue
│       ├── main.js
│       └── styles.css
├── go.mod
└── README.md
```

## 数据目录

Web 服务通过 `--output` 指定图片保存目录，默认是 `downloads`。

图片路径格式：

```text
downloads/2026/05/17/512/0304/20260517_000854_512_0304.jpg
```

内部文件：

```text
downloads/.lumara-jobs.json       # 下载任务记录
downloads/.thumbnails/            # 缩略图缓存
downloads/.archives/              # 服务器端生成的 ZIP 包
*.jpg.part                        # 未完成的断点续传文件
*.jpg.bad                         # 校验异常后保留的旧文件
```

删除任务记录不会删除图片、缩略图或压缩包文件。

## 图片类型

前端顶部提供“图片类型”页面，这是一个纯前端展示页，不依赖后端接口。页面会展示每个类型的编号、英文名、中文说明和对应示例图，示例图使用 NASA SDO 最新公开 512 分辨率图片。

| 类型 | 英文名 | 说明 |
| --- | --- | --- |
| `0304` | Chromosphere | 色球层，常用来观察日珥、耀斑和太阳边缘活动 |
| `0171` | Quiet Corona | 宁静日冕，适合观察较安静的日冕环结构 |
| `0193` | Corona | 日冕，适合观察日冕洞、热等离子体和较大尺度结构 |
| `0211` | Active Regions | 活跃区，适合观察高温活跃区和磁场活动区域 |
| `0094` | Hot Flares | 高温耀斑，适合观察非常热的耀斑区域 |
| `0131` | Flare Plasma | 耀斑等离子体，适合观察强耀斑和极高温等离子体 |
| `0335` | Active Corona | 活跃日冕，适合观察活跃日冕区域和环状结构 |
| `1600` | Transition | 过渡区，观察光球层到日冕之间的过渡区域 |
| `1700` | Surface | 太阳表面，接近可见光表面 |
| `HMIIF` | Sunspots | 太阳黑子，白光强度图 |
| `HMIBC` | Magnetic Field | 磁场图，显示太阳表面磁场正负极分布 |

支持分辨率：

```text
512, 1024, 2048, 4096
```

## 本地开发

安装前端依赖：

```powershell
cd frontend
npm install
```

启动前端开发服务：

```powershell
npm run dev
```

另开一个终端启动后端：

```powershell
cd ..
go run .\cmd\lumara-web --addr 127.0.0.1:8090 --output downloads
```

开发服务默认代理 `/api` 到 `http://127.0.0.1:8090`。

访问：

```text
http://127.0.0.1:5173
```

## 本地生产运行

构建前端：

```powershell
cd frontend
npm install
npm run build
cd ..
```

构建并运行 Web 服务：

```powershell
go build -o build\lumara-web.exe .\cmd\lumara-web
.\build\lumara-web.exe --addr 127.0.0.1:8090 --output downloads --static-dir frontend\dist
```

访问：

```text
http://127.0.0.1:8090
```

## Linux 部署

交叉编译后端：

```powershell
$env:GOOS='linux'
$env:GOARCH='amd64'
$env:CGO_ENABLED='0'
go build -trimpath -ldflags='-s -w' -o build\lumara-web-linux-amd64 .\cmd\lumara-web
```

构建前端：

```powershell
cd frontend
npm install
npm run build
cd ..
```

服务器目录示例：

```text
/opt/lumara/
├── data/sdo                 # 图片输出目录
├── releases/current         # 当前发布版本
└── logs
```

systemd 服务示例：

```ini
[Unit]
Description=Lumara Web API and Frontend
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/opt/lumara
ExecStart=/opt/lumara/releases/current/lumara-web --addr 0.0.0.0:8090 --output /opt/lumara/data/sdo --static-dir /opt/lumara/releases/current/web
Restart=always
RestartSec=3
Environment=TZ=Asia/Shanghai

[Install]
WantedBy=multi-user.target
```

线上域名、Cloudflare DNS、Nginx 反向代理和 TLS 证书配置见 [docs/deployment.md](docs/deployment.md)。

## Go 命令行下载器

`cmd/lumara-sdo` 仍保留为命令行工具，适合无前端场景。

构建：

```powershell
go build -o build\lumara-sdo.exe .\cmd\lumara-sdo
```

示例：

```powershell
.\build\lumara-sdo.exe 2026-05-17 --resolutions 512 --channels 0304 --limit 3
.\build\lumara-sdo.exe 2026-05 --resolutions 512 --channels 0304 --dry-run
.\build\lumara-sdo.exe --from 2026-01-01 --to 2026-05-19 --all --estimate
```

## 后端接口

完整接口文档见 [docs/backend-api.md](docs/backend-api.md)。

常用接口：

```text
GET    /api/health
GET    /api/options
POST   /api/downloads
GET    /api/downloads
GET    /api/downloads/{id}
DELETE /api/downloads/{id}
POST   /api/downloads/{id}/cancel
GET    /api/downloads/{id}/images
POST   /api/downloads/{id}/archive-jobs
GET    /api/archive-jobs/{id}
GET    /api/archive-jobs/{id}/file
POST   /api/archive
GET    /api/file?path=...
GET    /api/thumb?path=...
```

创建 512 测试任务：

```powershell
$body = @{
  from = '2026-05-17'
  to = '2026-05-17'
  resolutions = @('512')
  channels = @('0304')
  limit = 1
  workers = 2
  verifyExisting = $true
} | ConvertTo-Json

Invoke-RestMethod -Method Post -Uri 'http://127.0.0.1:8090/api/downloads' -ContentType 'application/json' -Body $body
```

## 测试

后端编译测试：

```powershell
go test ./...
```

前端构建测试：

```powershell
cd frontend
npm run build
```

## 注意事项

- 当前服务没有登录鉴权。如果部署到公网，建议放到内网、加反向代理鉴权，或配置防火墙白名单。
- 大范围下载会消耗大量磁盘空间，建议先用小范围和 `limit` 测试。
- 大任务 ZIP 采用服务器端异步压缩，压缩完成后再下载，前端会显示压缩进度。
- `.archives` 中的压缩包不会自动清理，长期运行时可定期清理旧 ZIP。
