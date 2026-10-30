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

// ANSI Цвета
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
)

type Strategy struct {
	Name string
	Args []string
}

func enableANSI() {
	var mode uint32
	handle := windows.Handle(os.Stdout.Fd())
	if err := windows.GetConsoleMode(handle, &mode); err == nil {
		_ = windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
}

func main() {
	enableANSI()

	fmt.Print("\033[H\033[2J") // Очистка экрана
	fmt.Println(colorCyan + colorBold + `
  ███████╗ █████╗ ██████╗ ██████╗ ███████╗████████╗
  ╚══███╔╝██╔══██╗██╔══██╗██╔══██╗██╔════╝╚══██╔══╝
    ███╔╝ ███████║██████╔╝██████╔╝█████╗     ██║   
   ███╔╝  ██╔══██║██╔═══╝ ██╔══██╗██╔══╝     ██║   
  ███████╗██║  ██║██║     ██║  ██║███████╗   ██║   
  ╚══════╝╚═╝  ╚═╝╚═╝     ╚═╝  ╚═╝╚══════╝   ╚═╝   ` + colorReset)
	fmt.Println(colorYellow + "   >>> Launcher by vetalnorth | Core & Presets by Flowseal <<<   " + colorReset)
	fmt.Println("------------------------------------------------------------------")

	exePath, err := os.Executable()
	if err != nil {
		fmt.Printf(colorRed+"[-] Критическая ошибка путей: %v\n"+colorReset, err)
		return
	}
	baseDir := filepath.Dir(exePath)
	winwsPath := filepath.Join(baseDir, "bin", "winws.exe")

	if _, err := os.Stat(winwsPath); os.IsNotExist(err) {
		fmt.Println(colorRed + "[-] winws.exe не обнаружен в папке bin/!" + colorReset)
		fmt.Println("    Убедитесь, что архив распакован полностью.")
		waitExit()
		return
	}

	checkAutoRun()

	strategies := scanLocalBatFiles(baseDir)
	if len(strategies) == 0 {
		fmt.Println(colorRed + "[-] В папке не найдено ни одного батника обхода (general*.bat)!" + colorReset)
		waitExit()
		return
	}

	fmt.Printf("\n[i] Обнаружено готовых стратегий обхода: %s%d%s\n", colorBold, len(strategies), colorReset)
	fmt.Println("[*] Запуск автоматического тестирования под вашего провайдера...\n")

	var workingStrat *Strategy

	for i, strat := range strategies {
		displayName := strings.TrimSuffix(strat.Name, ".bat")
		fmt.Printf(" [%s%d/%d%s] %-30s ", colorCyan, i+1, len(strategies), colorReset, displayName)

		stopWinWS()

		cmd := exec.Command(winwsPath, strat.Args...)
		if err := cmd.Start(); err != nil {
			fmt.Println(colorRed + "[ОШИБКА ЗАПУСКА]" + colorReset)
			continue
		}

		time.Sleep(1400 * time.Millisecond)

		if testConnection() {
			fmt.Println(colorGreen + colorBold + "[РАБОТАЕТ]" + colorReset)
			workingStrat = &strat
			break
		} else {
			fmt.Println(colorRed + "[БЛОКИРУЕТСЯ]" + colorReset)
		}
	}

	if workingStrat == nil {
		fmt.Println(colorRed + "\n[-] Ни одна из доступных стратегий не пробила фильтрацию." + colorReset)
		stopWinWS()
		waitExit()
		return
	}

	fmt.Println("\n---------------------------------------------------------")
	fmt.Printf(colorGreen+colorBold+"[+] УСПЕШНО: активирована стратегия %s\n"+colorReset, workingStrat.Name)
	fmt.Println("---------------------------------------------------------")

	suggestAutoRun(exePath)

	fmt.Println(colorCyan + "\n[•] Служба работает в фоне. Закройте окно для отключения." + colorReset)
	waitExit()
}

func scanLocalBatFiles(baseDir string) []Strategy {
	files, err := os.ReadDir(baseDir)
	if err != nil {
		return nil
	}

	var strats []Strategy
	argRegex := regexp.MustCompile(`(?i)winws\.exe\s+([^\r\n]+)`)

	for _, f := range files {
		if !f.IsDir() && strings.HasPrefix(strings.ToLower(f.Name()), "general") && strings.HasSuffix(strings.ToLower(f.Name()), ".bat") {
			content, err := os.ReadFile(filepath.Join(baseDir, f.Name()))
			if err != nil {
				continue
			}

			matches := argRegex.FindStringSubmatch(string(content))
			if len(matches) > 1 {
				rawArgs := matches[1]
				rawArgs = strings.ReplaceAll(rawArgs, "%~dp0bin\\", filepath.Join(baseDir, "bin")+"\\")
				rawArgs = strings.ReplaceAll(rawArgs, "%~dp0lists\\", filepath.Join(baseDir, "lists")+"\\")
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
		Timeout: 2200 * time.Millisecond,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			DialContext: (&net.Dialer{
				Timeout: 1800 * time.Millisecond,
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
		fmt.Println(colorGreen + "[v] Автозагрузка Windows: включена" + colorReset)
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

	fmt.Print("\n[?] Запускать выбранную конфигурацию при включении ПК? (y/n): ")
	reader := bufio.NewReader(os.Stdin)
	ans, _ := reader.ReadString('\n')
	ans = strings.ToLower(strings.TrimSpace(ans))

	if ans == "y" || ans == "д" {
		kWrite, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
		if err == nil {
			defer kWrite.Close()
			_ = kWrite.SetStringValue("ZapretLauncher", fmt.Sprintf("\"%s\"", appPath))
			fmt.Println(colorGreen + "[+] Запуск добавлен в автозагрузку системы." + colorReset)
		}
	}
}

func waitExit() {
	reader := bufio.NewReader(os.Stdin)
	_, _ = reader.ReadString('\n')
}
