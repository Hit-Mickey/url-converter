# 订阅转换器 (URL Converter) 🐱

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Docker](https://img.shields.io/badge/docker-ready-green.svg)
![Python](https://img.shields.io/badge/python-3.11-blue.svg)

[中文版说明请向下滚动 / Scroll down for Chinese documentation](#中文版说明)

A lightweight, purely local, and highly privacy-focused subscription converter designed for Clash Meta (Mihomo). 

It intelligently identifies, extracts, and converts various node links (`tuic://`, `vless://`, `hy2://`, `ss://`, etc.) and remote subscription links into standard YAML configurations supported by Clash.

## ✨ Features

- **100% Local Processing:** All conversions happen locally within the container. No data is sent to third-party APIs, ensuring absolute privacy for your node configurations.
- **Smart Link Detection:** Automatically distinguishes between raw node links, Base64-encoded subscriptions, and existing YAML configurations.
- **Multi-Protocol Support:** Natively parses TUIC, Hysteria2, VLESS (including Reality), VMess, Trojan, and Shadowsocks.
- **Three Core Modes:**
  - `Convert Only`: Strictly converts node links into YAML format.
  - `Merge Only`: Safely merges existing Clash subscriptions (rejects non-standard formats).
  - `Convert & Merge (All-in-One)`: Extracts, decrypts, converts, and merges everything into a single, unified profile.
- **Optional Security:** Supports Basic Authentication via environment variables to protect your deployment.

## 🚀 Quick Start (Docker)

You can run this project instantly using Docker Compose. Create a `docker-compose.yml` file:

```yaml
services:
  url-converter:
    image: mickey666/url-converter:latest
    container_name: url-converter
    ports:
      - "11111:5000"
    restart: unless-stopped
    # Optional: Enable Basic Auth by uncommenting the lines below
    # environment:
    #   - CONVERTER_USER=your_username
    #   - CONVERTER_PASS=your_secure_password
```

Then start the service:
```bash
docker compose up -d
```
Access the web interface at `http://localhost:11111`.

## 🛠️ Development Mode

If you want to modify the code and see changes in real-time without rebuilding the image:

1. Clone this repository.
2. Ensure `app.py` and `docker-compose.yml` are in the same directory.
3. Modify your `docker-compose.yml` to mount the local directory:

```yaml
services:
  url-converter-dev:
    image: python:3.11-alpine
    container_name: url-converter-dev
    ports:
      - "11111:5000"
    volumes:
      - ./:/app
    working_dir: /app
    command: sh -c "pip install flask pyyaml --no-cache-dir && python app.py"
    restart: unless-stopped
```

Run `docker compose up -d`. Any changes made to `app.py` will take effect after running `docker compose restart url-converter-dev`.

---

<h1 id="中文版说明">订阅转换器 (URL Converter) 🐱</h1>

一个轻量、纯本地且高度注重隐私的 Clash Meta (Mihomo) 订阅转换器。

它可以智能识别、提取并将各种节点链接（如 `tuic://`, `vless://`, `hy2://`, `ss://` 等）以及远程订阅链接转换为 Clash 支持的标准 YAML 配置。

## ✨ 核心特性

- **100% 本地处理：** 所有转换都在容器内部本地完成，绝不向第三方 API 发送数据，绝对保障您的节点隐私。
- **智能链接检测：** 自动区分并解析单节点链接、Base64 编码的传统订阅以及现有的 YAML 配置。
- **多协议原生支持：** 完美支持 TUIC, Hysteria2, VLESS (包含 Reality 复杂参数), VMess, Trojan 以及 Shadowsocks。
- **三大核心模式：**
  - `只转换节点`：严格将节点链接转换为 YAML 格式。
  - `仅合并订阅`：安全地合并现有的 Clash 订阅（严格拒绝非标准格式）。
  - `转换并合并 (全能)`：智能提取、解密、转换并将所有内容完美融合为一份配置文件。
- **可选安全防护：** 支持通过环境变量开启网页基础认证 (Basic Auth)，保护您的私有部署。

## 🚀 快速开始 (Docker)

您可以使用 Docker Compose 瞬间启动该项目。只需创建一个 `docker-compose.yml` 文件：

```yaml
services:
  url-converter:
    image: mickey666/url-converter:latest
    container_name: url-converter
    ports:
      - "11111:5000"
    restart: unless-stopped
    # 可选：取消下方注释以启用账号密码登录保护
    # environment:
    #   - CONVERTER_USER=您的用户名
    #   - CONVERTER_PASS=您的安全密码
```

随后启动服务：
```bash
docker compose up -d
```
在浏览器中访问 `http://localhost:11111` 即可使用。

## 🛠️ 本地开发模式

如果您希望修改代码，并在不重新构建镜像的情况下实时查看效果：

1. 克隆本仓库。
2. 确保 `app.py` 和 `docker-compose.yml` 在同一目录下。
3. 修改您的 `docker-compose.yml` 以挂载本地目录：

```yaml

services:
  url-converter-dev:
    image: python:3.11-alpine
    container_name: url-converter-dev
    ports:
      - "11111:5000"
    volumes:
      - ./:/app
    working_dir: /app
    command: sh -c "pip install flask pyyaml --no-cache-dir && python app.py"
    restart: unless-stopped
```

运行 `docker compose up -d` 启动。此时，您对 `app.py` 做的任何修改，只需运行 `docker compose restart url-converter-dev` 即可立即生效。

## 📄 开源协议

本项目采用 MIT 开源许可协议 - 详情请查看 [LICENSE](LICENSE) 文件。