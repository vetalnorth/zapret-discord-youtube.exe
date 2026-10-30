package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	colReset  = "\033[0m"
	colRed    = "\033[91m"
	colGreen  = "\033[92m"
	colYellow = "\033[93m"
	colCyan   = "\033[96m"
	colGray   = "\033[90m"
	colBold   = "\033[1m"
)

type Strategy struct {
	Name string
	Args []string
}

func initConsole() {
	handle := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err == nil {
		_ = windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
	// Заголовок окна терминала через ANSI-код
	fmt.Print("\033]0;Zapret DPI Controller\007")
}

func main() {
	initConsole()

	fmt.Print("\033[H\033[2J") // Очистка терминала
	fmt.Println(colCyan + colBold + `
  ┌──────────────────────────────────────────────────────────┐
  │                   ZAPRET DPI CONTROLLER                  │
  │     Automated Bypass Suite • Powered by Flowseal Core    │
  └──────────────────────────────────────────────────────────┘` + colReset)

	exePath, err := os.Executable()
	if err != nil {
		fmt.Printf(colRed+"[-] Ошибка определения каталога: %v\n"+colReset, err)
		return
	}
	baseDir := filepath.Dir(exePath)
	winwsPath := filepath.Join(baseDir, "bin", "winws.exe")

	if _, err := os.Stat(winwsPath); os.IsNotExist(err) {
		fmt.Println(colRed + " [!] Ошибка: winws.exe не найден в каталоге bin/" + colReset)
		fmt.Println(colGray + "     Убедитесь, что архив полностью извлечен на диск." + colReset)
		waitExit()
		return
	}

	checkAutoRun()

	// Ищем батники в папке presets или рядом в корне
	presetDir := filepath.Join(baseDir, "presets")
	if _, err := os.Stat(presetDir); os.IsNotExist(err) {
		presetDir = baseDir
	}

	strategies := scanBatFiles(baseDir, presetDir)
	if len(strategies) == 0 {
		fmt.Println(colRed + " [!] Не найдено ни одного файла конфигурации (general*.bat)!" + colReset)
		fmt.Println(colGray + "     Проверьте наличие папки 'presets' рядом с программой." + colReset)
		waitExit()
		return
	}

	fmt.Printf("\n "+colCyan+"•"+colReset+" Загружено пресетов Flowseal: %s%d шт.%s\n", colBold, len(strategies), colReset)
	fmt.Println(" " + colYellow + "•" + colReset + " Начинаем автоподбор рабочей стратегии под вашего провайдера...\n")

	var workingStrat *Strategy

	for i, strat := range strategies {
		displayName := strings.TrimSuffix(strat.Name, ".bat")
		fmt.Printf("   [%s%02d/%02d%s] %-32s ", colCyan, i+1, len(strategies), colReset, displayName)

		stopWinWS()

		cmd := exec.Command(winwsPath, strat.Args...)
		if err := cmd.Start(); err != nil {
			fmt.Println(colRed + "ОШИБКА СТАРТА" + colReset)
			continue
		}

		time.Sleep(1400 * time.Millisecond)

		if testConnection() {
			fmt.Println(colGreen + colBold + "✔ РАБОТАЕТ" + colReset)
			workingStrat = &strat
			break
		} else {
			fmt.Println(colGray + "✖ блокируется" + colReset)
		}
	}

	if workingStrat == nil {
		fmt.Println(colRed + "\n [!] Ни один пресет не открыл доступ к тестовому шлюзу." + colReset)
		stopWinWS()
		waitExit()
		return
	}

	fmt.Println("\n" + colGreen + " ┌──────────────────────────────────────────────────────────┐")
	fmt.Printf(" │  УСПЕШНО АКТИВИРОВАНО: %-33s │\n", workingStrat.Name)
	fmt.Println(" └──────────────────────────────────────────────────────────┘" + colReset)

	suggestAutoRun(exePath)

	fmt.Println(colCyan + "\n [i] Служба активна в фоне. Сверните это окно, пока пользуетесь сетью." + colReset)
	waitExit()
}

func scanBatFiles(baseDir, searchDir string) []Strategy {
	files, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}

	var strats []Strategy
	argRegex := regexp.MustCompile(`(?i)winws\.exe\s+([^\r\n]+)`)

	for _, f := range files {
		if !f.IsDir() && strings.HasPrefix(strings.ToLower(f.Name()), "general") && strings.HasSuffix(strings.ToLower(f.Name()), ".bat") {
			content, err := os.ReadFile(filepath.Join(searchDir, f.Name()))
			if err != nil {
				continue
			}

			matches := argRegex.FindStringSubmatch(string(content))
			if len(matches) > 1 {
				rawArgs := matches[1]
				rawArgs = strings.ReplaceAll(rawArgs, "%~dp0bin\\", filepath.Join(baseDir, "bin")+"\\")
				rawArgs = strings.ReplaceAll(rawArgs, "%~dp0lists\\", filepath.Join(baseDir, "lists")+"\\")
				rawArgs = strings.ReplaceAll(rawArgs, "%~dp0..\\bin\\", filepath.Join(baseDir, "bin")+"\\")
				rawArgs = strings.ReplaceAll(rawArgs, "%~dp0..\\lists\\", filepath.Join(baseDir, "lists")+"\\")
				rawArgs = strings.ReplaceAll(rawArgs, "%~dp0", baseDir+"\\")

				args := parseArgs(rawArgs)
				strats = append(strats, Strategy{
					Name: f.Name(),
					Args: args,
				})
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

func checkAutoRun() {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE)
	if err != nil {
		return
	}
	defer k.Close()

	val, _, err := k.GetStringValue("ZapretLauncher")
	if err == nil && val != "" {
		fmt.Println(" " + colGreen + "✔" + colReset + " Автозагрузка Windows: " + colBold + "ВКЛЮЧЕНА" + colReset)
	}
}

func suggestAutoRun(appPath string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE)
	if err == nil {
		val, _, errVal := k.GetStringValue("ZapretLauncher")
		k.Close()
		if errVal == nil && val != "" {
			return
		}
	}

	fmt.Print("\n " + colYellow + "?" + colReset + " Запускать этот режим автоматически при старте Windows? (y/n): ")
	reader := bufio.NewReader(os.Stdin)
	ans, _ := reader.ReadString('\n')
	ans = strings.ToLower(strings.TrimSpace(ans))

	if ans == "y" || ans == "д" {
		kWrite, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
		if err == nil {
			defer kWrite.Close()
			_ = kWrite.SetStringValue("ZapretLauncher", fmt.Sprintf("\"%s\"", appPath))
			fmt.Println(" " + colGreen + "✔" + colReset + " Успешно прописано в автозапуск системы.")
		}
	}
}

func waitExit() {
	reader := bufio.NewReader(os.Stdin)
	_, _ = reader.ReadString('\n')
}
