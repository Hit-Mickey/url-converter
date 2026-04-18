from flask import Flask, request, render_template_string, Response
import urllib.request
import urllib.parse
import urllib.error
import base64
import json
import yaml
import os
import ssl

app = Flask(__name__)

# --- 授权验证逻辑 ---
AUTH_USERNAME = os.environ.get('CONVERTER_USER')
AUTH_PASSWORD = os.environ.get('CONVERTER_PASS')

def is_auth_enabled():
    return bool(AUTH_USERNAME) or bool(AUTH_PASSWORD)

def check_auth(username, password):
    if not is_auth_enabled(): return True
    return username == AUTH_USERNAME and password == AUTH_PASSWORD

def authenticate():
    return Response('认证失败。请输入正确的账号密码访问转换器。', 401, {'WWW-Authenticate': 'Basic realm="Login Required"'})

def requires_auth(f):
    from functools import wraps
    @wraps(f)
    def decorated(*args, **kwargs):
        if is_auth_enabled():
            auth = request.authorization
            if not auth or not check_auth(auth.username, auth.password):
                return authenticate()
        return f(*args, **kwargs)
    return decorated

# --- HTML 前端模板 (保留了你的专属样式和功能按钮) ---
HTML_TEMPLATE = """
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>订阅转换器</title>
    <style>
        body { font-family: system-ui, -apple-system, sans-serif; background: #f4f4f5; padding: 20px; color: #3f3f46; }
        .container { max-width: 900px; margin: 0 auto; background: white; padding: 24px; border-radius: 12px; box-shadow: 0 4px 6px rgba(0,0,0,0.05); }
        .header { margin-bottom: 20px; text-align: center; } 
        h2 { margin: 0; color: #18181b; font-size: 1.5rem; }
        
        /* 严格保留的 Label 样式 */
        label { display: block; margin-bottom: 8px; font-weight: 600; font-size: 0.95rem; color: #71717a; }
        
        textarea { width: 100%; height: 250px; padding: 12px; border: 1px solid #e4e4e7; border-radius: 8px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; resize: vertical; box-sizing: border-box; font-size: 14px; outline: none; line-height: 1.5; white-space: pre; overflow-wrap: normal; overflow-x: scroll; }
        textarea:focus { border-color: #3b82f6; ring: 2px #3b82f6; }
        
        .btn-group { display: flex; gap: 12px; margin: 15px 0; justify-content: center; flex-wrap: wrap; } 
        button { color: white; border: none; padding: 10px 24px; border-radius: 6px; cursor: pointer; font-size: 15px; font-weight: 500; transition: filter 0.2s; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        button:hover { filter: brightness(1.1); }
        
        /* 为三个核心按钮分配不同颜色以便区分 */
        .btn-convert { background: #64748b; }       /* 灰色：基础转换 */
        .btn-merge { background: #10b981; }         /* 绿色：安全合并 */
        .btn-all { background: #3b82f6; }           /* 蓝色：全能转换并合并 */
        .btn-copy { background: #f4f4f5; color: #3f3f46; border: 1px solid #e4e4e7; box-shadow: none; }
        .btn-copy:hover { background: #e4e4e7; filter: none; }
        
        .footer { margin-top: 24px; font-size: 13px; color: #a1a1aa; text-align: center; border-top: 1px solid #f4f4f5; padding-top: 16px; font-weight: 500;}
        .alert { padding: 12px; border-radius: 6px; margin-bottom: 15px; font-size: 14px; background: #fee2e2; color: #b91c1c; border: 1px solid #fecaca; display: {% if error %}block{% else %}none{% endif %}; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h2>订阅转换器</h2>
        </div>
        
        <div class="alert">{{ error }}</div>
        
        <form method="POST" autocomplete="off">
            <label>粘贴链接（一行一个）：</label>
            <textarea name="links" autocomplete="off" placeholder="支持输入：&#10;1. 订阅链接 (http/https)&#10;2. 节点链接 (tuic://, vless://, ss:// 等)">{% if request.method == 'POST' %}{{ request.form.get('links', '') }}{% endif %}</textarea>
            
            <div class="btn-group">
                <button type="submit" name="action" value="convert" class="btn-convert">只转换节点</button>
                <button type="submit" name="action" value="merge" class="btn-merge">仅合并订阅 (严格验证)</button>
                <button type="submit" name="action" value="convert_merge" class="btn-all">转换并合并 (全能)</button>
            </div>
        </form>

        {% if result %}
            <label>生成结果：</label>
            <textarea readonly id="res" autocomplete="off">{{ result }}</textarea>
            <div class="btn-group">
                <button type="button" class="btn-copy" onclick="const el = document.getElementById('res'); el.select(); document.execCommand('copy'); this.innerText='复制成功';">复制结果</button>
            </div>
        {% endif %}
    </div>
    
    <div class="footer">谦谦出品</div>

    <script>
        if (window.history.replaceState) {
            window.history.replaceState(null, null, window.location.href);
        }
    </script>
</body>
</html>
"""

