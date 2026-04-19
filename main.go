package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// --- 常量与核心配置 ---
const MAX_LINKS = 100
const PING_TIMEOUT_MS = 3000

var (
	MIHOMO_DIR string
	MIHOMO_BIN string
)

func init() {
	cwd, _ := os.Getwd()
	MIHOMO_DIR = filepath.Join(cwd, "core")
	MIHOMO_BIN = filepath.Join(MIHOMO_DIR, "mihomo")
	if runtime.GOOS == "windows" {
		MIHOMO_BIN += ".exe"
	}
}

// --- 授权验证逻辑 ---
var (
	AUTH_USERNAME = os.Getenv("CONVERTER_USER")
	AUTH_PASSWORD = os.Getenv("CONVERTER_PASS")
)

func isAuthEnabled() bool {
	return AUTH_USERNAME != "" || AUTH_PASSWORD != ""
}

func checkAuth(username, password string) bool {
	if !isAuthEnabled() {
		return true
	}
	return username == AUTH_USERNAME && password == AUTH_PASSWORD
}

func requiresAuth(f http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if isAuthEnabled() {
			user, pass, ok := r.BasicAuth()
			if !ok || !checkAuth(user, pass) {
				w.Header().Set("WWW-Authenticate", `Basic realm="Login Required"`)
				http.Error(w, "认证失败。请输入正确的账号密码。", http.StatusUnauthorized)
				return
			}
		}
		f(w, r)
	}
}

