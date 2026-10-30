package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows/registry"
)

type Strategy struct {
	Name string
	Args []string
}

var (
	baseDir      string
	winwsPath    string
	workingStrat *Strategy
	mu           sync.Mutex
	srv          *http.Server
)

var currentProgress = struct {
	Total      int    `json:"total"`
	CurrentIdx int    `json:"current_idx"`
	CheckName  string `json:"check_name"`
	ActiveName string `json:"active_name"`
	IsRunning  bool   `json:"is_running"`
	IsDone     bool   `json:"is_done"`
	AutoRun    bool   `json:"autorun"`
}{}

func main() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	baseDir = filepath.Dir(exePath)
	winwsPath = filepath.Join(baseDir, "bin", "winws.exe")

	port := "18923"
	http.HandleFunc("/", serveUI)
	http.HandleFunc("/api/status", handleStatus)
	http.HandleFunc("/api/toggle-autorun", handleToggleAutorun)
	http.HandleFunc("/api/stop", handleStop)

	go func() {
		srv = &http.Server{Addr: "127.0.0.1:" + port}
		_ = srv.ListenAndServe()
	}()

	// Запуск движка поиска и перебора стратегий в отдельном потоке
	go runBenchmarkEngine()

	// Запуск бесшовного приложения без рамки браузера
	time.Sleep(350 * time.Millisecond)
	url := fmt.Sprintf("http://127.0.0.1:%s", port)
	cmd := exec.Command("cmd", "/c", "start", "msedge", "--app="+url, "--window-size=620,530")
	_ = cmd.Run()

	select {}
}

func runBenchmarkEngine() {
	mu.Lock()
	currentProgress.AutoRun = checkAutoRun()
	mu.Unlock()

	// Приоритетно сканируем подпапку presets, если нет — корень
	presetDir := filepath.Join(baseDir, "presets")
	if _, err := os.Stat(presetDir); os.IsNotExist(err) {
		presetDir = baseDir
	}

	strategies := scanBatFiles(baseDir, presetDir)
	mu.Lock()
	currentProgress.Total = len(strategies)
	currentProgress.IsRunning = true
	mu.Unlock()

	if len(strategies) == 0 {
		mu.Lock()
		currentProgress.IsDone = true
		currentProgress.IsRunning = false
		mu.Unlock()
		return
	}

	for i, strat := range strategies {
		mu.Lock()
		currentProgress.CurrentIdx = i + 1
		currentProgress.CheckName = strings.TrimSuffix(strat.Name, ".bat")
		mu.Unlock()

		stopWinWS()

		cmd := exec.Command(winwsPath, strat.Args...)
		if err := cmd.Start(); err != nil {
			continue
		}

		time.Sleep(1400 * time.Millisecond)

		if testConnection() {
			mu.Lock()
			workingStrat = &strat
			currentProgress.ActiveName = strings.TrimSuffix(strat.Name, ".bat")
			currentProgress.IsDone = true
			currentProgress.IsRunning = false
			mu.Unlock()
			return
		}
	}

	mu.Lock()
	currentProgress.IsDone = true
	currentProgress.IsRunning = false
	mu.Unlock()
}

func scanBatFiles(baseDir, searchDir string) []Strategy {
	files, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}

	var strats []Strategy
	// Ловит вызовы winws с любыми кавычками, пробелами и путями
	argRegex := regexp.MustCompile(`(?i)winws(?:\.exe)?"?\s+([^\r\n]+)`)

	for _, f := range files {
		name := strings.ToLower(f.Name())
		if !f.IsDir() && strings.HasPrefix(name, "general") && strings.HasSuffix(name, ".bat") {
			content, err := os.ReadFile(filepath.Join(searchDir, f.Name()))
			if err != nil {
				continue
			}

			matches := argRegex.FindStringSubmatch(string(content))
			if len(matches) > 1 {
				rawArgs := matches[1]
				// Заменяем шаблоны путей на абсолютные пути к bin и lists
				rawArgs = strings.ReplaceAll(rawArgs, "%~dp0bin\\", filepath.Join(baseDir, "bin")+"\\")
				rawArgs = strings.ReplaceAll(rawArgs, "%~dp0lists\\", filepath.Join(baseDir, "lists")+"\\")
				rawArgs = strings.ReplaceAll(rawArgs, "%~dp0..\\bin\\", filepath.Join(baseDir, "bin")+"\\")
				rawArgs = strings.ReplaceAll(rawArgs, "%~dp0..\\lists\\", filepath.Join(baseDir, "lists")+"\\")
				rawArgs = strings.ReplaceAll(rawArgs, "%BIN%", filepath.Join(baseDir, "bin")+"\\")
				rawArgs = strings.ReplaceAll(rawArgs, "%LISTS%", filepath.Join(baseDir, "lists")+"\\")
				rawArgs = strings.ReplaceAll(rawArgs, "%~dp0", baseDir+"\\")

				args := parseArgs(rawArgs)
				if len(args) > 0 {
					strats = append(strats, Strategy{
						Name: f.Name(),
						Args: args,
					})
				}
			}
		}
	}
	return strats
}

