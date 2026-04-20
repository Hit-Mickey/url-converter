package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
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
const LINK_EXPIRY_DAYS = 7 // 过期天数为 7 天

var (
	MIHOMO_DIR string
	MIHOMO_BIN string
)

// --- 短链系统存储 ---
type ShortLink struct {
	Data           string    `json:"data"`
	FilterMode     bool      `json:"filter"`
	LastAccessedAt time.Time `json:"last_accessed_at"` // 记录最后使用时间
}

var (
	shortLinkMu    sync.RWMutex
	shortLinks     = make(map[string]ShortLink)
	shortLinksFile string
)

func init() {
	cwd, _ := os.Getwd()
	MIHOMO_DIR = filepath.Join(cwd, "core")
	MIHOMO_BIN = filepath.Join(MIHOMO_DIR, "mihomo")
	if runtime.GOOS == "windows" {
		MIHOMO_BIN += ".exe"
	}
	shortLinksFile = filepath.Join(MIHOMO_DIR, "shortlinks.json")

	// 启动时清理残留 cache.db
	os.Remove(filepath.Join(MIHOMO_DIR, "cache.db"))

	loadShortLinks()
}

func loadShortLinks() {
	os.MkdirAll(MIHOMO_DIR, 0755)
	b, err := os.ReadFile(shortLinksFile)
	if err == nil {
		json.Unmarshal(b, &shortLinks)
	}
}

func saveShortLinks() {
	b, _ := json.MarshalIndent(shortLinks, "", "  ")
	os.WriteFile(shortLinksFile, b, 0644)
}

// 自动清理过期链接的任务
func startCleanupTask() {
	ticker := time.NewTicker(1 * time.Hour)
	go func() {
		for range ticker.C {
			cleanupExpiredLinks()
		}
	}()
}

func cleanupExpiredLinks() {
	shortLinkMu.Lock()
	defer shortLinkMu.Unlock()

	now := time.Now()
	count := 0
	for id, link := range shortLinks {
		if now.Sub(link.LastAccessedAt) > time.Duration(LINK_EXPIRY_DAYS)*24*time.Hour {
			delete(shortLinks, id)
			count++
		}
	}
	if count > 0 {
		saveShortLinks()
		fmt.Printf("[%s] 清理任务：删除了 %d 条过期短链\n", now.Format("2006-01-02 15:04:05"), count)
	}
}