// --- HTML 前端模板 ---
const HTML_TEMPLATE = `
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=1.0, user-scalable=no">
    <title>订阅转换器</title>
    <style>
        :root { --primary: #3b82f6; --success: #10b981; --warning: #f59e0b; --danger: #ef4444; --bg: #f4f4f5; --text: #3f3f46; }
        body { font-family: system-ui, -apple-system, sans-serif; background: var(--bg); padding: 15px; color: var(--text); margin: 0; }
        .container { max-width: 900px; margin: 0 auto; background: white; padding: 24px; border-radius: 12px; box-shadow: 0 4px 6px rgba(0,0,0,0.05); box-sizing: border-box; }
        .header { margin-bottom: 20px; text-align: center; } 
        h2 { margin: 0; color: #18181b; font-size: 1.5rem; }
        label { display: block; margin-bottom: 8px; font-weight: 600; font-size: 0.95rem; color: #71717a; }
        
        textarea { width: 100%; height: 200px; padding: 12px; border: 1px solid #e4e4e7; border-radius: 8px; font-family: ui-monospace, monospace; resize: vertical; box-sizing: border-box; font-size: 14px; outline: none; line-height: 1.5; white-space: pre; overflow-wrap: normal; overflow-x: scroll; }
        textarea:focus { border-color: var(--primary); box-shadow: 0 0 0 2px rgba(59, 130, 246, 0.2); }
        
        .options-bar { background: #f8fafc; padding: 12px; border-radius: 8px; margin-bottom: 15px; border: 1px solid #e2e8f0; }
        .options-bar label { margin: 0; font-weight: 500; font-size: 0.9rem; color: #475569; display: flex; align-items: center; gap: 8px; cursor: pointer; }
        
        .btn-group { display: flex; gap: 10px; margin: 15px 0; justify-content: center; flex-wrap: wrap; } 
        button { color: white; border: none; padding: 12px 20px; border-radius: 8px; cursor: pointer; font-size: 14px; font-weight: 500; transition: all 0.2s; box-shadow: 0 2px 4px rgba(0,0,0,0.1); width: 100%; max-width: 200px; }
        button:hover { filter: brightness(1.1); transform: translateY(-1px); }
        button:active { transform: translateY(1px); }
        
        .btn-all { background: var(--primary); }
        .btn-secondary { background: #f4f4f5; color: var(--text); border: 1px solid #e4e4e7; box-shadow: none; min-width: 120px; }
        .btn-secondary:hover { background: #e4e4e7; filter: none; }
        
        .footer { margin-top: 24px; font-size: 12px; color: #a1a1aa; text-align: center; border-top: 1px solid #f4f4f5; padding-top: 16px; }
        
        .alert { padding: 12px; border-radius: 8px; margin-bottom: 15px; font-size: 14px; background: #fee2e2; color: #b91c1c; border: 1px solid #fecaca; display: {{ if .Error }}block{{ else }}none{{ end }}; }
        .error-list { background: #fffbeb; color: #b45309; padding: 12px; border-radius: 8px; margin-bottom: 15px; border: 1px solid #fde68a; font-size: 13px; max-height: 150px; overflow-y: auto; }
        .stats-box { background: #ecfdf5; color: #047857; padding: 12px; border-radius: 8px; margin-bottom: 15px; border: 1px solid #a7f3d0; font-size: 13px; font-weight: 500; }
        
        .sub-box { display: flex; flex-wrap: wrap; gap: 10px; margin-bottom: 20px; align-items: center; background: #f8fafc; padding: 12px; border: 1px solid #e2e8f0; border-radius: 8px; }
        .sub-input { flex: 1; min-width: 200px; padding: 10px; border: 1px solid #cbd5e1; border-radius: 6px; font-family: monospace; font-size: 13px; outline: none; background: #ffffff; color: #334155; }
        .sub-input:focus { border-color: var(--primary); }
        
        @media (max-width: 600px) {
            .container { padding: 15px; }
            button { max-width: 100%; }
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h2>订阅转换器</h2>
        </div>
        
        <div class="alert">{{ .Error }}</div>
        
        {{ if .FilterStats }}
        <div class="stats-box">
            🚀 拉取 {{ .FilterStats.Total }} 个，{{ .FilterStats.Dead }} 个节点无法连通 gstatic.com 被剔除，保留 {{ .FilterStats.Alive }} 个健康节点。
        </div>
        {{ end }}
        
        {{ if .ErrorDetails }}
        <div class="error-list">
            <strong>⚠️ 解析失败日志：</strong>
            <ul style="margin: 5px 0 0 0; padding-left: 20px;">
            {{ range .ErrorDetails }}
                <li>{{ . }}</li>
            {{ end }}
            </ul>
        </div>
        {{ end }}
        
        <form method="POST" autocomplete="off" id="mainForm">
            <label>粘贴链接（一行一个，单次最多 {{ .MaxLinks }} 个）：</label>
            <textarea name="links" id="linksInput" autocomplete="off" placeholder="支持输入：&#10;1. 订阅链接 (http/https)&#10;2. 节点链接 (tuic://, vless://, ss:// 等)">{{ .Links }}</textarea>
            
            <div class="options-bar">
                <label>
                    <input type="checkbox" name="hosted" value="1" {{ if .HostedMode }}checked{{ end }} style="accent-color: #3b82f6; width: 18px; height: 18px;">
                    开启托管模式
                </label>
                <label style="margin-top: 10px;">
                    <input type="checkbox" name="filter" value="1" {{ if .FilterMode }}checked{{ end }} style="accent-color: #3b82f6; width: 18px; height: 18px;">
                    剔除无效节点
                </label>
            </div>
            
            <div class="btn-group">
                <button type="submit" class="btn-all">转 换</button>
            </div>
        </form>

        {{ if .SubUrl }}
            <label style="color: #059669; margin-top: 10px;">✅ 生成成功！您的专属订阅链接：</label>
            <div class="sub-box">
                <input type="text" readonly id="subUrl" class="sub-input" value="{{ .SubUrl }}">
                <button type="button" class="btn-all" id="copySubBtn" onclick="copyText('subUrl', 'copySubBtn')" style="margin: 0; max-width: 120px;">一键复制</button>
            </div>
        {{ end }}

        {{ if .Result }}
            <label>配置预览 (YAML)：</label>
            <textarea readonly id="res" autocomplete="off">{{ .Result }}</textarea>
            <div class="btn-group">
                <button type="button" class="btn-secondary" id="copyResBtn" onclick="copyText('res', 'copyResBtn')" style="max-width: 200px;">复制预览结果</button>
            </div>
        {{ end }}
    </div>
    
    <div class="footer">谦谦出品</div>

    <script>
        if (window.history.replaceState) {
            window.history.replaceState(null, null, window.location.href);
        }

        function copyText(elementId, btnId) {
            const el = document.getElementById(elementId);
            const btn = document.getElementById(btnId);
            const originalText = btn.innerText;
            
            if (navigator.clipboard && window.isSecureContext) {
                navigator.clipboard.writeText(el.value).then(() => {
                    btn.innerText = '已复制!';
                    setTimeout(() => btn.innerText = originalText, 2000);
                });
            } else {
                el.select();
                document.execCommand('copy');
                btn.innerText = '已复制!';
                setTimeout(() => btn.innerText = originalText, 2000);
            }
        }
    </script>
</body>
</html>
`

