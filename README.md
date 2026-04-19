# URL Converter

A high-performance, private multi-protocol subscription converter. Supports mixed parsing of multiple configurations, L7 real-world connectivity testing, automatic node deduplication, and a local short-link system with a 7-day auto-expiry (TTL) for inactive links.

[中文版说明请向下滚动 / Scroll down for Chinese documentation](#中文版说明)

---

## ✨ Features

* **Mixed Processing**: Paste multiple subscription links, direct node links (VMess, VLESS, SS, Trojan, Hysteria2, etc.), or complete YAML config files simultaneously. The system extracts and integrates them automatically.
* **Auto-Deduplication**: Automatically identifies nodes with the same name and appends `_1`, `_2` suffixes, ensuring the generated YAML config perfectly complies with Clash/Mihomo standards and prevents duplicate name errors.
* **L7 Connectivity Test**: Utilizes the Mihomo core to perform real `gstatic.com` latency tests on nodes, physically eliminating invalid nodes to guarantee subscription quality.
* **Auto Cleanup**: Destroys `cache.db` immediately after each testing task. A background goroutine runs hourly to clean up expired short links that haven't been accessed for over **7 days**.
* **Local Short Link**: All short link data is stored locally in `shortlinks.json`, with zero reliance on third-party services, protecting your routing privacy.
* **Highly Customizable**: Freely specify the Web UI username, password, and Mihomo core version via environment variables during deployment.

---

## 🚀 Quick Deployment

We recommend using Docker Compose for one-click deployment.

### 1. Create Config File

Create a `docker-compose.yml` file and modify the environment variables as needed:

```yaml
services:
  url-converter:
    image: mickey666/url-converter:latest
    container_name: url-converter
    restart: unless-stopped
    ports:
      - "5000:5000"
    environment:
      # Web UI Username
      - CONVERTER_USER=admin
      # Web UI Password
      - CONVERTER_PASS=your_password
      # Mihomo Core Version
      - MIHOMO_VERSION=v1.19.23
    volumes:
      # Persistent data
      - ./converter_data:/app/core
    logging:
      driver: "json-file"
      options:
        max-size: "10m"
        max-file: "3"
```

### 2. Launch

```bash
docker compose up -d
```

Visit `http://your-server-ip:5000` to start using it.

---

## 📝 Environment Variables

| Variable | Description | Default |
| :--- | :--- | :--- |
| `CONVERTER_USER` | Auth Username | Leave blank to disable auth |
| `CONVERTER_PASS` | Auth Password | Leave blank to disable auth |
| `MIHOMO_VERSION` | Mihomo Release Version | `v1.19.23` |
| `PORT` | Internal Port | `5000` |

---

## ⚖️ Cleanup Policy

1. **L7 Test Cache**: `cache.db` is deleted immediately after each testing task.
2. **Short-link TTL**: A background task runs hourly. Any short-link ID that has not been accessed for **7 consecutive days** will be permanently deleted from the database.

---
---

# 中文版说明

一款高性能、私有化的多协议订阅转换工具。支持多配置混合解析、L7 真机测速、节点自动去重，以及 7 天未访问自动销毁的本地短链系统。

---

## ✨ 核心特性

* **混合处理**: 允许同时粘贴多个订阅链接、直链节点（VMess, VLESS, SS, Trojan, Hysteria2 等）或完整的 YAML 配置文件，系统会自动提取并整合。
* **智能去重**: 自动识别同名节点并添加 `_1`, `_2` 后缀，确保生成的 YAML 配置文件符合 Clash/Mihomo 标准，杜绝重名报错。
* **L7 真机测速**: 调用 Mihomo 内核对节点进行真实的 `gstatic.com` 延迟测试，物理剔除无效节点，保证订阅质量。
* **极致洁癖**: 每次测速任务完成后立即销毁 `cache.db`；后台协程每小时自动巡检，清理超过 **7 天** 未被访问的过期短链。
* **私有化短链**: 所有短链数据仅存储在本地 `shortlinks.json` 中，不依赖任何第三方服务，保护您的链路隐私。
* **高度自定义**: 部署时可通过环境变量自由指定 Web 访问账号、密码以及 Mihomo 内核版本。

---

## 🚀 快速部署

推荐使用 Docker Compose 进行一键部署。

### 1. 创建配置文件

创建 `docker-compose.yml` 并根据需要修改环境变量：

```yaml
services:
  url-converter:
    image: mickey666/url-converter:latest
    container_name: url-converter
    restart: unless-stopped
    ports:
      - "5000:5000"
    environment:
      # 网页访问账号
      - CONVERTER_USER=admin
      # 网页访问密码
      - CONVERTER_PASS=your_password
      # Mihomo 内核版本号
      - MIHOMO_VERSION=v1.19.23
    volumes:
      # 持久化短链数据和内核文件
      - ./converter_data:/app/core
    logging:
      driver: "json-file"
      options:
        max-size: "10m"
        max-file: "3"
```

### 2. 启动服务

```bash
docker compose up -d
```

访问 `http://your-server-ip:5000` 即可开始使用。

---

## 📝 环境变量说明

| 变量 | 说明 | 默认值 |
| :--- | :--- | :--- |
| `CONVERTER_USER` | 网页基础认证用户名 | 留空则不开启认证 |
| `CONVERTER_PASS` | 网页基础认证密码 | 留空则不开启认证 |
| `MIHOMO_VERSION` | 测速用的内核版本 | `v1.19.23` |
| `PORT` | 容器内运行端口 | `5000` |

---

## ⚖️ 自动清理策略

1. **节点测速缓存**: 每次点击“开始转换”并完成测速后，程序会立即删除产生的 `cache.db` 文件。
2. **短链生存期**: 系统每小时执行一次后台巡检。如果某个短链 ID 在过去 **7 天内** 从未被访问过（即没有客户端拉取），该记录将从 `shortlinks.json` 中永久移除。

---

## 👤 作者

**谦谦出品**