# --- 核心解析逻辑 ---

def decode_base64_safe(s):
    """安全解密 Base64 字符串"""
    try:
        s = s.strip()
        s += "=" * ((4 - len(s) % 4) % 4)
        return base64.urlsafe_b64decode(s).decode('utf-8')
    except:
        return ""

def process_url_or_link(line):
    """
    智能分析单行文本，返回元组: (数据类型, 数据内容)
    数据类型: 'clash' (YAML 节点列表), 'raw' (单链接列表), 'error' (错误信息)
    """
    line = line.strip()
    if not line: return "empty", None
    
    # 1. 如果是远程订阅链接
    if line.startswith(('http://', 'https://')):
        try:
            # 伪装成 ClashMeta 获取最优配置格式，忽略 SSL 证书错误
            ctx = ssl.create_default_context()
            ctx.check_hostname = False
            ctx.verify_mode = ssl.CERT_NONE
            req = urllib.request.Request(line, headers={'User-Agent': 'ClashMeta/1.18.0'})
            
            with urllib.request.urlopen(req, timeout=10, context=ctx) as r:
                content = r.read().decode('utf-8')
            
            # 尝试 1: 检查是否为 Clash YAML
            try:
                data = yaml.safe_load(content)
                if isinstance(data, dict) and 'proxies' in data and isinstance(data['proxies'], list):
                    return "clash", data['proxies']
            except:
                pass
            
            # 尝试 2: 检查是否为 Base64 编码的单链接集合
            decoded = decode_base64_safe(content)
            links = [l.strip() for l in decoded.split('\n') if l.strip()]
            if links and any(l.startswith(('vmess://', 'vless://', 'tuic://', 'trojan://', 'hy2://', 'ss://')) for l in links):
                return "raw", links
                
            return "error", f"无法识别订阅格式：{line[:30]}..."
        except Exception as e:
            return "error", f"链接拉取失败 ({str(e)}): {line[:30]}..."
            
    # 2. 如果是原生节点链接
    else:
        return "raw", [line]

def parse_link(link):
    """将单链接转化为 Clash YAML 字典"""
    link = link.strip()
    if not link: return None
    try:
        if link.startswith('vmess://'):
            v = json.loads(decode_base64_safe(link[8:]))
            cfg = {"name": v.get("ps", f"VMess-{v.get('add')}"), "type": "vmess", "server": v.get("add"), "port": int(v.get("port")), "uuid": v.get("id"), "alterId": int(v.get("aid", 0)), "cipher": v.get("scy", "auto")}
            if v.get("net") == "ws":
                cfg["network"] = "ws"
                cfg["ws-opts"] = {"path": v.get("path", "/")}
                if v.get("host"): cfg["ws-opts"]["headers"] = {"Host": v.get("host")}
            if v.get("tls") == "tls":
                cfg["tls"] = True
                if v.get("sni"): cfg["servername"] = v.get("sni")
            return cfg
            
        u = urllib.parse.urlparse(link)
        qs = dict(urllib.parse.parse_qsl(u.query))
        name = urllib.parse.unquote(u.fragment) if u.fragment else f"{u.scheme.upper()}-{u.hostname}"

        if u.scheme == 'tuic':
            cfg = {"name": name, "type": "tuic", "server": u.hostname, "port": u.port, "uuid": u.username, "password": u.password}
            if 'sni' in qs: cfg['sni'] = qs['sni']
            if 'alpn' in qs: cfg['alpn'] = qs['alpn'].split(',')
            if 'congestion_control' in qs: cfg['congestion-controller'] = qs['congestion_control']
            if qs.get('insecure') == '1': cfg['skip-cert-verify'] = True
            return cfg
        elif u.scheme in ('hysteria2', 'hy2'):
            cfg = {"name": name, "type": "hysteria2", "server": u.hostname, "port": u.port, "password": u.username or u.password}
            if 'sni' in qs: cfg['sni'] = qs['sni']
            if qs.get('insecure') == '1': cfg['skip-cert-verify'] = True
            if 'obfs' in qs: cfg['obfs'] = qs['obfs']
            if 'obfs-password' in qs: cfg['obfs-password'] = qs['obfs-password']
            if 'up' in qs: cfg['up'] = qs['up']
            if 'down' in qs: cfg['down'] = qs['down']
            return cfg
        elif u.scheme == 'vless':
            cfg = {"name": name, "type": "vless", "server": u.hostname, "port": u.port, "uuid": u.username}
            if 'flow' in qs: cfg['flow'] = qs['flow']
            if 'type' in qs: cfg['network'] = qs['type']
            if cfg.get('network') == 'ws':
                cfg['ws-opts'] = {}
                if 'path' in qs: cfg['ws-opts']['path'] = urllib.parse.unquote(qs['path'])
                if 'host' in qs: cfg['ws-opts']['headers'] = {'Host': qs['host']}
                if not cfg['ws-opts']: del cfg['ws-opts']
            elif cfg.get('network') == 'grpc':
                cfg['grpc-opts'] = {}
                if 'serviceName' in qs: cfg['grpc-opts']['service-name'] = qs['serviceName']
                if not cfg['grpc-opts']: del cfg['grpc-opts']
            sec = qs.get('security')
            if sec in ('tls', 'reality'):
                cfg['tls'] = True
                if 'sni' in qs: cfg['servername'] = qs['sni']
                if 'fp' in qs: cfg['client-fingerprint'] = qs['fp']
                if sec == 'reality':
                    cfg['reality-opts'] = {}
                    if 'pbk' in qs: cfg['reality-opts']['public-key'] = qs['pbk']
                    if 'sid' in qs: cfg['reality-opts']['short-id'] = qs['sid']
            return cfg
        elif u.scheme == 'trojan':
            cfg = {"name": name, "type": "trojan", "server": u.hostname, "port": u.port, "password": u.username or u.password}
            if 'sni' in qs: cfg['sni'] = qs['sni']
            if qs.get('allowInsecure') == '1' or qs.get('insecure') == '1': cfg['skip-cert-verify'] = True
            return cfg
        elif u.scheme == 'ss':
            cfg = {"name": name, "type": "ss"}
            try:
                # 解析 SIP002 标准及旧版 Base64
                if '@' in u.netloc:
                    user_pass_b64, server_port = u.netloc.split('@', 1)
                    user_pass = decode_base64_safe(user_pass_b64)
                    server, port = server_port.split(':', 1)
                else:
                    decoded = decode_base64_safe(u.netloc)
                    user_pass, server_port = decoded.split('@', 1)
                    server, port = server_port.split(':', 1)
                
                method, password = user_pass.split(':', 1)
                cfg.update({"server": server, "port": int(port), "cipher": method, "password": password})
                return cfg
            except:
                return None
    except Exception:
        return None