type TemplateData struct {
	Error        string
	FilterStats  *Stats
	ErrorDetails []string
	MaxLinks     int
	Links        string
	HostedMode   bool
	FilterMode   bool
	SubUrl       string
	Result       string
}

type Stats struct {
	Total int
	Dead  int
	Alive int
}

// --- 核心调度系统：L7 真机测速 ---

func getFreePort() int {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func ensureCore() {
	if _, err := os.Stat(MIHOMO_BIN); err == nil {
		cmd := exec.Command(MIHOMO_BIN, "-v")
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Run(); err == nil {
			return
		}
		fmt.Println("⚠️ 检测到损坏或不兼容的历史核心，正在清理...")
		os.Remove(MIHOMO_BIN)
	}

	os.MkdirAll(MIHOMO_DIR, 0755)
	fmt.Println("⏳ 正在下载对应系统的 Mihomo 核心 (v1.19.20) 用于 L7 测速...")

	osName := runtime.GOOS
	arch := runtime.GOARCH
	var dlUrl string
	isZip := false

	version := "v1.19.20"
	baseURL := "https://github.com/MetaCubeX/mihomo/releases/download/" + version + "/"

	switch osName {
	case "windows":
		isZip = true
		if arch == "arm64" {
			dlUrl = baseURL + "mihomo-windows-arm64-" + version + ".zip"
		} else {
			dlUrl = baseURL + "mihomo-windows-amd64-" + version + ".zip"
		}
	case "darwin":
		if arch == "arm64" {
			dlUrl = baseURL + "mihomo-darwin-arm64-" + version + ".gz"
		} else {
			dlUrl = baseURL + "mihomo-darwin-amd64-" + version + ".gz"
		}
	default:
		if arch == "arm64" {
			dlUrl = baseURL + "mihomo-linux-arm64-" + version + ".gz"
		} else {
			dlUrl = baseURL + "mihomo-linux-amd64-" + version + ".gz"
		}
	}

	req, _ := http.NewRequest("GET", dlUrl, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		fmt.Printf("❌ 核心下载失败 (状态码: %d)，将降级跳过测速: %v\n", resp.StatusCode, err)
		return
	}
	defer resp.Body.Close()

	if isZip {
		bodyBytes, _ := io.ReadAll(resp.Body)
		zipReader, err := zip.NewReader(bytes.NewReader(bodyBytes), int64(len(bodyBytes)))
		if err != nil {
			fmt.Println("❌ ZIP 解析失败:", err)
			return
		}
		for _, file := range zipReader.File {
			if strings.HasSuffix(file.Name, ".exe") {
				f, _ := file.Open()
				out, _ := os.OpenFile(MIHOMO_BIN, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
				io.Copy(out, f)
				out.Close()
				f.Close()
				break
			}
		}
	} else {
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			fmt.Println("❌ 解压 GZ 失败:", err)
			return
		}
		defer gr.Close()

		out, err := os.OpenFile(MIHOMO_BIN, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			fmt.Println("❌ 创建核心文件失败:", err)
			return
		}
		defer out.Close()
		io.Copy(out, gr)
	}

	if osName != "windows" {
		os.Chmod(MIHOMO_BIN, 0755)
	}

	fmt.Println("✅ Mihomo 核心环境部署完毕！")
}

func runL7Filter(proxies []map[string]interface{}) ([]map[string]interface{}, int, int) {
	if len(proxies) == 0 {
		return []map[string]interface{}{}, 0, 0
	}
	originalCount := len(proxies)

	ensureCore()
	if _, err := os.Stat(MIHOMO_BIN); err != nil {
		fmt.Println("⚠️ 核心未找到，跳过测速直接返回原节点")
		return proxies, originalCount, 0
	}

	apiPort := getFreePort()
	safeProxies := make([]map[string]interface{}, 0, len(proxies))
	mapping := make(map[string]map[string]interface{})

	for i, p := range proxies {
		safeName := fmt.Sprintf("p_%d", i)
		pc := make(map[string]interface{})
		for k, v := range p {
			pc[k] = v
		}
		pc["name"] = safeName
		safeProxies = append(safeProxies, pc)
		mapping[safeName] = p
	}

	cfgPath := filepath.Join(MIHOMO_DIR, fmt.Sprintf("temp_%d.yaml", apiPort))
	cfgData := map[string]interface{}{
		"allow-lan":           false,
		"external-controller": fmt.Sprintf("127.0.0.1:%d", apiPort),
		"proxies":             safeProxies,
	}

	b, _ := yaml.Marshal(cfgData)
	os.WriteFile(cfgPath, b, 0644)

	cmd := exec.Command(MIHOMO_BIN, "-f", cfgPath, "-d", MIHOMO_DIR)
	cmd.Stdout = nil
	cmd.Stderr = nil
	err := cmd.Start()
	if err != nil {
		fmt.Printf("❌ Mihomo 进程启动失败: %v\n", err)
		os.Remove(cfgPath)
		return proxies, originalCount, 0
	}

	fmt.Println("\n=========================================")
	fmt.Printf("🚀 测速模式已激活：对 %d 个节点开始并发测试 (gstatic.com)\n", originalCount)
	fmt.Println("=========================================")

	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
		os.Remove(cfgPath)
	}()

	apiBase := fmt.Sprintf("http://127.0.0.1:%d", apiPort)
	client := &http.Client{Timeout: time.Millisecond * time.Duration(PING_TIMEOUT_MS+1500)}

	ready := false
	for i := 0; i < 30; i++ {
		resp, err := http.Get(apiBase)
		if err == nil {
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	aliveProxies := make([]map[string]interface{}, 0)

	if ready {
		time.Sleep(300 * time.Millisecond)

		var wg sync.WaitGroup
		var mu sync.Mutex
		targetURL := url.QueryEscape("http://www.gstatic.com/generate_204")

		for name := range mapping {
			wg.Add(1)
			go func(proxyName string) {
				defer wg.Done()
				realName := mapping[proxyName]["name"].(string)

				urlStr := fmt.Sprintf("%s/proxies/%s/delay?timeout=%d&url=%s", apiBase, proxyName, PING_TIMEOUT_MS, targetURL)

				req, _ := http.NewRequest("GET", urlStr, nil)
				resp, err := client.Do(req)
				if err != nil {
					fmt.Printf("[过滤] 节点 '%s' -> ❌ 连接超时或被拒绝\n", realName)
					return
				}
				defer resp.Body.Close()

				var result map[string]interface{}
				if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
					if delay, ok := result["delay"].(float64); ok && delay > 0 {
						fmt.Printf("[通过] 节点 '%s' -> ✅ 延迟: %.0f ms\n", realName, delay)
						mu.Lock()
						aliveProxies = append(aliveProxies, mapping[proxyName])
						mu.Unlock()
					} else {
						fmt.Printf("[过滤] 节点 '%s' -> ❌ 握手失败或无响应\n", realName)
					}
				}
			}(name)
		}
		wg.Wait()
		fmt.Println("=========================================\n✅ 测速筛选完毕！")
	} else {
		fmt.Println("⚠️ API 控制器就绪超时，跳过测速环节。")
		aliveProxies = proxies
	}

	deadCount := originalCount - len(aliveProxies)
	return aliveProxies, originalCount, deadCount
}

// --- 节点解析工具逻辑 (保持不变) ---

func decodeBase64Safe(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "_", "/")
	if pad := len(s) % 4; pad != 0 {
		s += strings.Repeat("=", 4-pad)
	}
	return base64.StdEncoding.DecodeString(s)
}