func getOrCreateShortLink(data string, filter bool) string {
	h := md5.Sum([]byte(fmt.Sprintf("%s|%v", data, filter)))
	id := hex.EncodeToString(h[:])[:8]

	shortLinkMu.Lock()
	defer shortLinkMu.Unlock()
	if _, exists := shortLinks[id]; !exists {
		shortLinks[id] = ShortLink{
			Data:           data,
			FilterMode:     filter,
			LastAccessedAt: time.Now(),
		}
		saveShortLinks()
	}
	return id
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
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
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
    <meta http-equiv="Cache-Control" content="no-store, no-cache, must-revalidate, post-check=0, pre-check=0">
    <meta http-equiv="Pragma" content="no-cache">
    <meta http-equiv="Expires" content="0">
    <title>订阅转换器</title>
    <style>
        :root {
            --primary: #3b82f6; --primary-hover: #2563eb; --success: #10b981;
            --success-bg: #ecfdf5; --success-border: #a7f3d0; --success-text: #047857;
            --warning: #f59e0b; --warning-bg: #fffbeb; --warning-border: #fde68a; --warning-text: #b45309;
            --danger: #ef4444; --danger-bg: #fee2e2; --danger-border: #fecaca; --danger-text: #b91c1c;
            --bg: #f4f4f5; --text: #3f3f46; --text-muted: #71717a; --card-bg: #ffffff;
            --border: #e4e4e7; --shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.1), 0 2px 4px -1px rgba(0, 0, 0, 0.06);
        }
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body { font-family: system-ui, -apple-system, sans-serif; background: var(--bg); padding: 20px 15px; color: var(--text); line-height: 1.6; }
        .container { max-width: 900px; margin: 0 auto; background: var(--card-bg); padding: 28px; border-radius: 16px; box-shadow: var(--shadow); }
        .header { margin-bottom: 24px; text-align: center; padding-bottom: 16px; border-bottom: 1px solid var(--border); }
        .header h2 { margin: 0; color: #18181b; font-size: 1.6rem; font-weight: 700; }
        .header p { margin-top: 6px; color: var(--text-muted); font-size: 0.9rem; }
        label { display: block; margin-bottom: 10px; font-weight: 600; font-size: 0.95rem; color: #52525b; }
        textarea { width: 100%; height: 180px; padding: 12px 14px; border: 2px solid var(--border); border-radius: 10px; font-family: monospace; resize: vertical; font-size: 13px; outline: none; background: #fafafa; }
        textarea:focus { border-color: var(--primary); box-shadow: 0 0 0 3px rgba(59, 130, 246, 0.15); background: #fff; }
        .options-bar { background: #f8fafc; padding: 14px 16px; border-radius: 10px; margin: 16px 0; border: 1px solid var(--border); display: flex; flex-wrap: wrap; gap: 16px; align-items: center; }
        .options-bar label { margin: 0; font-weight: 500; font-size: 0.9rem; color: #475569; display: flex; align-items: center; gap: 8px; cursor: pointer; user-select: none; }
        .btn-group { display: flex; gap: 12px; margin: 20px 0; justify-content: center; flex-wrap: wrap; }
        button { color: white; border: none; padding: 12px 28px; border-radius: 10px; cursor: pointer; font-size: 14px; font-weight: 600; min-width: 140px; background: var(--primary); }
        button:hover:not(:disabled) { background: var(--primary-hover); transform: translateY(-1px); box-shadow: 0 4px 8px rgba(0,0,0,0.12); }
        button:disabled { opacity: 0.8; cursor: not-allowed; }
        .footer { margin-top: 32px; font-size: 12px; color: #a1a1aa; text-align: center; padding-top: 20px; border-top: 1px solid var(--border); }
        .alert { padding: 14px 16px; border-radius: 10px; margin-bottom: 16px; font-size: 14px; background: var(--danger-bg); color: var(--danger-text); border: 1px solid var(--danger-border); display: {{ if .Error }}block{{ else }}none{{ end }}; }
        .error-list { background: var(--warning-bg); color: var(--warning-text); padding: 14px 16px; border-radius: 10px; margin-bottom: 16px; border: 1px solid var(--warning-border); font-size: 13px; max-height: 160px; overflow-y: auto; }
        .stats-box { background: var(--success-bg); color: var(--success-text); padding: 14px 16px; border-radius: 10px; margin-bottom: 16px; border: 1px solid var(--success-border); font-size: 14px; font-weight: 500; display: flex; align-items: center; gap: 8px; }
        .sub-box { display: flex; flex-wrap: wrap; gap: 10px; margin: 16px 0; align-items: center; background: #f8fafc; padding: 12px; border: 1px solid var(--border); border-radius: 10px; }
        .sub-input { flex: 1; min-width: 200px; padding: 10px 12px; border: 1px solid #cbd5e1; border-radius: 6px; font-size: 13px; }
        
        .filter-card { background: var(--card-bg); border: 1px solid var(--border); border-radius: 12px; margin: 20px 0; overflow: hidden; }
        .filter-card summary { padding: 14px 18px; background: linear-gradient(135deg, #f8fafc 0%, #f1f5f9 100%); border-bottom: 1px solid var(--border); cursor: pointer; font-weight: 600; color: #334155; display: flex; align-items: center; justify-content: space-between; list-style: none; }
        .filter-card summary::-webkit-details-marker { display: none; } /* 隐藏原生三角 */
        
        /* 交互三角样式与动画 */
        .summary-left { display: flex; align-items: center; gap: 10px; }
        .toggle-icon { display: inline-block; width: 0; height: 0; border-top: 7px solid transparent; border-bottom: 7px solid transparent; border-left: 12px solid #64748b; transition: transform 0.1s cubic-bezier(0.4, 0, 0.2, 1); }
        details[open] > summary .toggle-icon { transform: rotate(90deg); }

        .filter-stats-inline { display: inline-flex; align-items: center; gap: 12px; font-size: 0.85rem; font-weight: 500; }
        .stat-badge { padding: 3px 10px; border-radius: 20px; font-size: 0.8rem; }
        .stat-badge.pass { background: var(--success-bg); color: var(--success-text); border: 1px solid var(--success-border); }
        .stat-badge.fail { background: var(--danger-bg); color: var(--danger-text); border: 1px solid var(--danger-border); }
        .filter-log { padding: 12px 18px 18px; background: #fafafa; max-height: 280px; overflow-y: auto; font-family: monospace; font-size: 12px; }
        .log-item { padding: 6px 0; border-bottom: 1px dashed #e2e8f0; display: flex; align-items: flex-start; gap: 8px; }
        .result-card { margin-top: 20px; border: 1px solid var(--border); border-radius: 12px; overflow: hidden; }
        .result-card label { padding: 12px 16px; background: #f8fafc; border-bottom: 1px solid var(--border); font-weight: 600; }
        .result-card textarea { border: none; border-radius: 0; height: 220px; background: #fff; }
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
            <span><img src="/images/白猫-拉取节点.svg" alt="白猫" style="position: relative; top: -1px;vertical-align: middle; margin-right: 6px; height: 16px;">拉取 <strong>{{ .FilterStats.Total }}</strong> 个节点，
            <span style="color: var(--danger);">{{ .FilterStats.Dead }}</span> 个无法连通被剔除，
            保留 <strong style="color: var(--success);">{{ .FilterStats.Alive }}</strong> 个健康节点</span>
        </div>
        {{ end }}
        
        {{ if .ErrorDetails }}
        <div class="error-list">
            <strong>⚠️ 解析失败：</strong>
            <ul>
            {{ range .ErrorDetails }}
                <li>{{ . }}</li>
            {{ end }}
            </ul>
        </div>
        {{ end }}

        {{ if .FilterResults }}
        <div class="filter-card">
            <details open>
                <summary>
                    <div class="summary-left">
                        <i class="toggle-icon"></i>
                        <span><img src="/images/更多猫宠-节点检测详情.svg" alt="更多猫宠" style="position: relative; top: -1px;vertical-align: middle; margin-right: 5px; height: 16px;">节点检测详情</span>
                    </div>
                    <div class="filter-stats-inline">
                        <span class="stat-badge pass">✅ {{ .FilterStats.Alive }} 通过</span>
                        <span class="stat-badge fail">❌ {{ .FilterStats.Dead }} 失败</span>
                    </div>
                </summary>
                <div class="filter-log">
                    {{ range .FilterResults }}
                        <div class="log-item">
                            <span class="log-text">{{ . }}</span>
                        </div>
                    {{ end }}
                </div>
            </details>
        </div>
        {{ end }}
        
        <form method="POST" autocomplete="off" id="mainForm">
            <label for="linksInput">
                <img src="/images/布偶猫-粘贴混合内容.svg" alt="布偶猫" style="position: relative; top: -1px;vertical-align: middle; margin-right: 0px; height: 18px;">
                粘贴混合内容（最大支持解析 {{ .MaxLinks }} 个节点）
            </label>
            <textarea name="links" id="linksInput" autocomplete="off" placeholder="支持混合输入：&#10;• 多个订阅链接 (http/https)&#10;• 多协议节点链接 (tuic://, vless://, ss:// 等)&#10;• 完整的或部分的 Clash/Mihomo 配置 (YAML)">{{ .Links }}</textarea>
            
            <div class="options-bar">
                <label title="开启后可生成持久短链订阅">
                    <input type="checkbox" name="hosted" value="1" {{ if .HostedMode }}checked{{ end }}>
                    <img src="/images/柴犬-托管模式.svg" alt="柴犬" style="vertical-align: middle; margin-right: -3px; height: 16px;">托管模式
                </label>
                <label title="自动测试节点连通性并剔除无效节点">
                    <input type="checkbox" name="filter" value="1" {{ if .FilterMode }}checked{{ end }}>
                    <img src="/images/金毛-删除无效节点.svg" alt="金毛" style="vertical-align: middle; margin-right: -3px; height: 16px;">剔除无效节点
                </label>
            </div>
            
            <div class="btn-group">
                <button type="submit" id="submitBtn"><img src="/images/橘猫-开始转换.svg" alt="橘猫" style="position: relative; top: -1px;vertical-align: middle; margin-right: 6px; height: 16px;">开始转换</button>
            </div>
        </form>

        {{ if .SubUrl }}
            <label style="color: var(--success-text); margin-top: 16px; display: block;"><img src="/images/柯基-专属订阅短链接.svg" alt="柯基" style="position: relative; top: -1px;vertical-align: middle; margin-right: 6px; height: 16px;">专属订阅短链接（7天不使用将过期）：</label>
            <div class="sub-box">
                <input type="text" readonly id="subUrl" class="sub-input" value="{{ .SubUrl }}">
                <button type="button" onclick="copyText('subUrl', this)" style="margin: 0; min-width: 100px; padding: 10px 16px;"><img src="/images/田园犬-复制.svg" alt="田园犬" style="position: relative; top: -2px;vertical-align: middle; margin-right: 6px; height: 16px;">复制</button>
            </div>
        {{ end }}

        {{ if .Result }}
            <div class="result-card">
                <label><img src="/images/可达鸭-配置预览.svg" alt="可达鸭" style="position: relative; top: -1px;vertical-align: middle; margin-right: 6px; height: 16px;">配置预览 (YAML)</label>
                <textarea readonly id="res" autocomplete="off">{{ .Result }}</textarea>
                <div style="padding: 12px 16px; background: #f8fafc; border-top: 1px solid var(--border);">
                    <button type="button" onclick="copyText('res', this)" style="min-width: 160px;"><img src="/images/边牧-复制预览结果.svg" alt="边牧" style="position: relative; top: -1px;vertical-align: middle; margin-right: 6px; height: 16px;">复制预览结果</button>
                </div>
            </div>
        {{ end }}
    </div>
    
    <div class="footer">谦谦出品</div>

    <script>
        window.addEventListener('load', function() {
            const form = document.getElementById('mainForm');
            if (form) form.reset(); 
            if (window.history.replaceState) {
                window.history.replaceState(null, null, window.location.href);
            }
        });

        document.addEventListener('DOMContentLoaded', function() {
            const form = document.getElementById('mainForm');
            const submitBtn = document.getElementById('submitBtn');
            form.addEventListener('submit', function(e) {
                if (submitBtn.disabled) { e.preventDefault(); return; }
                submitBtn.innerText = '🚀 转换中...';
                submitBtn.disabled = true;
            });
        });

        function copyText(elementId, btn) {
            const el = document.getElementById(elementId);
            const originalText = btn.innerText;
            el.select();
            el.setSelectionRange(0, 99999);
            document.execCommand('copy');
            btn.innerText = '✓ 已复制';
            btn.style.background = 'var(--success)';
            setTimeout(() => {
                btn.innerText = originalText;
                btn.style.background = 'var(--primary)';
            }, 2000);
        }
    </script>
</body>
</html>
`

type TemplateData struct {
	Error         string
	FilterStats   *Stats
	FilterResults []string
	ErrorDetails  []string
	MaxLinks      int
	Links         string
	HostedMode    bool
	FilterMode    bool
	SubUrl        string
	Result        string
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
	// 动态获取内核版本
	version := os.Getenv("MIHOMO_VERSION")
	if version == "" {
		version = "v1.19.20"
	}

	if _, err := os.Stat(MIHOMO_BIN); err == nil {
		// 检查版本是否兼容
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
	fmt.Printf("⏳ 正在下载对应系统的 Mihomo 核心 (%s) 用于 L7 测速...\n", version)

	osName := runtime.GOOS
	arch := runtime.GOARCH
	var dlUrl string
	isZip := false

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

func runL7Filter(proxies []map[string]interface{}) ([]map[string]interface{}, int, int, []string) {
	if len(proxies) == 0 {
		return []map[string]interface{}{}, 0, 0, []string{}
	}
	originalCount := len(proxies)

	ensureCore()
	if _, err := os.Stat(MIHOMO_BIN); err != nil {
		fmt.Println("⚠️ 核心未找到，跳过测速直接返回原节点")
		return proxies, originalCount, 0, []string{}
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
		return proxies, originalCount, 0, []string{fmt.Sprintf("Mihomo 进程启动失败: %v", err)}
	}

	var results []string
	results = append(results, fmt.Sprintf("🚀 开始对 %d 个节点进行并发测试 (gstatic.com)", originalCount))

	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
		os.Remove(cfgPath)

		// 每次测速完自动清理
		cachePath := filepath.Join(MIHOMO_DIR, "cache.db")
		if _, err := os.Stat(cachePath); err == nil {
			os.Remove(cachePath)
		}
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
					mu.Lock()
					results = append(results, fmt.Sprintf("[过滤] %s -> ❌ 连接超时或被拒绝", realName))
					mu.Unlock()
					return
				}
				defer resp.Body.Close()

				var result map[string]interface{}
				if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
					if delay, ok := result["delay"].(float64); ok && delay > 0 {
						mu.Lock()
						results = append(results, fmt.Sprintf("[通过] %s -> ✅ 延迟: %.0f ms", realName, delay))
						aliveProxies = append(aliveProxies, mapping[proxyName])
						mu.Unlock()
					} else {
						mu.Lock()
						results = append(results, fmt.Sprintf("[过滤] %s -> ❌ 握手失败或无响应", realName))
						mu.Unlock()
					}
				} else {
					mu.Lock()
					results = append(results, fmt.Sprintf("[过滤] %s -> ❌ 解析响应失败", realName))
					mu.Unlock()
				}
			}(name)
		}
		wg.Wait()
		results = append(results, "✅ 测速筛选完毕！")
	} else {
		results = append(results, "⚠️ API 控制器就绪超时，跳过测速环节。")
		aliveProxies = proxies
	}

	deadCount := originalCount - len(aliveProxies)
	return aliveProxies, originalCount, deadCount, results
}

// --- 节点解析工具逻辑 ---

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

func generateProxies(lines []string, filterMode bool) ([]map[string]interface{}, []string, *Stats, []string) {
	var proxies []map[string]interface{}
	var errorDetails []string
	var stats *Stats
	var filterResults []string
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
		filteredProxies, total, dead, results := runL7Filter(proxies)
		proxies = filteredProxies
		filterResults = results
		stats = &Stats{Total: total, Dead: dead, Alive: total - dead}
	}
	return proxies, errorDetails, stats, filterResults
}

func isLikelyBase64Links(data []byte) bool {
	str := string(data)
	return strings.Contains(str, "://")
}

func parseYamlArray(lines []string, proxies *[]map[string]interface{}) {
	str := strings.Join(lines, "\n")
	var arr []map[string]interface{}
	if err := yaml.Unmarshal([]byte(str), &arr); err == nil && len(arr) > 0 {
		*proxies = append(*proxies, arr...)
	}
}

func extractProxyBlocks(lines []string) []map[string]interface{} {
	var proxies []map[string]interface{}
	inProxies := false
	var currentBlock []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "proxies:") {
			inProxies = true
			if len(currentBlock) > 0 {
				parseYamlArray(currentBlock, &proxies)
				currentBlock = nil
			}
			continue
		}

		if inProxies {
			if len(line) > 0 && line[0] != ' ' && line[0] != '-' && line[0] != '#' && strings.Contains(line, ":") {
				inProxies = false
				parseYamlArray(currentBlock, &proxies)
				currentBlock = nil
				continue
			}
			currentBlock = append(currentBlock, line)
		}
	}
	if inProxies && len(currentBlock) > 0 {
		parseYamlArray(currentBlock, &proxies)
	}
	if len(proxies) == 0 {
		parseYamlArray(lines, &proxies)
	}

	return proxies
}

// --- 节点名称去重逻辑 ---
func deduplicateProxyNames(proxies []map[string]interface{}) {
	seenNames := make(map[string]bool)
	for _, p := range proxies {
		name, ok := p["name"].(string)
		if !ok {
			continue
		}

		originalName := name
		counter := 1
		for seenNames[name] {
			name = fmt.Sprintf("%s_%d", originalName, counter)
			counter++
		}

		p["name"] = name
		seenNames[name] = true
	}
}

func extractProxiesFromInput(inputText string, filterMode bool) ([]map[string]interface{}, []string, *Stats, []string) {
	var allProxies []map[string]interface{}
	var allErrs []string

	lines := strings.Split(inputText, "\n")
	var rawLinks []string
	var textLines []string

	linkRegex := regexp.MustCompile(`(?i)^(https?|vmess|vless|ss|ssr|trojan|tuic|hysteria|hysteria2|hy2)://`)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if linkRegex.MatchString(trimmed) && !strings.Contains(trimmed, " ") {
			rawLinks = append(rawLinks, trimmed)
		} else {
			textLines = append(textLines, line)
		}
	}

	if len(rawLinks) > 0 {
		proxies, errs, _, _ := generateProxies(rawLinks, false)
		allProxies = append(allProxies, proxies...)
		allErrs = append(allErrs, errs...)
	}

	restText := strings.Join(textLines, "\n")
	if strings.TrimSpace(restText) != "" {
		b64Str := strings.ReplaceAll(restText, "\n", "")
		b64Str = strings.ReplaceAll(b64Str, "\r", "")

		if decoded, err := decodeBase64Safe(b64Str); err == nil && isLikelyBase64Links(decoded) {
			b64Lines := strings.Split(string(decoded), "\n")
			proxies, errs, _, _ := generateProxies(b64Lines, false)
			allProxies = append(allProxies, proxies...)
			allErrs = append(allErrs, errs...)
		} else {
			yamlProxies := extractProxyBlocks(textLines)
			if len(yamlProxies) > 0 {
				allProxies = append(allProxies, yamlProxies...)
			} else {
				var doc struct {
					Proxies []map[string]interface{} `yaml:"proxies"`
				}
				if err := yaml.Unmarshal([]byte(restText), &doc); err == nil && len(doc.Proxies) > 0 {
					allProxies = append(allProxies, doc.Proxies...)
				} else {
					if len(textLines) > 0 {
						allErrs = append(allErrs, "部分混合文本未能识别为有效的配置或代理")
					}
				}
			}
		}
	}

	if len(allProxies) > MAX_LINKS {
		allProxies = allProxies[:MAX_LINKS]
	}

	deduplicateProxyNames(allProxies)

	var stats *Stats
	var filterResults []string
	if filterMode && len(allProxies) > 0 {
		filteredProxies, total, dead, results := runL7Filter(allProxies)
		allProxies = filteredProxies
		filterResults = results
		stats = &Stats{Total: total, Dead: dead, Alive: total - dead}
	}

	return allProxies, allErrs, stats, filterResults
}

func buildOutput(proxies []map[string]interface{}) []byte {
	b, _ := yaml.Marshal(map[string]interface{}{"proxies": proxies})
	return b
}

// --- 路由 ---

func indexHandler(w http.ResponseWriter, r *http.Request) {
	funcMap := template.FuncMap{
		"contains": strings.Contains,
	}
	tmpl, _ := template.New("index").Funcs(funcMap).Parse(HTML_TEMPLATE)
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

		if strings.TrimSpace(linksText) == "" {
			data.Error = "请输入混合链接内容或配置文件。"
			tmpl.Execute(w, data)
			return
		}

		proxies, errs, stats, filterResults := extractProxiesFromInput(linksText, data.FilterMode)

		data.ErrorDetails = errs
		data.FilterStats = stats
		data.FilterResults = filterResults

		if len(proxies) == 0 && len(errs) > 0 && !data.FilterMode {
			if data.Error == "" {
				data.Error = "处理失败，所有输入均未能成功解析。"
			}
		}

		if len(proxies) > 0 {
			yamlBytes := buildOutput(proxies)
			data.Result = string(yamlBytes)

			if data.HostedMode {
				b64Data := base64.URLEncoding.EncodeToString([]byte(linksText))
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

				id := getOrCreateShortLink(b64Data, data.FilterMode)
				data.SubUrl = fmt.Sprintf("%s?id=%s", baseUrl, id)
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

	var filterMode bool
	var encodedData string

	id := r.URL.Query().Get("id")
	if id != "" {
		shortLinkMu.Lock()
		sl, exists := shortLinks[id]
		if exists {
			sl.LastAccessedAt = time.Now()
			shortLinks[id] = sl
			saveShortLinks()
		}
		shortLinkMu.Unlock()

		if !exists {
			http.Error(w, "订阅短链接不存在或已过期", http.StatusNotFound)
			return
		}
		encodedData = sl.Data
		filterMode = sl.FilterMode
	} else {
		encodedData = r.URL.Query().Get("data")
		filterMode = r.URL.Query().Get("filter") == "1"
	}

	if encodedData == "" {
		http.Error(w, "缺少订阅数据参数", http.StatusBadRequest)
		return
	}

	decodedBytes, err := decodeBase64Safe(encodedData)
	if err != nil {
		http.Error(w, fmt.Sprintf("Base64 解码异常: %v", err), http.StatusBadRequest)
		return
	}

	inputText := string(decodedBytes)
	if strings.TrimSpace(inputText) == "" {
		http.Error(w, "订阅内容为空", http.StatusBadRequest)
		return
	}

	proxies, _, _, _ := extractProxiesFromInput(inputText, filterMode)
	if len(proxies) > 0 {
		yamlBytes := buildOutput(proxies)
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Write(yamlBytes)
	} else {
		http.Error(w, "未能提取到任何有效节点", http.StatusBadRequest)
	}
}

func main() {
	startCleanupTask()

	// 添加静态文件服务，处理图片等静态资源
	fs := http.FileServer(http.Dir("images"))
	http.Handle("/images/", http.StripPrefix("/images/", fs))

	http.HandleFunc("/", requiresAuth(indexHandler))
	http.HandleFunc("/sub", requiresAuth(subHandler))

	port := os.Getenv("PORT")
	if port == "" {
		port = "5000"
	}
	fmt.Printf("Server is running on http://0.0.0.0:%s\n", port)

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		panic(err)
	}
}
