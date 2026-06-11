# Lumara

Lumara 是一个用于下载、管理和取回 NASA SDO 太阳图片的前后端分离项目。

它的核心流程是：服务器先高速下载图片，浏览器再按任务查看图片、生成压缩包并取回本地。这样可以减少本地网络慢、长时间直连 NASA 不稳定的问题。

## 当前能力

- 创建服务器下载任务，支持日期范围、分辨率、图片类型、数量限制和并发数。
- 查看任务状态、进度、下载数量、跳过数量和失败数量。
- 按任务分页浏览图片，使用缩略图网格并可打开原图。
- 下载当前选择的图片，或异步压缩并下载整个任务目录。
- 查看 SDO 图片类型说明和 NASA 最新示例图。
- 使用 Go 单二进制同时提供 API 和前端静态资源。

完整功能说明见 [docs/features.md](docs/features.md)。

## 技术栈

- 后端：Go，标准库 HTTP 服务。
- 前端：Vue 3 + Vite。
- 图片来源：NASA SDO browse archive。

## 目录结构

```text
.
├── cmd
│   ├── lumara-sdo       # Go 命令行下载器
│   └── lumara-web       # Go Web API 和静态资源服务
├── docs
│   ├── api-calls.md     # 公网调用文档
│   ├── backend-api.md   # 后端接口文档
│   ├── deployment.md    # 线上部署和反向代理文档
│   └── features.md      # 功能文档
├── frontend             # Vue 3 前端
├── go.mod
└── README.md
```

## 本地开发

安装并启动前端：

```powershell
cd frontend
npm install
npm run dev
```

另开一个终端启动后端：

```powershell
cd ..
go run .\cmd\lumara-web --addr 127.0.0.1:8090 --output downloads
```

开发服务默认代理 `/api` 到 `http://127.0.0.1:8090`，访问：

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

## 文档

- [功能文档](docs/features.md)
- [公网调用文档](docs/api-calls.md)
- [后端接口](docs/backend-api.md)
- [线上部署](docs/deployment.md)

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
- 数据目录、图片类型、CLI 下载器和 ZIP 清理说明见 [docs/features.md](docs/features.md)。