func safeInt(val interface{}, defaultVal int) int {
	switch v := val.(type) {
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	case float64:
		return int(v)
	case int:
		return v
	}
	return defaultVal
}

func parseWsOpts(pathStr string, hostVal interface{}) map[string]interface{} {
	opts := make(map[string]interface{})
	re := regexp.MustCompile(`^(.*?)(?:\?ed=(\d+))?$`)
	matches := re.FindStringSubmatch(pathStr)
	path := "/"
	if len(matches) > 1 && matches[1] != "" {
		path = matches[1]
	}
	opts["path"] = path
	hostStr := ""
	if h, ok := hostVal.(string); ok {
		hostStr = h
	} else if hList, ok := hostVal.([]interface{}); ok && len(hList) > 0 {
		hostStr = fmt.Sprintf("%v", hList[0])
	}
	if hostStr != "" {
		opts["headers"] = map[string]string{"Host": hostStr}
	}
	if len(matches) > 2 && matches[2] != "" {
		if ed, err := strconv.Atoi(matches[2]); err == nil {
			opts["max-early-data"] = ed
			opts["early-data-header-name"] = "Sec-WebSocket-Protocol"
		}
	}
	return opts
}

func extractServerPort(hostPortStr string) (string, string) {
	if strings.Contains(hostPortStr, "]") {
		parts := strings.SplitN(hostPortStr, "]", 2)
		server := strings.TrimLeft(parts[0], "[")
		portStr := "443"
		if len(parts) > 1 && strings.HasPrefix(parts[1], ":") {
			portStr = strings.TrimPrefix(parts[1], ":")
		}
		return server, portStr
	}
	if strings.Contains(hostPortStr, ":") {
		idx := strings.LastIndex(hostPortStr, ":")
		return hostPortStr[:idx], hostPortStr[idx+1:]
	}
	return hostPortStr, "443"
}

