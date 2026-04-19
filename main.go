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

var (
	MIHOMO_DIR string
	MIHOMO_BIN string
)

// --- 短链系统存储 ---
type ShortLink struct {
	Data       string `json:"data"`
	FilterMode bool   `json:"filter"`
	FullConfig bool   `json:"full"`
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

func getOrCreateShortLink(data string, filter, full bool) string {
	h := md5.Sum([]byte(fmt.Sprintf("%s|%v|%v", data, filter, full)))
	id := hex.EncodeToString(h[:])[:8]

	shortLinkMu.Lock()
	defer shortLinkMu.Unlock()
	if _, exists := shortLinks[id]; !exists {
		shortLinks[id] = ShortLink{Data: data, FilterMode: filter, FullConfig: full}
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
    <meta http-equiv="Cache-Control" content="no-store, no-cache, must-revalidate, post-check=0, pre-check=0">
    <meta http-equiv="Pragma" content="no-cache">
    <meta http-equiv="Expires" content="0">
    <title>订阅转换器</title>
    <style>
        :root {
            --primary: #3b82f6;
            --primary-hover: #2563eb;
            --success: #10b981;
            --success-bg: #ecfdf5;
            --success-border: #a7f3d0;
            --success-text: #047857;
            --warning: #f59e0b;
            --warning-bg: #fffbeb;
            --warning-border: #fde68a;
            --warning-text: #b45309;
            --danger: #ef4444;
            --danger-bg: #fee2e2;
            --danger-border: #fecaca;
            --danger-text: #b91c1c;
            --bg: #f4f4f5;
            --text: #3f3f46;
            --text-muted: #71717a;
            --card-bg: #ffffff;
            --border: #e4e4e7;
            --shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.1), 0 2px 4px -1px rgba(0, 0, 0, 0.06);
        }
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
            background: var(--bg);
            padding: 20px 15px;
            color: var(--text);
            line-height: 1.6;
        }
        .container {
            max-width: 900px;
            margin: 0 auto;
            background: var(--card-bg);
            padding: 28px;
            border-radius: 16px;
            box-shadow: var(--shadow);
        }
        .header {
            margin-bottom: 24px;
            text-align: center;
            padding-bottom: 16px;
            border-bottom: 1px solid var(--border);
        }
        .header h2 {
            margin: 0;
            color: #18181b;
            font-size: 1.6rem;
            font-weight: 700;
        }
        .header p {
            margin-top: 6px;
            color: var(--text-muted);
            font-size: 0.9rem;
        }
        
        label {
            display: block;
            margin-bottom: 10px;
            font-weight: 600;
            font-size: 0.95rem;
            color: #52525b;
        }
        
        textarea {
            width: 100%;
            height: 180px;
            padding: 12px 14px;
            border: 2px solid var(--border);
            border-radius: 10px;
            font-family: ui-monospace, SFMono-Regular, monospace;
            resize: vertical;
            font-size: 13px;
            outline: none;
            line-height: 1.5;
            transition: border-color 0.2s, box-shadow 0.2s;
            background: #fafafa;
        }
        textarea:focus {
            border-color: var(--primary);
            box-shadow: 0 0 0 3px rgba(59, 130, 246, 0.15);
            background: #fff;
        }
        
        .options-bar {
            background: #f8fafc;
            padding: 14px 16px;
            border-radius: 10px;
            margin: 16px 0;
            border: 1px solid var(--border);
            display: flex;
            flex-wrap: wrap;
            gap: 16px;
            align-items: center;
        }
        .options-bar label {
            margin: 0;
            font-weight: 500;
            font-size: 0.9rem;
            color: #475569;
            display: flex;
            align-items: center;
            gap: 8px;
            cursor: pointer;
            user-select: none;
        }
        .options-bar input[type="checkbox"] {
            width: 18px;
            height: 18px;
            accent-color: var(--primary);
            cursor: pointer;
        }
        
        .btn-group {
            display: flex;
            gap: 12px;
            margin: 20px 0;
            justify-content: center;
            flex-wrap: wrap;
        }
        button {
            color: white;
            border: none;
            padding: 12px 28px;
            border-radius: 10px;
            cursor: pointer;
            font-size: 14px;
            font-weight: 600;
            transition: all 0.2s ease;
            box-shadow: 0 2px 4px rgba(0,0,0,0.08);
            min-width: 140px;
        }
        button:hover:not(:disabled) {
            filter: brightness(1.05);
            transform: translateY(-1px);
            box-shadow: 0 4px 8px rgba(0,0,0,0.12);
        }
        button:active:not(:disabled) {
            transform: translateY(0);
        }
        button:disabled {
            opacity: 0.8;
            cursor: not-allowed;
        }
        .btn-all { background: var(--primary); }
        .btn-all:hover:not(:disabled) { background: var(--primary-hover); }
        
        .footer {
            margin-top: 32px;
            font-size: 12px;
            color: #a1a1aa;
            text-align: center;
            padding-top: 20px;
            border-top: 1px solid var(--border);
        }
        
        /* 消息提示框 */
        .alert {
            padding: 14px 16px;
            border-radius: 10px;
            margin-bottom: 16px;
            font-size: 14px;
            background: var(--danger-bg);
            color: var(--danger-text);
            border: 1px solid var(--danger-border);
            display: {{ if .Error }}block{{ else }}none{{ end }};
        }
        .error-list {
            background: var(--warning-bg);
            color: var(--warning-text);
            padding: 14px 16px;
            border-radius: 10px;
            margin-bottom: 16px;
            border: 1px solid var(--warning-border);
            font-size: 13px;
            max-height: 160px;
            overflow-y: auto;
        }
        .error-list ul { margin: 6px 0 0 0; padding-left: 20px; }
        .error-list li { margin: 4px 0; }
        
        .stats-box {
            background: var(--success-bg);
            color: var(--success-text);
            padding: 14px 16px;
            border-radius: 10px;
            margin-bottom: 16px;
            border: 1px solid var(--success-border);
            font-size: 14px;
            font-weight: 500;
            display: flex;
            align-items: center;
            gap: 8px;
        }
        
        /* 订阅链接框 */
        .sub-box {
            display: flex;
            flex-wrap: wrap;
            gap: 10px;
            margin: 16px 0;
            align-items: center;
            background: #f8fafc;
            padding: 12px;
            border: 1px solid var(--border);
            border-radius: 10px;
        }
        .sub-input {
            flex: 1;
            min-width: 200px;
            padding: 10px 12px;
            border: 1px solid #cbd5e1;
            border-radius: 6px;
            font-family: ui-monospace, monospace;
            font-size: 13px;
            outline: none;
            background: #fff;
            color: #334155;
        }
        .sub-input:focus { border-color: var(--primary); }
        
        /* 节点检测详情模块 */
        .filter-card {
            background: var(--card-bg);
            border: 1px solid var(--border);
            border-radius: 12px;
            margin: 20px 0;
            overflow: hidden;
            box-shadow: 0 1px 3px rgba(0,0,0,0.05);
        }
        .filter-card summary {
            padding: 14px 18px;
            background: linear-gradient(135deg, #f8fafc 0%, #f1f5f9 100%);
            border-bottom: 1px solid var(--border);
            cursor: pointer;
            font-weight: 600;
            font-size: 0.95rem;
            color: #334155;
            display: flex;
            align-items: center;
            gap: 10px;
            list-style: none;
            transition: background 0.2s;
        }
        .filter-card summary:hover {
            background: linear-gradient(135deg, #f1f5f9 0%, #e2e8f0 100%);
        }
        .filter-card summary::-webkit-details-marker { display: none; }
        .filter-card summary::before {
            content: "▶";
            font-size: 0.8rem;
            color: var(--text-muted);
            transition: transform 0.25s cubic-bezier(0.4, 0, 0.2, 1);
            display: inline-block;
        }
        .filter-card details[open] > summary::before {
            transform: rotate(90deg);
        }
        
        .filter-stats-inline {
            display: inline-flex;
            align-items: center;
            gap: 12px;
            margin-left: auto;
            font-size: 0.85rem;
            font-weight: 500;
        }
        .stat-badge {
            padding: 3px 10px;
            border-radius: 20px;
            font-size: 0.8rem;
        }
        .stat-badge.pass { background: var(--success-bg); color: var(--success-text); border: 1px solid var(--success-border); }
        .stat-badge.fail { background: var(--danger-bg); color: var(--danger-text); border: 1px solid var(--danger-border); }
        
        .filter-log {
            padding: 12px 18px 18px;
            background: #fafafa;
            max-height: 280px;
            overflow-y: auto;
            font-family: ui-monospace, SFMono-Regular, monospace;
            font-size: 12px;
            line-height: 1.7;
        }
        .filter-log::-webkit-scrollbar { width: 6px; }
        .filter-log::-webkit-scrollbar-track { background: #f1f5f9; border-radius: 3px; }
        .filter-log::-webkit-scrollbar-thumb { background: #cbd5e1; border-radius: 3px; }
        
        .log-item {
            padding: 6px 0;
            border-bottom: 1px dashed #e2e8f0;
            display: flex;
            align-items: flex-start;
            gap: 8px;
            animation: fadeIn 0.2s ease;
        }
        .log-item:last-child { border-bottom: none; }
        @keyframes fadeIn {
            from { opacity: 0; transform: translateY(4px); }
            to { opacity: 1; transform: translateY(0); }
        }
        .log-icon { flex-shrink: 0; width: 18px; text-align: center; font-weight: bold; }
        .log-pass .log-icon { color: var(--success); }
        .log-fail .log-icon { color: var(--danger); }
        .log-info .log-icon { color: var(--text-muted); }
        .log-pass { color: #065f46; }
        .log-fail { color: #991b1b; }
        .log-info { color: var(--text-muted); }
        
        /* 结果预览区域 */
        .result-card {
            margin-top: 20px;
            border: 1px solid var(--border);
            border-radius: 12px;
            overflow: hidden;
        }
        .result-card label {
            display: block;
            padding: 12px 16px;
            background: #f8fafc;
            border-bottom: 1px solid var(--border);
            font-weight: 600;
            font-size: 0.9rem;
            color: #334155;
        }
        .result-card textarea {
            border: none;
            border-radius: 0;
            height: 220px;
            background: #fff;
        }
        
        /* 转换按钮加载动画 */
        .spinner {
            display: inline-block;
            width: 14px;
            height: 14px;
            border: 2px solid rgba(255,255,255,0.3);
            border-radius: 50%;
            border-top-color: #fff;
            animation: spin 0.75s linear infinite;
            margin-right: 6px;
            vertical-align: middle;
        }
        @keyframes spin {
            to { transform: rotate(360deg); }
        }

        @media (max-width: 600px) {
            .container { padding: 20px 16px; }
            .header h2 { font-size: 1.4rem; }
            button { min-width: 120px; padding: 10px 20px; font-size: 13px; }
            .options-bar { flex-direction: column; align-items: flex-start; gap: 10px; }
            .filter-stats-inline { margin-left: 0; margin-top: 6px; flex-wrap: wrap; }
            .sub-box { flex-direction: column; align-items: stretch; }
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h2>订阅转换器</h2>
            <p>支持多配置混合/多协议直链/订阅链接 · 节点连通性检测</p>
        </div>
        
        <div class="alert">{{ .Error }}</div>
        
        {{ if .FilterStats }}
        <div class="stats-box">
            <span>🚀</span>
            <span>拉取 <strong>{{ .FilterStats.Total }}</strong> 个节点，
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
            <details>
                <summary>
                    <span>📋 节点检测详情</span>
                    <div class="filter-stats-inline">
                        <span class="stat-badge pass">✅ {{ .FilterStats.Alive }} 通过</span>
                        <span class="stat-badge fail">❌ {{ .FilterStats.Dead }} 失败</span>
                    </div>
                </summary>
                <div class="filter-log">
                    {{ range .FilterResults }}
                        {{ if contains . "✅" }}
                            <div class="log-item log-pass">
                                <span class="log-icon">✓</span>
                                <span class="log-text">{{ . }}</span>
                            </div>
                        {{ else if contains . "❌" }}
                            <div class="log-item log-fail">
                                <span class="log-icon">✗</span>
                                <span class="log-text">{{ . }}</span>
                            </div>
                        {{ else }}
                            <div class="log-item log-info">
                                <span class="log-icon">•</span>
                                <span class="log-text">{{ . }}</span>
                            </div>
                        {{ end }}
                    {{ end }}
                </div>
            </details>
        </div>
        {{ end }}
        
        <form method="POST" autocomplete="off" id="mainForm">
            <label for="linksInput">🔗 粘贴混合内容（最大支持解析 {{ .MaxLinks }} 个节点）</label>
            <textarea name="links" id="linksInput" autocomplete="off" placeholder="支持混合输入：&#10;• 多个订阅链接 (http/https)&#10;• 多协议节点链接 (tuic://, vless://, ss:// 等)&#10;• Clash/Mihomo 配置 (YAML)"></textarea>
            
            <div class="options-bar">
                <label title="开启后可生成持久短链订阅">
                    <input type="checkbox" name="hosted" value="1" {{ if .HostedMode }}checked{{ end }}>
                    托管模式
                </label>
                <label title="自动测试节点连通性并剔除无效节点">
                    <input type="checkbox" name="filter" value="1" {{ if .FilterMode }}checked{{ end }}>
                    剔除无效节点
                </label>
                <label title="生成完整的包含路由和策略组的配置文件">
                    <input type="checkbox" name="full" value="1" {{ if .FullConfig }}checked{{ end }}>
                    生成完整配置
                </label>
            </div>
            
            <div class="btn-group">
                <button type="submit" class="btn-all" id="submitBtn">开始转换</button>
            </div>
        </form>

        {{ if .SubUrl }}
            <label style="color: var(--success-text); margin-top: 16px; display: block;">生成成功！专属订阅短链接：</label>
            <div class="sub-box">
                <input type="text" readonly id="subUrl" class="sub-input" value="{{ .SubUrl }}">
                <button type="button" class="btn-all" id="copySubBtn" onclick="copyText('subUrl', 'copySubBtn')" style="margin: 0; min-width: 100px; padding: 10px 16px;">复制</button>
            </div>
        {{ end }}

        {{ if .Result }}
            <div class="result-card">
                <label>📄 配置预览 (YAML)</label>
                <textarea readonly id="res" autocomplete="off">{{ .Result }}</textarea>
                <div style="padding: 12px 16px; background: #f8fafc; border-top: 1px solid var(--border);">
                    <button type="button" class="btn-all" id="copyResBtn" onclick="copyText('res', 'copyResBtn')" style="min-width: 160px;">复制预览结果</button>
                </div>
            </div>
        {{ end }}
    </div>
    
    <div class="footer">谦谦出品</div>

    <script>
        // ✅ 刷新页面时强制回到初始状态，防止浏览器缓存 POST 结果
        window.addEventListener('load', function() {
            const form = document.getElementById('mainForm');
            if (form) {
                form.reset(); // 清空输入框并重置复选框到 HTML 默认状态
            }
            // 替换当前历史记录，防止 F5 触发“确认重新提交表单”提示
            if (window.history.replaceState) {
                window.history.replaceState(null, null, window.location.href);
            }
        });

        document.addEventListener('DOMContentLoaded', function() {
            const form = document.getElementById('mainForm');
            const submitBtn = document.getElementById('submitBtn');
            
            form.addEventListener('submit', function(e) {
                if (submitBtn.classList.contains('btn-loading')) {
                    e.preventDefault();
                    return;
                }
                submitBtn.classList.add('btn-loading');
                submitBtn.innerHTML = '<span class="spinner"></span>转换中...';
                submitBtn.disabled = true;
            });

            // 默认展开检测详情（如果有结果）
            const details = document.querySelector('.filter-card details');
            if (details && {{ if .FilterResults }}true{{ else }}false{{ end }}) {
                details.open = true;
            }
        });

        function copyText(elementId, btnId) {
            const el = document.getElementById(elementId);
            const btn = document.getElementById(btnId);
            const originalText = btn.innerText;
            
            const successCallback = () => {
                btn.innerText = '✓ 已复制';
                btn.style.background = 'var(--success)';
                setTimeout(() => {
                    btn.innerText = originalText;
                    btn.style.background = '';
                }, 2000);
            };
            
            if (navigator.clipboard && window.isSecureContext) {
                navigator.clipboard.writeText(el.value).then(successCallback).catch(() => fallbackCopy(el, btn, originalText));
            } else {
                fallbackCopy(el, btn, originalText);
            }
        }
        
        function fallbackCopy(el, btn, originalText) {
            el.select();
            el.setSelectionRange(0, 99999);
            document.execCommand('copy');
            btn.innerText = '✓ 已复制';
            btn.style.background = 'var(--success)';
            setTimeout(() => {
                btn.innerText = originalText;
                btn.style.background = '';
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
	FullConfig    bool
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

// 辅助方法：判断解码后的内容是否包含常见协议头部
func isLikelyBase64Links(data []byte) bool {
	str := string(data)
	return strings.Contains(str, "://")
}

// 辅助方法：解析纯 YAML 节点数组
func parseYamlArray(lines []string, proxies *[]map[string]interface{}) {
	str := strings.Join(lines, "\n")
	var arr []map[string]interface{}
	if err := yaml.Unmarshal([]byte(str), &arr); err == nil && len(arr) > 0 {
		*proxies = append(*proxies, arr...)
	}
}

// 辅助方法：智能从混合文本中抽取只属于 proxies: 下的块
func extractProxyBlocks(lines []string) []map[string]interface{} {
	var proxies []map[string]interface{}
	inProxies := false
	var currentBlock []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// 如果发现 proxies: 根节点声明，开始收集
		if strings.HasPrefix(trimmed, "proxies:") {
			inProxies = true
			if len(currentBlock) > 0 {
				parseYamlArray(currentBlock, &proxies)
				currentBlock = nil
			}
			continue
		}

		if inProxies {
			// 如果该行没有任何前置空格，且不是 -（列表项）也不是 #（注释），则认为遇到了下一个根节点
			if len(line) > 0 && line[0] != ' ' && line[0] != '-' && line[0] != '#' && strings.Contains(line, ":") {
				inProxies = false
				parseYamlArray(currentBlock, &proxies)
				currentBlock = nil
				continue
			}
			currentBlock = append(currentBlock, line)
		}
	}
	// 处理最后一个可能未闭合的块
	if inProxies && len(currentBlock) > 0 {
		parseYamlArray(currentBlock, &proxies)
	}

	// 如果没有找到明确的 proxies: 标记，则可能用户粘贴了纯节点数组，尝试整体作为一个数组解析
	if len(proxies) == 0 {
		parseYamlArray(lines, &proxies)
	}

	return proxies
}

// --- 新增：节点名称去重逻辑 ---
func deduplicateProxyNames(proxies []map[string]interface{}) {
	seenNames := make(map[string]bool)
	for _, p := range proxies {
		name, ok := p["name"].(string)
		if !ok {
			continue
		}

		originalName := name
		counter := 1
		// 如果名字已经存在，则不断加后缀直到唯一
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

	// 正则匹配明确的代理链接格式或 HTTP 订阅链接
	linkRegex := regexp.MustCompile(`(?i)^(https?|vmess|vless|ss|ssr|trojan|tuic|hysteria|hysteria2|hy2)://`)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// 如果这一行完全符合链接格式（不含空格的连续字符串），归入 links 处理池
		if linkRegex.MatchString(trimmed) && !strings.Contains(trimmed, " ") {
			rawLinks = append(rawLinks, trimmed)
		} else {
			// 将其它格式归入 textLines，需保留原始前置空格以维持 YAML 缩进正确性
			textLines = append(textLines, line)
		}
	}

	// 1. 处理直链及远程 http 订阅 (将其转交 generateProxies，暂时关闭内置 filter)
	if len(rawLinks) > 0 {
		proxies, errs, _, _ := generateProxies(rawLinks, false)
		allProxies = append(allProxies, proxies...)
		allErrs = append(allErrs, errs...)
	}

	// 2. 处理剩余文本内容（可能包含完整的 YAML、纯节点配置、Base64 订阅信息等）
	restText := strings.Join(textLines, "\n")
	if strings.TrimSpace(restText) != "" {
		// 尝试去除换行后做 Base64 检查
		b64Str := strings.ReplaceAll(restText, "\n", "")
		b64Str = strings.ReplaceAll(b64Str, "\r", "")

		if decoded, err := decodeBase64Safe(b64Str); err == nil && isLikelyBase64Links(decoded) {
			b64Lines := strings.Split(string(decoded), "\n")
			proxies, errs, _, _ := generateProxies(b64Lines, false)
			allProxies = append(allProxies, proxies...)
			allErrs = append(allErrs, errs...)
		} else {
			// 作为 YAML 内容进行处理：首先尝试抽取 proxies 数组块
			yamlProxies := extractProxyBlocks(textLines)
			if len(yamlProxies) > 0 {
				allProxies = append(allProxies, yamlProxies...)
			} else {
				// 兜底策略：作为完整单文档尝试解析
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

	// 执行数量限制截断
	if len(allProxies) > MAX_LINKS {
		allProxies = allProxies[:MAX_LINKS]
	}

	// ✅ 关键修复：在测速和输出之前，强制对所有节点名称进行去重处理
	deduplicateProxyNames(allProxies)

	// 统一进行连通性检测（如果需要的话），避免对多处分离的代理执行多次测试
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

func buildOutput(proxies []map[string]interface{}, fullConfig bool) []byte {
	if !fullConfig {
		b, _ := yaml.Marshal(map[string]interface{}{"proxies": proxies})
		return b
	}

	var proxyNames []string
	for _, p := range proxies {
		if name, ok := p["name"].(string); ok {
			proxyNames = append(proxyNames, name)
		}
	}

	config := map[string]interface{}{
		"port":       7890,
		"socks-port": 7891,
		"allow-lan":  true,
		"mode":       "rule",
		"log-level":  "info",
		"proxies":    proxies,
		"proxy-groups": []map[string]interface{}{
			{
				"name":    "PROXIES",
				"type":    "select",
				"proxies": append([]string{"DIRECT"}, proxyNames...),
			},
		},
		"rules": []string{
			"MATCH,PROXIES",
		},
	}
	b, _ := yaml.Marshal(config)
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
		data.FullConfig = r.FormValue("full") == "1"
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
			yamlBytes := buildOutput(proxies, data.FullConfig)
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

				// 生成本地持久化短链 ID
				id := getOrCreateShortLink(b64Data, data.FilterMode, data.FullConfig)
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

	var filterMode, fullConfig bool
	var encodedData string

	id := r.URL.Query().Get("id")
	if id != "" {
		shortLinkMu.RLock()
		sl, exists := shortLinks[id]
		shortLinkMu.RUnlock()
		if !exists {
			http.Error(w, "订阅短链接不存在或已失效", http.StatusNotFound)
			return
		}
		encodedData = sl.Data
		filterMode = sl.FilterMode
		fullConfig = sl.FullConfig
	} else {
		// 兼容旧版的长链接请求
		encodedData = r.URL.Query().Get("data")
		filterMode = r.URL.Query().Get("filter") == "1"
		fullConfig = r.URL.Query().Get("full") == "1"
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
		yamlBytes := buildOutput(proxies, fullConfig)
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Write(yamlBytes)
	} else {
		http.Error(w, "未能提取到任何有效节点", http.StatusBadRequest)
	}
}

func main() {
	http.HandleFunc("/", requiresAuth(indexHandler))
	http.HandleFunc("/sub", requiresAuth(subHandler))

	port := "5000"
	fmt.Printf("Server is running on http://0.0.0.0:%s\n", port)

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		panic(err)
	}
}
