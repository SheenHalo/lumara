# Lumara 线上部署文档

本文档记录当前公网部署方案。Lumara Web 服务仍然监听应用端口 `8090`，公网入口由 Cloudflare 和服务器上的 Nginx 反向代理提供。

## 当前线上信息

| 项目 | 值 |
| --- | --- |
| 公网地址 | `https://lumara.aback.fun/` |
| 源站服务器 | `154.50.110.149` |
| 应用服务 | `lumara-web` |
| 应用监听 | `127.0.0.1:8090` 或 `0.0.0.0:8090` |
| 反向代理 | Nginx |
| 源站系统 | Debian 12 |
| TLS 证书 | Let's Encrypt |

## Cloudflare DNS

`aback.fun` 托管在 Cloudflare，`lumara` 子域使用橙云代理：

```text
Type: A
Name: lumara
Content: 154.50.110.149
Proxy status: Proxied
TTL: Auto
```

Cloudflare SSL/TLS 模式建议使用 `Full (strict)`。源站已经部署 Let's Encrypt 证书，可以通过严格校验。

不要把 `lumara` 记录指向 Cloudflare 的边缘 IP。DNS 面板中公网解析结果显示 Cloudflare IP 是正常的，但记录内容应当是源站 IP `154.50.110.149`。

## 服务器反向代理

Nginx 站点配置路径：

```text
/etc/nginx/sites-available/lumara.aback.fun
/etc/nginx/sites-enabled/lumara.aback.fun -> /etc/nginx/sites-available/lumara.aback.fun
```

当前反向代理关系：

```text
80  -> 301 redirect to https://lumara.aback.fun/
443 -> proxy_pass http://127.0.0.1:8090
```

核心 Nginx 配置片段：

```nginx
server {
    listen 80;
    listen [::]:80;
    server_name lumara.aback.fun;

    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name lumara.aback.fun;

    ssl_certificate /etc/letsencrypt/live/lumara.aback.fun/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/lumara.aback.fun/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8090;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
        client_max_body_size 100m;
    }
}
```

修改 Nginx 配置后验证并重载：

```bash
nginx -t
systemctl reload nginx
```

## TLS 证书

服务器使用 Certbot 申请和续期 Let's Encrypt 证书：

```bash
apt-get update
apt-get install -y certbot python3-certbot-nginx
certbot --nginx -d lumara.aback.fun --non-interactive --agree-tos --register-unsafely-without-email --no-eff-email --redirect
```

证书路径：

```text
/etc/letsencrypt/live/lumara.aback.fun/fullchain.pem
/etc/letsencrypt/live/lumara.aback.fun/privkey.pem
```

查看证书有效期：

```bash
openssl x509 -in /etc/letsencrypt/live/lumara.aback.fun/fullchain.pem -noout -subject -issuer -dates
```

查看自动续期定时器：

```bash
systemctl list-timers --all | grep certbot
```

手动验证续期流程：

```bash
certbot renew --dry-run
```

## 部署应用服务

构建产物仍按 README 的 Linux 部署流程生成。服务器上建议将 Web 服务作为 systemd 服务运行，并让服务监听 `8090`：

```ini
ExecStart=/opt/lumara/releases/current/lumara-web --addr 127.0.0.1:8090 --output /opt/lumara/data/sdo --static-dir /opt/lumara/releases/current/web
```

如果需要从服务器外部直接调试应用端口，也可以临时监听 `0.0.0.0:8090`，但长期运行建议只监听 `127.0.0.1:8090`，由 Nginx 对外暴露 80/443。

## 验证命令

源站本机验证：

```bash
curl -I -H 'Host: lumara.aback.fun' http://127.0.0.1/
curl -I --resolve lumara.aback.fun:443:127.0.0.1 https://lumara.aback.fun/
```

公网验证：

```bash
curl -I http://lumara.aback.fun/
curl -I https://lumara.aback.fun/
```

预期结果：

```text
http://lumara.aback.fun/  -> 301 Moved Permanently
https://lumara.aback.fun/ -> 200 OK
```

查看 Nginx 日志：

```bash
tail -f /var/log/nginx/lumara.aback.fun.access.log
tail -f /var/log/nginx/lumara.aback.fun.ssl.access.log
tail -f /var/log/nginx/lumara.aback.fun.error.log
tail -f /var/log/nginx/lumara.aback.fun.ssl.error.log
```

## 常见问题

### Cloudflare 526

`526 Invalid SSL certificate` 表示 Cloudflare 不信任源站证书。确认 Nginx 使用的是 Let's Encrypt 证书，并检查 Cloudflare SSL/TLS 模式是否为 `Full (strict)`。

```bash
openssl x509 -in /etc/letsencrypt/live/lumara.aback.fun/fullchain.pem -noout -issuer -dates
nginx -t
systemctl reload nginx
```

### HTTP 正常但 HTTPS 不正常

优先检查源站 443：

```bash
curl -I --resolve lumara.aback.fun:443:154.50.110.149 https://lumara.aback.fun/
```

如果源站 443 正常但 Cloudflare 仍报错，检查 Cloudflare SSL/TLS 模式、证书有效期和 Nginx 是否已重载。

### Cloudflare 522、523 或 524

这类错误通常表示 Cloudflare 到源站连接失败或超时。检查：

- DNS 记录内容是否为 `154.50.110.149`。
- 服务器安全组、防火墙是否允许 80 和 443。
- Nginx 是否在监听 80 和 443。
- `lumara-web` 是否在监听 `8090`。

```bash
ss -ltnp | grep -E ':(80|443|8090)\s'
systemctl status nginx
curl -I http://127.0.0.1:8090/
```