func parseLink(link string) (map[string]interface{}, error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return nil, nil
	}
	if strings.HasPrefix(link, "vmess://") {
		decoded, err := decodeBase64Safe(link[8:])
		if err != nil {
			return nil, fmt.Errorf("Base64 解码失败")
		}
		var v map[string]interface{}
		if err := json.Unmarshal(decoded, &v); err != nil {
			return nil, err
		}
		ps := fmt.Sprintf("VMess-%v", v["add"])
		if name, ok := v["ps"].(string); ok && name != "" {
			ps = name
		}
		cfg := map[string]interface{}{
			"name": ps, "type": "vmess", "server": fmt.Sprintf("%v", v["add"]),
			"port": safeInt(v["port"], 443), "uuid": fmt.Sprintf("%v", v["id"]), "cipher": "auto",
		}
		if cy, ok := v["scy"].(string); ok && cy != "" {
			cfg["cipher"] = cy
		}
		if _, hasAead := v["aead"]; hasAead {
			cfg["alterId"] = 0
		} else {
			cfg["alterId"] = safeInt(v["aid"], 0)
		}
		ciphers := map[string]bool{"auto": true, "none": true, "zero": true, "aes-128-gcm": true, "chacha20-poly1305": true}
		if !ciphers[cfg["cipher"].(string)] {
			cfg["cipher"] = "auto"
		}
		if v["net"] == "ws" {
			cfg["network"] = "ws"
			path := "/"
			if p, ok := v["path"].(string); ok {
				path = p
			}
			cfg["ws-opts"] = parseWsOpts(path, v["host"])
		}
		if v["tls"] == "tls" {
			cfg["tls"] = true
			if sni, ok := v["sni"].(string); ok && sni != "" {
				cfg["servername"] = sni
			}
		}
		return cfg, nil
	}
	u, err := url.Parse(link)
	if err != nil {
		if !strings.HasPrefix(link, "ss://") {
			return nil, err
		}
	}
	qs := u.Query()
	name := u.Fragment
	if name == "" {
		name = fmt.Sprintf("%s-%s", strings.ToUpper(u.Scheme), u.Hostname())
	}
	netloc := u.Host
	if netloc == "" && u.Opaque != "" {
		netloc = u.Opaque
	}
	hostPort := netloc
	if strings.Contains(netloc, "@") {
		parts := strings.Split(netloc, "@")
		hostPort = parts[len(parts)-1]
	}
	server, portStr := extractServerPort(hostPort)
	password := ""
	uuidStr := ""
	if u.User != nil {
		uuidStr = u.User.Username()
		password, _ = u.User.Password()
		if password == "" {
			password = uuidStr
		}
	}
	switch u.Scheme {
	case "tuic":
		cfg := map[string]interface{}{"name": name, "type": "tuic", "server": server, "port": safeInt(portStr, 443), "uuid": uuidStr, "password": password}
		if qs.Get("sni") != "" {
			cfg["sni"] = qs.Get("sni")
		}
		if qs.Get("alpn") != "" {
			cfg["alpn"] = strings.Split(qs.Get("alpn"), ",")
		}
		if qs.Get("congestion_control") != "" {
			cfg["congestion-controller"] = qs.Get("congestion_control")
		}
		if qs.Get("allow-insecure") == "1" || qs.Get("insecure") == "1" {
			cfg["skip-cert-verify"] = true
		}
		return cfg, nil
	case "hysteria2", "hy2", "hysteria", "hy":
		isHy2 := u.Scheme == "hysteria2" || u.Scheme == "hy2"
		t := "hysteria"
		if isHy2 {
			t = "hysteria2"
		}
		cfg := map[string]interface{}{"name": name, "type": t, "server": server, "password": password}
		if strings.Contains(portStr, "-") || strings.Contains(portStr, ",") {
			cfg["ports"] = portStr
			pParts := strings.Split(portStr, "-")
			pParts = strings.Split(pParts[0], ",")
			cfg["port"] = safeInt(pParts[0], 443)
		} else {
			cfg["port"] = safeInt(portStr, 443)
		}
		sni := qs.Get("peer")
		if sni == "" {
			sni = qs.Get("sni")
		}
		if sni != "" {
			cfg["sni"] = sni
		}
		if qs.Get("pinSHA256") != "" {
			cfg["fingerprint"] = qs.Get("pinSHA256")
		}
		if qs.Get("insecure") == "1" {
			cfg["skip-cert-verify"] = true
		}
		if qs.Get("obfs") != "" {
			cfg["obfs"] = qs.Get("obfs")
		}
		obfsPwd := qs.Get("obfs-password")
		if obfsPwd == "" {
			obfsPwd = qs.Get("obfsParam")
		}
		if obfsPwd != "" {
			cfg["obfs-password"] = obfsPwd
		}
		if qs.Get("alpn") != "" {
			cfg["alpn"] = strings.Split(qs.Get("alpn"), ",")
		}
		return cfg, nil
	case "vless", "trojan":
		cfg := map[string]interface{}{"name": name, "type": u.Scheme, "server": server, "port": safeInt(portStr, 443)}
		if u.Scheme == "vless" {
			cfg["uuid"] = uuidStr
		} else {
			cfg["password"] = password
		}
		if qs.Has("flow") {
			cfg["flow"] = qs.Get("flow")
		}
		network := qs.Get("type")
		if network != "" {
			cfg["network"] = network
		}
		if network == "ws" {
			path := qs.Get("path")
			if path == "" {
				path = "/"
			}
			cfg["ws-opts"] = parseWsOpts(path, qs.Get("host"))
		} else if network == "grpc" {
			grpcOpts := make(map[string]interface{})
			if qs.Has("serviceName") {
				grpcOpts["grpc-service-name"] = qs.Get("serviceName")
			}
			cfg["grpc-opts"] = grpcOpts
		}
		sec := qs.Get("security")
		if sec == "tls" || sec == "reality" || u.Scheme == "trojan" {
			if u.Scheme != "trojan" || sec == "tls" {
				cfg["tls"] = true
			}
			if qs.Has("sni") {
				cfg["servername"] = qs.Get("sni")
			}
			fp := qs.Get("fp")
			if fp == "" {
				fp = qs.Get("pcs")
			}
			if fp != "" {
				cfg["client-fingerprint"] = fp
			}
			if sec == "reality" {
				if fp == "" {
					cfg["client-fingerprint"] = "chrome"
				}
				realOpts := make(map[string]interface{})
				if qs.Has("pbk") {
					realOpts["public-key"] = qs.Get("pbk")
				}
				if qs.Has("sid") {
					realOpts["short-id"] = qs.Get("sid")
				}
				if qs.Has("spx") {
					realOpts["spider-x"] = qs.Get("spx")
				}
				cfg["reality-opts"] = realOpts
			}
		}
		if qs.Get("allowInsecure") == "1" || qs.Get("insecure") == "1" {
			cfg["skip-cert-verify"] = true
		}
		return cfg, nil
	case "ss":
		cfg := map[string]interface{}{"name": name, "type": "ss"}
		netloc := u.Host
		if netloc == "" {
			netloc = u.Opaque
		}
		var userinfo, hostPortStr, method, passwd string
		if strings.Contains(netloc, "@") {
			parts := strings.SplitN(netloc, "@", 2)
			userinfo, hostPortStr = parts[0], parts[1]
			if strings.Contains(userinfo, ":") && !strings.HasPrefix(userinfo, "?") {
				uParts := strings.SplitN(userinfo, ":", 2)
				method, passwd = uParts[0], uParts[1]
			} else {
				dec, err := decodeBase64Safe(userinfo)
				if err == nil && strings.Contains(string(dec), ":") {
					uParts := strings.SplitN(string(dec), ":", 2)
					method, passwd = uParts[0], uParts[1]
				}
			}
		} else {
			dec, err := decodeBase64Safe(netloc)
			if err == nil {
				parts := strings.SplitN(string(dec), "@", 2)
				if len(parts) == 2 {
					userinfo, hostPortStr = parts[0], parts[1]
					uParts := strings.SplitN(userinfo, ":", 2)
					if len(uParts) == 2 {
						method, passwd = uParts[0], uParts[1]
					}
				}
			}
		}
		srv, portS := extractServerPort(hostPortStr)
		cfg["server"] = srv
		cfg["port"] = safeInt(portS, 443)
		if method != "" {
			cfg["cipher"] = method
		}
		if passwd != "" {
			cfg["password"] = passwd
		}
		return cfg, nil
	}
	return nil, fmt.Errorf("不支持的协议或格式异常")
}

