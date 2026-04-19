package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
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

// --- 核心结构体定义 ---

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
        
        #log-console { background: #18181b; color: #10b981; padding: 15px; border-radius: 8px; font-family: ui-monospace, monospace; font-size: 12px; line-height: 1.6; margin-bottom: 20px; max-height: 250px; overflow-y: auto; display: none; border: 1px solid #3f3f46; }
        .log-line { margin: 2px 0; border-bottom: 1px solid #27272a; padding-bottom: 2px; }
        .log-err { color: #ef4444; }
        .log-warn { color: #f59e0b; }
        
        .footer { margin-top: 24px; font-size: 12px; color: #a1a1aa; text-align: center; border-top: 1px solid #f4f4f5; padding-top: 16px; }
        .alert { padding: 12px; border-radius: 8px; margin-bottom: 15px; font-size: 14px; background: #fee2e2; color: #b91c1c; border: 1px solid #fecaca; display: {{ if .Error }}block{{ else }}none{{ end }}; }
        .stats-box { background: #ecfdf5; color: #047857; padding: 12px; border-radius: 8px; margin-bottom: 15px; border: 1px solid #a7f3d0; font-size: 13px; font-weight: 500; }
        .sub-box { display: flex; flex-wrap: wrap; gap: 10px; margin-bottom: 20px; align-items: center; background: #f8fafc; padding: 12px; border: 1px solid #e2e8f0; border-radius: 8px; }
        .sub-input { flex: 1; min-width: 200px; padding: 10px; border: 1px solid #cbd5e1; border-radius: 6px; font-family: monospace; font-size: 13px; outline: none; background: #ffffff; color: #334155; }
        
        @media (max-width: 600px) { .container { padding: 15px; } button { max-width: 100%; } }
    </style>
</head>
<body>
    <div class="container">
        <div class="header"><h2>订阅转换器</h2></div>
        <div class="alert" id="error-alert">{{ .Error }}</div>
        <div id="log-console"></div>
        <form method="POST" autocomplete="off" id="mainForm">
            <label>粘贴链接（一行一个）：</label>
            <textarea name="links" id="linksInput" autocomplete="off" placeholder="支持订阅链接或节点链接">{{ .Links }}</textarea>
            <div class="options-bar">
                <label><input type="checkbox" name="hosted" value="1" {{ if .HostedMode }}checked{{ end }} style="accent-color: #3b82f6;"> 开启托管模式</label>
                <label style="margin-top: 10px;"><input type="checkbox" name="filter" value="1" {{ if .FilterMode }}checked{{ end }} style="accent-color: #3b82f6;"> 剔除无效节点</label>
            </div>
            <div class="btn-group"><button type="submit" class="btn-all">转 换</button></div>
        </form>
        <div id="results-area">
            {{ block "results" . }}
            {{ if .FilterStats }}
            <div class="stats-box">🚀 拉取 {{ .FilterStats.Total }} 个，{{ .FilterStats.Dead }} 个失效，保留 {{ .FilterStats.Alive }} 个健康节点。</div>
            {{ end }}
            {{ if .SubUrl }}
                <label style="color: #059669; margin-top: 10px;">✅ 生成成功！您的专属订阅链接：</label>
                <div class="sub-box">
                    <input type="text" readonly id="subUrl" class="sub-input" value="{{ .SubUrl }}">
                    <button type="button" class="btn-all" onclick="copyText('subUrl', this)" style="margin: 0; max-width: 100px;">复制</button>
                </div>
            {{ end }}
            {{ if .Result }}
                <label>配置预览 (YAML)：</label>
                <textarea readonly id="res">{{ .Result }}</textarea>
                <div class="btn-group"><button type="button" class="btn-secondary" onclick="copyText('res', this)" style="max-width: 200px;">复制预览</button></div>
            {{ end }}
            {{ end }}
        </div>
    </div>
    <div class="footer">谦谦出品</div>
    <script>
        window.appendLog = (msg, type = '') => {
            const console = document.getElementById('log-console');
            console.style.display = 'block';
            const line = document.createElement('div');
            line.className = 'log-line ' + (type ? 'log-' + type : '');
            line.innerText = msg;
            console.appendChild(line);
            console.scrollTop = console.scrollHeight;
        };
        function copyText(id, btn) {
            const el = document.getElementById(id);
            const old = btn.innerText;
            navigator.clipboard.writeText(el.value).then(() => {
                btn.innerText = '已复制!';
                setTimeout(() => btn.innerText = old, 2000);
            });
        }
        document.getElementById('mainForm').onsubmit = () => {
            document.getElementById('results-area').innerHTML = '';
            document.getElementById('log-console').innerHTML = '';
            window.appendLog('📡 开始处理...', 'warn');
        };
    </script>
</body>
</html>
`

// --- 核心调度系统 ---

func getFreePort() int {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func ensureCore(logFunc func(string, string)) {
	if _, err := os.Stat(MIHOMO_BIN); err == nil {
		cmd := exec.Command(MIHOMO_BIN, "-v")
		if err := cmd.Run(); err == nil {
			return
		}
		os.Remove(MIHOMO_BIN)
	}
	os.MkdirAll(MIHOMO_DIR, 0755)
	logFunc("⏳ 正在下载 Mihomo 核心 (v1.19.20)...", "warn")
	osName, arch := runtime.GOOS, runtime.GOARCH
	var dlUrl string
	isZip, version := false, "v1.19.20"
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
	resp, err := http.Get(dlUrl)
	if err != nil || resp.StatusCode != 200 {
		return
	}
	defer resp.Body.Close()
	if isZip {
		b, _ := io.ReadAll(resp.Body)
		zr, _ := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		for _, f := range zr.File {
			if strings.HasSuffix(f.Name, ".exe") {
				rc, _ := f.Open()
				out, _ := os.OpenFile(MIHOMO_BIN, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
				io.Copy(out, rc)
				out.Close()
				rc.Close()
				break
			}
		}
	} else {
		gr, _ := gzip.NewReader(resp.Body)
		out, _ := os.OpenFile(MIHOMO_BIN, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		io.Copy(out, gr)
		out.Close()
		gr.Close()
	}
	if osName != "windows" {
		os.Chmod(MIHOMO_BIN, 0755)
	}
	logFunc("✅ 核心环境就绪", "")
}

func runL7Filter(proxies []map[string]interface{}, logFunc func(string, string)) ([]map[string]interface{}, int, int) {
	if len(proxies) == 0 {
		return proxies, 0, 0
	}
	originalCount := len(proxies)
	ensureCore(logFunc)
	apiPort := getFreePort()
	safeProxies := make([]map[string]interface{}, 0)
	mapping := make(map[string]map[string]interface{})
	for i, p := range proxies {
		name := fmt.Sprintf("p_%d", i)
		pc := make(map[string]interface{})
		for k, v := range p {
			pc[k] = v
		}
		pc["name"] = name
		safeProxies = append(safeProxies, pc)
		mapping[name] = p
	}
	cfgPath := filepath.Join(MIHOMO_DIR, fmt.Sprintf("temp_%d.yaml", apiPort))
	cfg := map[string]interface{}{"allow-lan": false, "external-controller": fmt.Sprintf("127.0.0.1:%d", apiPort), "proxies": safeProxies}
	b, _ := yaml.Marshal(cfg)
	os.WriteFile(cfgPath, b, 0644)
	cmd := exec.Command(MIHOMO_BIN, "-f", cfgPath, "-d", MIHOMO_DIR)
	cmd.Start()
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
		os.Remove(cfgPath)
	}()
	logFunc(fmt.Sprintf("🚀 测速开始：共 %d 个节点", originalCount), "warn")
	apiBase := fmt.Sprintf("http://127.0.0.1:%d", apiPort)
	client := &http.Client{Timeout: (PING_TIMEOUT_MS + 1500) * time.Millisecond}
	ready := false
	for i := 0; i < 20; i++ {
		if r, err := http.Get(apiBase); err == nil {
			r.Body.Close()
			ready = true
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !ready {
		return proxies, originalCount, 0
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	alive := make([]map[string]interface{}, 0)
	target := url.QueryEscape("http://www.gstatic.com/generate_204")
	for name := range mapping {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			realName := mapping[n]["name"].(string)
			u := fmt.Sprintf("%s/proxies/%s/delay?timeout=%d&url=%s", apiBase, n, PING_TIMEOUT_MS, target)
			resp, err := client.Get(u)
			if err != nil {
				logFunc(fmt.Sprintf("[过滤] %s -> ❌ 超时", realName), "err")
				return
			}
			defer resp.Body.Close()
			var res map[string]interface{}
			if err := json.NewDecoder(resp.Body).Decode(&res); err == nil {
				if d, ok := res["delay"].(float64); ok && d > 0 {
					logFunc(fmt.Sprintf("[通过] %s -> ✅ %.0fms", realName, d), "")
					mu.Lock()
					alive = append(alive, mapping[n])
					mu.Unlock()
				} else {
					logFunc(fmt.Sprintf("[过滤] %s -> 握手失败", realName), "err")
				}
			}
		}(name)
	}
	wg.Wait()
	logFunc("✨ 筛选完毕", "warn")
	return alive, originalCount, originalCount - len(alive)
}

// --- 节点解析辅助逻辑 ---

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
			return nil, err
		}
		var v map[string]interface{}
		json.Unmarshal(decoded, &v)
		ps := "VMess"
		if n, ok := v["ps"].(string); ok {
			ps = n
		}
		cfg := map[string]interface{}{
			"name": ps, "type": "vmess", "server": fmt.Sprintf("%v", v["add"]),
			"port": safeInt(v["port"], 443), "uuid": fmt.Sprintf("%v", v["id"]), "cipher": "auto",
		}
		if v["net"] == "ws" {
			cfg["network"] = "ws"
			cfg["ws-opts"] = parseWsOpts(fmt.Sprintf("%v", v["path"]), v["host"])
		}
		if v["tls"] == "tls" {
			cfg["tls"] = true
		}
		return cfg, nil
	}
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	qs := u.Query()
	name := u.Fragment
	if name == "" {
		name = u.Hostname()
	}
	hostPort := u.Host
	if strings.Contains(hostPort, "@") {
		hostPort = strings.Split(hostPort, "@")[1]
	}
	server, portStr := extractServerPort(hostPort)
	pwd := ""
	if u.User != nil {
		pwd, _ = u.User.Password()
		if pwd == "" {
			pwd = u.User.Username()
		}
	}
	switch u.Scheme {
	case "tuic":
		return map[string]interface{}{"name": name, "type": "tuic", "server": server, "port": safeInt(portStr, 443), "uuid": u.User.Username(), "password": pwd}, nil
	case "vless", "trojan":
		cfg := map[string]interface{}{"name": name, "type": u.Scheme, "server": server, "port": safeInt(portStr, 443)}
		if u.Scheme == "vless" {
			cfg["uuid"] = u.User.Username()
		} else {
			cfg["password"] = pwd
		}
		if qs.Get("security") == "tls" || qs.Get("security") == "reality" {
			cfg["tls"] = true
			if qs.Get("sni") != "" {
				cfg["servername"] = qs.Get("sni")
			}
		}
		return cfg, nil
	case "ss":
		dec, _ := decodeBase64Safe(u.Host)
		parts := strings.Split(string(dec), "@")
		if len(parts) < 2 {
			return nil, nil
		}
		up := strings.Split(parts[0], ":")
		sp, pS := extractServerPort(parts[1])
		return map[string]interface{}{"name": name, "type": "ss", "server": sp, "port": safeInt(pS, 443), "cipher": up[0], "password": up[1]}, nil
	}
	return nil, nil
}

func processUrlOrLink(line string) (string, []map[string]interface{}, []string, string) {
	if strings.HasPrefix(line, "http") {
		resp, err := http.Get(line)
		if err != nil {
			return "error", nil, nil, "网络错误"
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var yamlData struct {
			Proxies []map[string]interface{} `yaml:"proxies"`
		}
		if err := yaml.Unmarshal(b, &yamlData); err == nil && len(yamlData.Proxies) > 0 {
			return "clash", yamlData.Proxies, nil, ""
		}
		dec, err := decodeBase64Safe(string(b))
		if err == nil {
			ls := strings.Split(string(dec), "\n")
			var raw []string
			for _, l := range ls {
				if s := strings.TrimSpace(l); s != "" {
					raw = append(raw, s)
				}
			}
			return "raw", nil, raw, ""
		}
		return "error", nil, nil, "无法识别"
	}
	return "raw", nil, []string{line}, ""
}

func generateProxies(lines []string, filterMode bool, logFunc func(string, string)) ([]map[string]interface{}, []string, *Stats) {
	var proxies []map[string]interface{}
	var errs []string
	for _, l := range lines {
		rtype, clashData, rawData, _ := processUrlOrLink(l)
		if rtype == "clash" {
			proxies = append(proxies, clashData...)
		} else if rtype == "raw" {
			for _, r := range rawData {
				p, err := parseLink(r)
				if err == nil && p != nil {
					proxies = append(proxies, p)
				}
			}
		}
	}
	var stats *Stats
	if filterMode && len(proxies) > 0 {
		a, t, d := runL7Filter(proxies, logFunc)
		proxies = a
		stats = &Stats{Total: t, Dead: d, Alive: t - d}
	}
	return proxies, errs, stats
}

// --- 路由与主函数 ---

func indexHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, _ := template.New("i").Parse(HTML_TEMPLATE)
	data := TemplateData{MaxLinks: MAX_LINKS}
	if r.Method == "GET" {
		data.HostedMode, data.FilterMode = true, true
		tmpl.Execute(w, data)
		return
	}
	flusher, ok := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Transfer-Encoding", "chunked")
	r.ParseForm()
	data.Links = r.FormValue("links")
	data.HostedMode, data.FilterMode = r.FormValue("hosted") == "1", r.FormValue("filter") == "1"
	tmpl.Execute(w, data)
	if ok {
		flusher.Flush()
	}
	logToWeb := func(msg, t string) {
		m := strings.ReplaceAll(msg, "'", "\\'")
		fmt.Fprintf(w, "<script>window.appendLog('%s', '%s');</script>", m, t)
		if ok {
			flusher.Flush()
		}
	}
	lines := strings.Split(data.Links, "\n")
	var valid []string
	for _, l := range lines {
		if s := strings.TrimSpace(l); s != "" {
			valid = append(valid, s)
		}
	}
	if len(valid) > 0 {
		proxies, _, stats := generateProxies(valid, data.FilterMode, logToWeb)
		if len(proxies) > 0 {
			y, _ := yaml.Marshal(map[string]interface{}{"proxies": proxies})
			data.Result = string(y)
			data.FilterStats = stats
			if data.HostedMode {
				b64 := base64.URLEncoding.EncodeToString([]byte(strings.Join(valid, "\n")))
				host := r.Host
				baseUrl := fmt.Sprintf("http://%s/sub?data=%s", host, b64)
				if data.FilterMode {
					baseUrl += "&filter=1"
				}
				data.SubUrl = baseUrl
			}
		}
	}
	resBuf := new(bytes.Buffer)
	tmpl.ExecuteTemplate(resBuf, "results", data)
	escaped := base64.StdEncoding.EncodeToString(resBuf.Bytes())
	fmt.Fprintf(w, "<script>document.getElementById('results-area').innerHTML = atob('%s');</script></body></html>", escaped)
}

func subHandler(w http.ResponseWriter, r *http.Request) {
	f := r.URL.Query().Get("filter") == "1"
	d := r.URL.Query().Get("data")
	if d == "" {
		return
	}
	b, _ := decodeBase64Safe(d)
	lines := strings.Split(string(b), "\n")
	var vl []string
	for _, l := range lines {
		if s := strings.TrimSpace(l); s != "" {
			vl = append(vl, s)
		}
	}
	px, _, _ := generateProxies(vl, f, func(s1, s2 string) {})
	y, _ := yaml.Marshal(map[string]interface{}{"proxies": px})
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Write(y)
}

func main() {
	http.HandleFunc("/", requiresAuth(indexHandler))
	http.HandleFunc("/sub", requiresAuth(subHandler))
	fmt.Println("Server is running on http://0.0.0.0:5000")
	http.ListenAndServe(":5000", nil)
}