func parseArgs(raw string) []string {
	var args []string
	var current strings.Builder
	inQuotes := false

	for _, r := range raw {
		switch {
		case r == '"':
			inQuotes = !inQuotes
		case r == ' ' && !inQuotes:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

func testConnection() bool {
	client := http.Client{
		Timeout: 2000 * time.Millisecond,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			DialContext: (&net.Dialer{
				Timeout: 1500 * time.Millisecond,
			}).DialContext,
		},
	}

	resp, err := client.Get("https://www.youtube.com")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200 || resp.StatusCode == 301 || resp.StatusCode == 302
}

func stopWinWS() {
	cmd := exec.Command("taskkill", "/F", "/IM", "winws.exe", "/T")
	_ = cmd.Run()
}

func checkAutoRun() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	val, _, err := k.GetStringValue("ZapretLauncher")
	return err == nil && val != ""
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(currentProgress)
}

func handleToggleAutorun(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	exePath, _ := os.Executable()
	isCurrent := checkAutoRun()

	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err == nil {
		defer k.Close()
		if isCurrent {
			_ = k.DeleteValue("ZapretLauncher")
			currentProgress.AutoRun = false
		} else {
			_ = k.SetStringValue("ZapretLauncher", fmt.Sprintf("\"%s\"", exePath))
			currentProgress.AutoRun = true
		}
	}
	w.WriteHeader(http.StatusOK)
}

func handleStop(w http.ResponseWriter, r *http.Request) {
	stopWinWS()
	os.Exit(0)
}

func serveUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, htmlUI)
}