func processUrlOrLink(line string) (string, []map[string]interface{}, []string, string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "empty", nil, nil, ""
	}
	if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
		tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
		client := &http.Client{Transport: tr, Timeout: 8 * time.Second}
		req, _ := http.NewRequest("GET", line, nil)
		req.Header.Set("User-Agent", "ClashMeta/1.18.0")
		resp, err := client.Do(req)
		if err != nil {
			return "error", nil, nil, fmt.Sprintf("网络请求失败 (%v)", err)
		}
		defer resp.Body.Close()
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return "error", nil, nil, "读取响应失败"
		}
		content := string(bodyBytes)
		var yamlData struct {
			Proxies []map[string]interface{} `yaml:"proxies"`
		}
		if err := yaml.Unmarshal(bodyBytes, &yamlData); err == nil && len(yamlData.Proxies) > 0 {
			return "clash", yamlData.Proxies, nil, ""
		}
		decoded, err := decodeBase64Safe(content)
		if err == nil {
			lines := strings.Split(string(decoded), "\n")
			var rawLinks []string
			for _, l := range lines {
				if l = strings.TrimSpace(l); l != "" {
					rawLinks = append(rawLinks, l)
				}
			}
			if len(rawLinks) > 0 {
				return "raw", nil, rawLinks, ""
			}
		}
		return "error", nil, nil, "远程内容不是有效的 YAML 或 Base64"
	}
	return "raw", nil, []string{line}, ""
}

