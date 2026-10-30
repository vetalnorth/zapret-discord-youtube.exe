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
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"
)

type Strategy struct {
	Name string   `json:"name"`
	Args []string `json:"args"`
}

var (
	baseDir      string
	winwsPath    string
	workingStrat *Strategy
	allStrats    []Strategy
	mu           sync.Mutex
	srv          *http.Server
	currentCmd   *exec.Cmd
)

var appState = struct {
	Total       int        `json:"total"`
	CurrentIdx  int        `json:"current_idx"`
	CheckName   string     `json:"check_name"`
	ActiveName  string     `json:"active_name"`
	IsRunning   bool       `json:"is_running"`
	IsDone      bool       `json:"is_done"`
	AutoRun     bool       `json:"autorun"`
	GameMode    bool       `json:"gamemode"`
	PingMs      int        `json:"ping_ms"`
	PresetsList []string   `json:"presets_list"`
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
	http.HandleFunc("/api/toggle-gamemode", handleToggleGameMode)
	http.HandleFunc("/api/select-preset", handleSelectPreset)
	http.HandleFunc("/api/rescan", handleRescan)
	http.HandleFunc("/api/stop", handleStop)

	go func() {
		srv = &http.Server{Addr: "127.0.0.1:" + port}
		_ = srv.ListenAndServe()
	}()

	go initEngine()

	time.Sleep(350 * time.Millisecond)
	url := fmt.Sprintf("http://127.0.0.1:%s", port)
	cmd := exec.Command("cmd", "/c", "start", "msedge", "--app="+url, "--window-size=680,680")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Run()

	select {}
}

func initEngine() {
	mu.Lock()
	appState.AutoRun = checkAutoRun()
	mu.Unlock()

	presetDir := filepath.Join(baseDir, "presets")
	if _, err := os.Stat(presetDir); os.IsNotExist(err) {
		presetDir = baseDir
	}

	allStrats = scanBatFiles(baseDir, presetDir)
	
	var names []string
	for _, s := range allStrats {
		names = append(names, strings.TrimSuffix(s.Name, ".bat"))
	}

	mu.Lock()
	appState.PresetsList = names
	appState.Total = len(allStrats)
	mu.Unlock()

	runBenchmarkEngine()
}

func runBenchmarkEngine() {
	mu.Lock()
	appState.IsRunning = true
	appState.IsDone = false
	appState.ActiveName = ""
	mu.Unlock()

	if len(allStrats) == 0 {
		mu.Lock()
		appState.IsDone = true
		appState.IsRunning = false
		mu.Unlock()
		return
	}

	for i, strat := range allStrats {
		mu.Lock()
		if !appState.IsRunning {
			mu.Unlock()
			return
		}
		appState.CurrentIdx = i + 1
		appState.CheckName = strings.TrimSuffix(strat.Name, ".bat")
		mu.Unlock()

		startStrategy(&strat)
		time.Sleep(2000 * time.Millisecond)

		ok, ping := testConnectionDetailed()
		if ok {
			mu.Lock()
			workingStrat = &strat
			appState.ActiveName = strings.TrimSuffix(strat.Name, ".bat")
			appState.PingMs = ping
			appState.IsDone = true
			appState.IsRunning = false
			mu.Unlock()
			return
		}
	}

	mu.Lock()
	appState.IsDone = true
	appState.IsRunning = false
	mu.Unlock()
}

func startStrategy(strat *Strategy) {
	stopWinWS()

	args := make([]string, len(strat.Args))
	copy(args, strat.Args)

	// Если включен игровой режим — добавляем параметры для Discord Voice/UDP
	mu.Lock()
	if appState.GameMode {
		args = append(args, "--filter-udp=50000-65535", "--dpi-desync=fake", "--dpi-desync-any-protocol")
	}
	mu.Unlock()

	currentCmd = exec.Command(winwsPath, args...)
	currentCmd.Dir = filepath.Join(baseDir, "bin")
	currentCmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
	_ = currentCmd.Start()
}

func scanBatFiles(baseDir, searchDir string) []Strategy {
	files, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}

	var strats []Strategy
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
				binDir := filepath.Join(baseDir, "bin")
				listsDir := filepath.Join(baseDir, "lists")

				rawArgs = strings.ReplaceAll(rawArgs, "%~dp0bin\\", binDir+"\\")
				rawArgs = strings.ReplaceAll(rawArgs, "%~dp0lists\\", listsDir+"\\")
				rawArgs = strings.ReplaceAll(rawArgs, "%~dp0..\\bin\\", binDir+"\\")
				rawArgs = strings.ReplaceAll(rawArgs, "%~dp0..\\lists\\", listsDir+"\\")
				rawArgs = strings.ReplaceAll(rawArgs, "%BIN%", binDir+"\\")
				rawArgs = strings.ReplaceAll(rawArgs, "%LISTS%", listsDir+"\\")
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