const htmlUI = `<!DOCTYPE html>
<html lang="ru">
<head>
  <meta charset="UTF-8">
  <title>Zapret DPI Controller</title>
  <style>
    * {
      box-sizing: border-box;
      margin: 0;
      padding: 0;
      user-select: none;
      -webkit-user-drag: none;
    }

    body {
      height: 100vh;
      background: #090b10;
      font-family: -apple-system, BlinkMacSystemFont, 'SF Pro Display', 'Segoe UI', Roboto, sans-serif;
      color: #f8fafc;
      overflow: hidden;
      display: flex;
      flex-direction: column;
      justify-content: space-between;
      padding: 24px;
      position: relative;
    }

    /* Фоновые жидкие световые сферы */
    .blob {
      position: absolute;
      border-radius: 50%;
      filter: blur(80px);
      z-index: 0;
      opacity: 0.55;
      pointer-events: none;
      animation: float 8s ease-in-out infinite alternate;
    }
    .blob-1 {
      width: 260px;
      height: 260px;
      background: radial-gradient(circle, #38bdf8, #6366f1);
      top: -40px;
      left: -40px;
    }
    .blob-2 {
      width: 280px;
      height: 280px;
      background: radial-gradient(circle, #a855f7, #ec4899);
      bottom: -60px;
      right: -40px;
      animation-delay: -3s;
    }
    .blob-3 {
      width: 180px;
      height: 180px;
      background: radial-gradient(circle, #10b981, #06b6d4);
      top: 40%;
      left: 60%;
      opacity: 0.3;
      animation-delay: -5s;
    }
    @keyframes float {
      0% { transform: translate(0, 0) scale(1); }
      100% { transform: translate(30px, 20px) scale(1.1); }
    }

    /* Стеклянные панели Liquid Glass */
    .glass-panel {
      position: relative;
      z-index: 1;
      background: rgba(255, 255, 255, 0.06);
      backdrop-filter: blur(28px) saturate(190%);
      -webkit-backdrop-filter: blur(28px) saturate(190%);
      border: 1px solid rgba(255, 255, 255, 0.14);
      box-shadow: 
        0 20px 40px rgba(0, 0, 0, 0.4),
        inset 0 1px 1px rgba(255, 255, 255, 0.25);
    }

    .header {
      padding: 14px 18px;
      border-radius: 18px;
      display: flex;
      align-items: center;
      justify-content: space-between;
    }
    .title-group h1 {
      font-size: 16px;
      font-weight: 700;
      letter-spacing: 0.3px;
      background: linear-gradient(135deg, #ffffff 40%, rgba(255,255,255,0.6));
      -webkit-background-clip: text;
      -webkit-text-fill-color: transparent;
    }
    .title-group p {
      font-size: 11px;
      color: rgba(255, 255, 255, 0.55);
      margin-top: 1px;
    }
    .badge {
      background: rgba(255, 255, 255, 0.08);
      border: 1px solid rgba(255, 255, 255, 0.18);
      padding: 5px 12px;
      border-radius: 30px;
      font-size: 11px;
      font-weight: 600;
      letter-spacing: 0.2px;
      color: #e0f2fe;
      box-shadow: inset 0 1px 1px rgba(255, 255, 255, 0.2);
    }

    .main-card {
      padding: 34px 24px;
      border-radius: 26px;
      display: flex;
      flex-direction: column;
      align-items: center;
      text-align: center;
      margin: auto 0;
    }

    .radar-wrapper {
      position: relative;
      width: 90px;
      height: 90px;
      display: flex;
      align-items: center;
      justify-content: center;
      margin-bottom: 22px;
    }
    .radar-core {
      width: 72px;
      height: 72px;
      border-radius: 50%;
      background: linear-gradient(135deg, rgba(255,255,255,0.18), rgba(255,255,255,0.02));
      border: 1.5px solid rgba(255, 255, 255, 0.3);
      box-shadow: 
        0 10px 25px rgba(0, 0, 0, 0.3),
        inset 0 1px 2px rgba(255, 255, 255, 0.5);
      position: relative;
      display: flex;
      align-items: center;
      justify-content: center;
    }
    .radar-ring {
      position: absolute;
      inset: -6px;
      border-radius: 50%;
      border: 2px solid transparent;
      border-top-color: #38bdf8;
      border-right-color: #818cf8;
      animation: spin 1.2s cubic-bezier(0.5, 0.1, 0.5, 0.9) infinite;
    }
    .radar-ring.success {
      border-color: #10b981;
      box-shadow: 0 0 24px rgba(16, 185, 129, 0.6);
      animation: pulse-ring 2.2s infinite ease-in-out;
    }
    @keyframes spin { 100% { transform: rotate(360deg); } }
    @keyframes pulse-ring {
      0%, 100% { transform: scale(1); opacity: 0.8; }
      50% { transform: scale(1.1); opacity: 1; }
    }

    .status-caption {
      font-size: 13px;
      font-weight: 500;
      color: rgba(255, 255, 255, 0.6);
      margin-bottom: 8px;
    }
    .current-strat {
      font-size: 21px;
      font-weight: 700;
      color: #ffffff;
      min-height: 28px;
      letter-spacing: -0.2px;
      text-shadow: 0 2px 10px rgba(0,0,0,0.4);
    }
    .current-strat.active-success {
      background: linear-gradient(135deg, #4ade80, #22d3ee);
      -webkit-background-clip: text;
      -webkit-text-fill-color: transparent;
      text-shadow: none;
    }

    .footer {
      padding: 12px 18px;
      border-radius: 18px;
      display: flex;
      align-items: center;
      justify-content: space-between;
    }
    .autorun-group {
      display: flex;
      align-items: center;
      gap: 12px;
      font-size: 13px;
      font-weight: 500;
      color: rgba(255, 255, 255, 0.85);
      cursor: pointer;
    }

    .switch {
      width: 44px;
      height: 26px;
      background: rgba(255, 255, 255, 0.12);
      border: 1px solid rgba(255, 255, 255, 0.18);
      border-radius: 14px;
      position: relative;
      transition: all 0.35s cubic-bezier(0.2, 0.8, 0.2, 1);
      box-shadow: inset 0 1px 2px rgba(0, 0, 0, 0.3);
    }
    .switch::after {
      content: '';
      position: absolute;
      width: 20px;
      height: 20px;
      background: #ffffff;
      border-radius: 50%;
      top: 2px;
      left: 2px;
      box-shadow: 0 2px 6px rgba(0, 0, 0, 0.35);
      transition: all 0.35s cubic-bezier(0.2, 0.8, 0.2, 1);
    }
    .switch.active {
      background: linear-gradient(135deg, #0ea5e9, #38bdf8);
      border-color: rgba(255, 255, 255, 0.4);
      box-shadow: 0 0 16px rgba(14, 165, 233, 0.5);
    }
    .switch.active::after {
      left: 20px;
    }

    .btn-quit {
      background: rgba(239, 68, 68, 0.12);
      border: 1px solid rgba(239, 68, 68, 0.25);
      color: #fca5a5;
      padding: 7px 15px;
      border-radius: 12px;
      font-size: 12px;
      font-weight: 600;
      cursor: pointer;
      backdrop-filter: blur(10px);
      box-shadow: inset 0 1px 1px rgba(255, 255, 255, 0.15);
      transition: all 0.2s ease;
    }
    .btn-quit:hover {
      background: rgba(239, 68, 68, 0.24);
      border-color: rgba(239, 68, 68, 0.4);
      color: #ffffff;
    }
  </style>
</head>
<body>
  <div class="blob blob-1"></div>
  <div class="blob blob-2"></div>
  <div class="blob blob-3"></div>

  <div class="header glass-panel">
    <div class="title-group">
      <h1>ZAPRET CONTROLLER</h1>
      <p>Automated Bypass Suite • Powered by Flowseal Core</p>
    </div>
    <div class="badge" id="badgeTotal">Загрузка...</div>
  </div>

  <div class="main-card glass-panel">
    <div class="radar-wrapper">
      <div class="radar-ring" id="radarRing"></div>
      <div class="radar-core">
        <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="color: rgba(255,255,255,0.85);">
          <path d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83"/>
        </svg>
      </div>
    </div>
    <div class="status-caption" id="statusTitle">Подбор рабочей стратегии...</div>
    <div class="current-strat" id="statusStrat">Инициализация</div>
  </div>

  <div class="footer glass-panel">
    <div class="autorun-group" onclick="toggleAutorun()">
      <div class="switch" id="toggleSwitch"></div>
      <span>Автозапуск при старте Windows</span>
    </div>
    <button class="btn-quit" onclick="quitApp()">Отключить</button>
  </div>

  <script>
    async function update() {
      try {
        const res = await fetch('/api/status');
        const d = await res.json();

        document.getElementById('badgeTotal').innerText = d.total + ' пресетов';

        const ring = document.getElementById('radarRing');
        const title = document.getElementById('statusTitle');
        const strat = document.getElementById('statusStrat');

        if (d.is_running) {
          title.innerText = 'Проверка пресета (' + d.current_idx + '/' + d.total + ')...';
          strat.innerText = d.check_name;
          strat.className = 'current-strat';
          ring.className = 'radar-ring';
        } else if (d.is_done && d.active_name) {
          title.innerText = 'Обход активирован:';
          strat.innerText = d.active_name;
          strat.className = 'current-strat active-success';
          ring.className = 'radar-ring success';
        } else if (d.is_done && !d.active_name) {
          title.innerText = 'Внимание';
          strat.innerText = 'Ни один пресет не подошел';
          strat.style.color = '#f87171';
        }

        const t = document.getElementById('toggleSwitch');
        if (d.autorun) { t.classList.add('active'); } else { t.classList.remove('active'); }
      } catch(e) {}
    }

    async function toggleAutorun() {
      await fetch('/api/toggle-autorun');
      update();
    }

    async function quitApp() {
      await fetch('/api/stop');
      window.close();
    }

    setInterval(update, 700);
    update();
  </script>
</body>
</html>`