func generateProxies(lines []string, filterMode bool) ([]map[string]interface{}, []string, *Stats) {
	var proxies []map[string]interface{}
	var errorDetails []string
	var stats *Stats
	for _, line := range lines {
		rtype, clashData, rawData, errStr := processUrlOrLink(line)
		prefix := line
		if len(prefix) > 40 {
			prefix = prefix[:40] + "..."
		}
		if rtype == "clash" {
			proxies = append(proxies, clashData...)
		} else if rtype == "raw" {
			for _, rawLink := range rawData {
				parsed, err := parseLink(rawLink)
				if err != nil {
					rawPrefix := rawLink
					if len(rawPrefix) > 40 {
						rawPrefix = rawPrefix[:40] + "..."
					}
					errorDetails = append(errorDetails, fmt.Sprintf("提取节点 %s => %v", rawPrefix, err))
				} else if parsed != nil {
					proxies = append(proxies, parsed)
				}
			}
		} else if rtype == "error" {
			errorDetails = append(errorDetails, fmt.Sprintf("%s => %s", prefix, errStr))
		}
	}
	if filterMode && len(proxies) > 0 {
		filteredProxies, total, dead := runL7Filter(proxies)
		proxies = filteredProxies
		stats = &Stats{Total: total, Dead: dead, Alive: total - dead}
	}
	return proxies, errorDetails, stats
}