func testConnectionDetailed() (bool, int) {
	targets := []string{
		"https://www.youtube.com",
		"https://rr1---sn-axq7sn76.googlevideo.com",
		"https://discord.com",
	}

	client := http.Client{
		Timeout: 2500 * time.Millisecond,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			DialContext: (&net.Dialer{
				Timeout: 1800 * time.Millisecond,
			}).DialContext,
		},
	}

	for _, url := range targets {
		start := time.Now()
		resp, err := client.Get(url)
		if err == nil {
			elapsed := int(time.Since(start).Milliseconds())
			_ = resp.Body.Close()
			if resp.StatusCode > 0 && resp.StatusCode < 500 {
				return true, elapsed
			}
		}
	}
	return false, 0
}

func stopWinWS() {
	cmd := exec.Command("taskkill", "/F", "/IM", "winws.exe", "/T")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
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
	_ = json.NewEncoder(w).Encode(appState)
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
			appState.AutoRun = false
		} else {
			_ = k.SetStringValue("ZapretLauncher", fmt.Sprintf("\"%s\"", exePath))
			appState.AutoRun = true
		}
	}
	w.WriteHeader(http.StatusOK)
}

func handleToggleGameMode(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	appState.GameMode = !appState.GameMode
	isGame := appState.GameMode
	strat := workingStrat
	mu.Unlock()

	if strat != nil {
		startStrategy(strat)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"gamemode": isGame})
}

func handleSelectPreset(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		return
	}

	mu.Lock()
	appState.IsRunning = false
	var chosen *Strategy
	for _, s := range allStrats {
		if strings.TrimSuffix(s.Name, ".bat") == name {
			chosen = &s
			break
		}
	}
	mu.Unlock()

	if chosen != nil {
		startStrategy(chosen)
		mu.Lock()
		workingStrat = chosen
		appState.ActiveName = name
		appState.IsDone = true
		mu.Unlock()
	}
	w.WriteHeader(http.StatusOK)
}