# --- 路由与请求分发 ---
@app.route('/', methods=['GET', 'POST'])
@requires_auth
def index():
    result = ""
    error = ""
    
    if request.method == 'POST':
        action = request.form.get('action')
        links_text = request.form.get('links', '')
        lines = [l.strip() for l in links_text.split('\n') if l.strip()]

        if not lines:
            error = "请输入链接内容。"
            return render_template_string(HTML_TEMPLATE, result=result, error=error)

        proxies = []

        # 模式 1：保留现在的转换功能 (仅对每一行文本直接转换)
        if action == 'convert':
            for line in lines:
                parsed = parse_link(line)
                if parsed: proxies.append(parsed)
            if not proxies: error = "未解析到有效节点，或者您输入了远程订阅链接（请使用第三个按钮）。"

        # 模式 2：订阅合并功能 (严格检查 Clash 格式)
        elif action == 'merge':
            for line in lines:
                rtype, data = process_url_or_link(line)
                if rtype == "raw":
                    error = "检测到非 Clash 格式链接（单节点链接或 Base64 订阅）。请使用【转换并合并 (全能)】功能！"
                    return render_template_string(HTML_TEMPLATE, result="", error=error)
                elif rtype == "clash":
                    proxies.extend(data)
                elif rtype == "error":
                    error = data
                    return render_template_string(HTML_TEMPLATE, result="", error=error)
            if not proxies and not error: error = "未从订阅链接中提取到有效节点。"

        # 模式 3：转换并合并 (全能处理)
        elif action == 'convert_merge':
            for line in lines:
                rtype, data = process_url_or_link(line)
                if rtype == "clash":
                    # 已经是 YAML 字典，直接合并
                    proxies.extend(data)
                elif rtype == "raw":
                    # 是单链接列表，逐个转换后合并
                    for raw_link in data:
                        parsed = parse_link(raw_link)
                        if parsed: proxies.append(parsed)
                elif rtype == "error":
                    # 遇到无效链接不中断，但在输出中可以通过其他方式提示（这里选择容错略过）
                    pass
            if not proxies: error = "提取或转换失败，未获得任何有效节点。"

        # 如果最终获得了节点，则生成 YAML
        if proxies:
            result = yaml.dump({"proxies": proxies}, allow_unicode=True, sort_keys=False, default_flow_style=False)

    return render_template_string(HTML_TEMPLATE, result=result, error=error)

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=5000)