// --- 路由 ---

func indexHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, _ := template.New("index").Parse(HTML_TEMPLATE)
	data := TemplateData{MaxLinks: MAX_LINKS}
	if r.Method == "GET" {
		data.HostedMode = true
		data.FilterMode = true
		tmpl.Execute(w, data)
		return
	}
	if r.Method == "POST" {
		r.ParseForm()
		data.HostedMode = r.FormValue("hosted") == "1"
		data.FilterMode = r.FormValue("filter") == "1"
		linksText := r.FormValue("links")
		data.Links = linksText
		linesRaw := strings.Split(linksText, "\n")
		var lines []string
		for _, l := range linesRaw {
			if l = strings.TrimSpace(l); l != "" {
				lines = append(lines, l)
			}
		}
		if len(lines) == 0 {
			data.Error = "请输入链接内容。"
			tmpl.Execute(w, data)
			return
		}
		if len(lines) > MAX_LINKS {
			data.Error = fmt.Sprintf("为防止滥用，单次最多允许处理 %d 个链接。已为您截断。", MAX_LINKS)
			lines = lines[:MAX_LINKS]
		}
		proxies, errs, stats := generateProxies(lines, data.FilterMode)
		data.ErrorDetails = errs
		data.FilterStats = stats
		if len(proxies) == 0 && len(errs) > 0 && !data.FilterMode {
			if data.Error == "" {
				data.Error = "处理失败，所有输入均未能成功解析。"
			}
		}
		if len(proxies) > 0 {
			yamlBytes, _ := yaml.Marshal(map[string]interface{}{"proxies": proxies})
			data.Result = string(yamlBytes)
			// 【修复点】：只有开启托管模式，才生成订阅链接
			if data.HostedMode {
				b64Data := base64.URLEncoding.EncodeToString([]byte(strings.Join(lines, "\n")))
				scheme := r.Header.Get("X-Forwarded-Proto")
				if scheme == "" {
					scheme = "http"
					if r.TLS != nil {
						scheme = "https"
					}
				}
				host := r.Header.Get("X-Forwarded-Host")
				if host == "" {
					host = r.Host
				}
				baseUrl := fmt.Sprintf("%s://", scheme)
				if isAuthEnabled() {
					baseUrl += fmt.Sprintf("%s:%s@", url.QueryEscape(AUTH_USERNAME), url.QueryEscape(AUTH_PASSWORD))
				}
				baseUrl += host + "/sub"
				params := url.Values{}
				params.Add("data", b64Data)
				if data.FilterMode {
					params.Add("filter", "1")
				}
				data.SubUrl = fmt.Sprintf("%s?%s", baseUrl, params.Encode())
			}
		}
		tmpl.Execute(w, data)
	}
}

func subHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	filterMode := r.URL.Query().Get("filter") == "1"
	encodedData := r.URL.Query().Get("data")
	if encodedData == "" {
		http.Error(w, "缺少订阅数据参数", http.StatusBadRequest)
		return
	}
	decodedBytes, err := decodeBase64Safe(encodedData)
	if err != nil {
		http.Error(w, fmt.Sprintf("Base64 解码异常: %v", err), http.StatusBadRequest)
		return
	}
	linesRaw := strings.Split(string(decodedBytes), "\n")
	var lines []string
	for _, l := range linesRaw {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		http.Error(w, "订阅内容为空", http.StatusBadRequest)
		return
	}
	if len(lines) > MAX_LINKS {
		lines = lines[:MAX_LINKS]
	}
	proxies, _, _ := generateProxies(lines, filterMode)
	if len(proxies) > 0 {
		yamlBytes, _ := yaml.Marshal(map[string]interface{}{"proxies": proxies})
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Write(yamlBytes)
	} else {
		http.Error(w, "未能提取到任何有效节点", http.StatusBadRequest)
	}
}

func main() {
	// 根路径：主页，使用认证中间件包装
	http.HandleFunc("/", requiresAuth(indexHandler))

	// 订阅路径：生成的结果，使用认证中间件包装
	http.HandleFunc("/sub", requiresAuth(subHandler))

	port := "5000"
	fmt.Printf("Server is running on http://0.0.0.0:%s\n", port)

	// 启动服务
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		panic(err)
	}
}