func handleRescan(w http.ResponseWriter, r *http.Request) {
	go runBenchmarkEngine()
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
  <title>Zapret Ultimate Controller</title>
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
      background: #090b11;
      font-family: -apple-system, BlinkMacSystemFont, 'SF Pro Display', 'Segoe UI', Roboto, sans-serif;
      color: #f8fafc;
      overflow: hidden;
      display: flex;
      flex-direction: column;
      justify-content: space-between;
      padding: 22px;
      position: relative;
    }

    /* Жидкие цветные сферы */
    .blob {
      position: absolute;
      border-radius: 50%;
      filter: blur(85px);
      z-index: 0;
      opacity: 0.55;
      pointer-events: none;
      animation: float 9s ease-in-out infinite alternate;
    }
    .blob-1 {
      width: 280px;
      height: 280px;
      background: radial-gradient(circle, #0284c7, #6366f1);
      top: -50px;
      left: -50px;
    }
    .blob-2 {
      width: 300px;
      height: 300px;
      background: radial-gradient(circle, #9333ea, #f43f5e);
      bottom: -60px;
      right: -50px;
      animation-delay: -3.5s;
    }
    .blob-3 {
      width: 200px;
      height: 200px;
      background: radial-gradient(circle, #10b981, #06b6d4);
      top: 45%;
      left: 55%;
      opacity: 0.25;
      animation-delay: -6s;
    }
    @keyframes float {
      0% { transform: translate(0, 0) scale(1); }
      100% { transform: translate(35px, 25px) scale(1.12); }
    }

    /* Стеклянный контейнер с неоновым свечением */
    .glass {
      position: relative;
      z-index: 1;
      background: rgba(255, 255, 255, 0.05);
      backdrop-filter: blur(32px) saturate(200%);
      -webkit-backdrop-filter: blur(32px) saturate(200%);
      border: 1px solid rgba(255, 255, 255, 0.15);
      box-shadow: 
        0 24px 48px rgba(0, 0, 0, 0.45),
        inset 0 1px 1px rgba(255, 255, 255, 0.3);
    }

    /* Шапка */
    .header {
      padding: 14px 20px;
      border-radius: 20px;
      display: flex;
      align-items: center;
      justify-content: space-between;
    }
    .title-group h1 {
      font-size: 17px;
      font-weight: 700;
      letter-spacing: 0.4px;
      background: linear-gradient(135deg, #ffffff 40%, rgba(255,255,255,0.65));
      -webkit-background-clip: text;
      -webkit-text-fill-color: transparent;
    }
    .title-group p {
      font-size: 11px;
      color: rgba(255, 255, 255, 0.55);
      margin-top: 1px;
    }
    .ping-badge {
      display: flex;
      align-items: center;
      gap: 6px;
      background: rgba(255, 255, 255, 0.08);
      border: 1px solid rgba(255, 255, 255, 0.18);
      padding: 6px 12px;
      border-radius: 30px;
      font-size: 11px;
      font-weight: 600;
      color: #38bdf8;
    }
    .ping-dot {
      width: 7px;
      height: 7px;
      border-radius: 50%;
      background: #38bdf8;
    }
    .ping-dot.online {
      background: #10b981;
      box-shadow: 0 0 8px #10b981;
    }

    /* Главная карточка */
    .main-card {
      padding: 26px 22px;
      border-radius: 24px;
      display: flex;
      flex-direction: column;
      align-items: center;
      text-align: center;
      margin: 12px 0;
    }

    .radar-wrapper {
      position: relative;
      width: 86px;
      height: 86px;
      display: flex;
      align-items: center;
      justify-content: center;
      margin-bottom: 16px;
    }
    .radar-core {
      width: 68px;
      height: 68px;
      border-radius: 50%;
      background: linear-gradient(135deg, rgba(255,255,255,0.18), rgba(255,255,255,0.02));
      border: 1.5px solid rgba(255, 255, 255, 0.3);
      box-shadow: 0 10px 25px rgba(0, 0, 0, 0.35), inset 0 1px 2px rgba(255, 255, 255, 0.5);
      display: flex;
      align-items: center;
      justify-content: center;
    }
    .radar-ring {
      position: absolute;
      inset: -5px;
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
      50% { transform: scale(1.08); opacity: 1; }
    }

    .status-caption {
      font-size: 13px;
      color: rgba(255, 255, 255, 0.6);
      margin-bottom: 6px;
    }
    .current-strat {
      font-size: 19px;
      font-weight: 700;
      color: #ffffff;
      min-height: 26px;
    }
    .current-strat.active-success {
      background: linear-gradient(135deg, #4ade80, #22d3ee);
      -webkit-background-clip: text;
      -webkit-text-fill-color: transparent;
    }

    /* Стеклянный блок настроек */
    .controls-grid {
      width: 100%;
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 12px;
      margin-top: 18px;
    }
    .control-item {
      background: rgba(255, 255, 255, 0.04);
      border: 1px solid rgba(255, 255, 255, 0.1);
      border-radius: 16px;
      padding: 12px 14px;
      display: flex;
      align-items: center;
      justify-content: space-between;
      cursor: pointer;
      transition: all 0.25s ease;
    }
    .control-item:hover {
      background: rgba(255, 255, 255, 0.08);
      border-color: rgba(255, 255, 255, 0.2);
    }
    .control-item span {
      font-size: 12px;
      font-weight: 500;
      color: rgba(255, 255, 255, 0.85);
    }

    /* iOS Свитч */
    .switch {
      width: 38px;
      height: 22px;
      background: rgba(255, 255, 255, 0.15);
      border-radius: 12px;
      position: relative;
      transition: 0.3s cubic-bezier(0.2, 0.8, 0.2, 1);
    }
    .switch::after {
      content: '';
      position: absolute;
      width: 16px;
      height: 16px;
      background: #ffffff;
      border-radius: 50%;
      top: 3px;
      left: 3px;
      transition: 0.3s cubic-bezier(0.2, 0.8, 0.2, 1);
      box-shadow: 0 2px 4px rgba(0,0,0,0.4);
    }
    .switch.active {
      background: linear-gradient(135deg, #0ea5e9, #38bdf8);
      box-shadow: 0 0 14px rgba(14, 165, 233, 0.6);
    }
    .switch.active::after {
      left: 19px;
    }

    /* Селектор пресета вручную */
    .select-wrapper {
      width: 100%;
      margin-top: 12px;
      display: flex;
      align-items: center;
      gap: 10px;
    }
    .glass-select {
      flex: 1;
      background: rgba(18, 22, 32, 0.7);
      border: 1px solid rgba(255, 255, 255, 0.15);
      color: #f8fafc;
      padding: 10px 14px;
      border-radius: 14px;
      font-size: 13px;
      font-weight: 500;
      outline: none;
      backdrop-filter: blur(20px);
      cursor: pointer;
    }
    .glass-select option {
      background: #11141e;
      color: #f8fafc;
    }
    .btn-action {
      background: rgba(56, 189, 248, 0.12);
      border: 1px solid rgba(56, 189, 248, 0.3);
      color: #38bdf8;
      padding: 10px 16px;
      border-radius: 14px;
      font-size: 12px;
      font-weight: 600;
      cursor: pointer;
      transition: all 0.2s ease;
      white-space: nowrap;
    }
    .btn-action:hover {
      background: rgba(56, 189, 248, 0.25);
      color: #ffffff;
    }

    /* Подвал */
    .footer {
      padding: 12px 18px;
      border-radius: 18px;
      display: flex;
      align-items: center;
      justify-content: space-between;
    }
    .footer-info {
      font-size: 11px;
      color: rgba(255, 255, 255, 0.45);
    }
    .btn-quit {
      background: rgba(239, 68, 68, 0.12);
      border: 1px solid rgba(239, 68, 68, 0.25);
      color: #fca5a5;
      padding: 8px 16px;
      border-radius: 12px;
      font-size: 12px;
      font-weight: 600;
      cursor: pointer;
      transition: 0.2s ease;
    }
    .btn-quit:hover {
      background: rgba(239, 68, 68, 0.25);
      color: #ffffff;
    }
  </style>
</head>
<body>
  <div class="blob blob-1"></div>
  <div class="blob blob-2"></div>
  <div class="blob blob-3"></div>

  <div class="header glass">
    <div class="title-group">
      <h1>ZAPRET ULTIMATE</h1>
      <p>Flowseal Core Engine • Glassmorphism Edition</p>
    </div>
    <div class="ping-badge">
      <div class="ping-dot" id="pingDot"></div>
      <span id="pingText">Пинг: -- ms</span>
    </div>
  </div>

  <div class="main-card glass">
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

    <div class="controls-grid">
      <div class="control-item" onclick="toggleGameMode()">
        <span>Игровой режим (Discord Voice / UDP)</span>
        <div class="switch" id="gameSwitch"></div>
      </div>
      <div class="control-item" onclick="toggleAutorun()">
        <span>Автозапуск Windows</span>
        <div class="switch" id="autoSwitch"></div>
      </div>
    </div>

    <div class="select-wrapper">
      <select class="glass-select" id="presetSelect" onchange="selectManualPreset(this.value)">
        <option value="">Выбрать пресет вручную...</option>
      </select>
      <button class="btn-action" onclick="rescan()">Пересканировать</button>
    </div>
  </div>

  <div class="footer glass">
    <div class="footer-info">Управляется автоматически • Закройте для сворачивания</div>
    <button class="btn-quit" onclick="quitApp()">Отключить</button>
  </div>

  <script>
    let listPopulated = false;

    async function update() {
      try {
        const res = await fetch('/api/status');
        const d = await res.json();

        // Заполнение списка пресетов один раз
        if (!listPopulated && d.presets_list && d.presets_list.length > 0) {
          const sel = document.getElementById('presetSelect');
          d.presets_list.forEach(p => {
            const opt = document.createElement('option');
            opt.value = p;
            opt.innerText = p;
            sel.appendChild(opt);
          });
          listPopulated = true;
        }

        const ring = document.getElementById('radarRing');
        const title = document.getElementById('statusTitle');
        const strat = document.getElementById('statusStrat');
        const pingDot = document.getElementById('pingDot');
        const pingText = document.getElementById('pingText');

        if (d.ping_ms > 0) {
          pingDot.className = 'ping-dot online';
          pingText.innerText = 'Шлюз: ' + d.ping_ms + ' ms';
        }

        if (d.is_running) {
          title.innerText = 'Проверка стратегии (' + d.current_idx + '/' + d.total + ')...';
          strat.innerText = d.check_name;
          strat.className = 'current-strat';
          ring.className = 'radar-ring';
        } else if (d.is_done && d.active_name) {
          title.innerText = 'Обход успешно активен:';
          strat.innerText = d.active_name;
          strat.className = 'current-strat active-success';
          ring.className = 'radar-ring success';
          document.getElementById('presetSelect').value = d.active_name;
        } else if (d.is_done && !d.active_name) {
          title.innerText = 'Внимание';
          strat.innerText = 'Ни один пресет не подошел';
          strat.style.color = '#f87171';
        }

        const autoSw = document.getElementById('autoSwitch');
        if (d.autorun) { autoSw.classList.add('active'); } else { autoSw.classList.remove('active'); }

        const gameSw = document.getElementById('gameSwitch');
        if (d.gamemode) { gameSw.classList.add('active'); } else { gameSw.classList.remove('active'); }
      } catch(e) {}
    }

    async function toggleAutorun() {
      await fetch('/api/toggle-autorun');
      update();
    }

    async function toggleGameMode() {
      await fetch('/api/toggle-gamemode');
      update();
    }

    async function selectManualPreset(name) {
      if (!name) return;
      await fetch('/api/select-preset?name=' + encodeURIComponent(name));
      update();
    }

    async function rescan() {
      await fetch('/api/rescan');
      update();
    }

    async function quitApp() {
      await fetch('/api/stop');
      window.close();
    }

    setInterval(update, 800);
    update();
  </script>
</body>
</html>`
