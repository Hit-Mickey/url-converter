from flask import Flask, request, render_template_string, Response
import urllib.parse
import base64
import json
import yaml
import os

app = Flask(__name__)

# --- 获取环境变量，如果没有设置，则为 None ---
AUTH_USERNAME = os.environ.get('CONVERTER_USER')
AUTH_PASSWORD = os.environ.get('CONVERTER_PASS')

def is_auth_enabled():
    """判断是否启用了密码验证"""
    return bool(AUTH_USERNAME) or bool(AUTH_PASSWORD)

def check_auth(username, password):
    """验证账号密码是否正确"""
    if not is_auth_enabled():
        return True
    return username == AUTH_USERNAME and password == AUTH_PASSWORD

def authenticate():
    """向浏览器发送 401 认证请求"""
    return Response(
        '认证失败。请输入正确的账号密码访问转换器。', 401,
        {'WWW-Authenticate': 'Basic realm="Login Required"'}
    )

def requires_auth(f):
    """认证装饰器"""
    from functools import wraps
    @wraps(f)
    def decorated(*args, **kwargs):
        if is_auth_enabled():
            auth = request.authorization
            if not auth or not check_auth(auth.username, auth.password):
                return authenticate()
        return f(*args, **kwargs)
    return decorated

# 优化的 HTML 模板 (仅恢复了 label 的字体样式)
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
        
        /* 恢复了上一版中 label 的原始字体粗细和大小 */
        label { display: block; margin-bottom: 8px; font-weight: 600; font-size: 0.95rem; color: #71717a; }
        
        textarea { width: 100%; height: 250px; padding: 12px; border: 1px solid #e4e4e7; border-radius: 8px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; resize: vertical; box-sizing: border-box; font-size: 14px; outline: none; }
        textarea:focus { border-color: #3b82f6; ring: 2px #3b82f6; }
        .btn-group { display: flex; gap: 10px; margin: 15px 0; justify-content: center; } 
        button { background: #3b82f6; color: white; border: none; padding: 10px 24px; border-radius: 6px; cursor: pointer; font-size: 15px; font-weight: 500; transition: background 0.2s; }
        button:hover { background: #2563eb; }
        button.secondary { background: #f4f4f5; color: #3f3f46; border: 1px solid #e4e4e7; }
        button.secondary:hover { background: #e4e4e7; }
        .footer { margin-top: 24px; font-size: 13px; color: #a1a1aa; text-align: center; border-top: 1px solid #f4f4f5; padding-top: 16px; font-weight: 500;}
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h2>订阅转换器</h2>
        </div>
        
        <form method="POST" autocomplete="off">
            <label>粘贴链接（一行一个）：</label>
            <textarea name="links" autocomplete="off" placeholder="在这里粘贴您的节点链接...">{% if request.method == 'POST' %}{{ request.form.get('links', '') }}{% endif %}</textarea>
            <div class="btn-group">
                <button type="submit">转换</button>
            </div>
        </form>

        {% if result %}
            <label>转换结果：</label>
            <textarea readonly id="res" autocomplete="off">{{ result }}</textarea>
            <div class="btn-group">
                <button class="secondary" onclick="const el = document.getElementById('res'); el.select(); document.execCommand('copy'); this.innerText='复制成功';">复制结果</button>
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

def parse_link(link):
    link = link.strip()
    if not link: return None
    try:
        if link.startswith('vmess://'):
            b64 = link[8:]
            b64 += "=" * ((4 - len(b64) % 4) % 4)
            v = json.loads(base64.urlsafe_b64decode(b64).decode('utf-8'))
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
    except Exception as e:
        print(f"解析失败: {link} - {e}")
        return None

@app.route('/', methods=['GET', 'POST'])
@requires_auth
def index():
    result = ""
    if request.method == 'POST':
        links = request.form.get('links', '').split('\n')
        proxies = [parse_link(l) for l in links if l.strip()]
        valid_proxies = [p for p in proxies if p]
        if valid_proxies:
            result = yaml.dump({"proxies": valid_proxies}, allow_unicode=True, sort_keys=False, default_flow_style=False)
        else:
            result = "未解析到有效链接。"
    return render_template_string(HTML_TEMPLATE, result=result)

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=5000)