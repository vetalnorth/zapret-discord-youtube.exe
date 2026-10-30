package main

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/windows/registry"
)

type GitHubContent struct {
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
	Type        string `json:"type"`
}

type Strategy struct {
	Name string
	Args []string
}

func main() {
	fmt.Println("==================================================")
	fmt.Println("       Zapret Dynamic Launcher (Windows)          ")
	fmt.Println("  Auto-Sync from Flowseal, Benchmark & Auto-Run   ")
	fmt.Println("==================================================")

	exePath, err := os.Executable()
	if err != nil {
		fmt.Printf("[-] Ошибка путей: %v\n", err)
		return
	}
	baseDir := filepath.Dir(exePath)
	winwsPath := filepath.Join(baseDir, "bin", "winws.exe")

	if _, err := os.Stat(winwsPath); os.IsNotExist(err) {
		fmt.Println("[-] Ошибка: папка bin или файл winws.exe не найдены рядом с лаунчером!")
		waitExit()
		return
	}

	// 1. Проверяем автозапуск в реестре
	checkAutoRun()

	// 2. Синхронизируем стратегии напрямую из репозитория Flowseal
	strategies := fetchUpstreamStrategies(baseDir)
	if len(strategies) == 0 {
		fmt.Println("[!] Не удалось загрузить батники с GitHub, используем резервные стратегии...")
		strategies = getFallbackStrategies(baseDir)
	}

	// 3. Автоподбор рабочей стратегии
	fmt.Println("\n[*] Начинаем автоподбор рабочей конфигурации...")
	var workingStrat *Strategy

	for i, strat := range strategies {
		fmt.Printf("[%d/%d] Проверяем: %s... ", i+1, len(strategies), strat.Name)
		stopWinWS()

		cmd := exec.Command(winwsPath, strat.Args...)
		if err := cmd.Start(); err != nil {
			fmt.Println("ошибка старта")
			continue
		}

		time.Sleep(1500 * time.Millisecond)

		if testConnection() {
			fmt.Println("РАБОТАЕТ!")
			workingStrat = &strat
			break
		} else {
			fmt.Println("блокируется")
		}
	}

	if workingStrat == nil {
		fmt.Println("\n[-] Ни один вариант не дал стабильного подключения.")
		stopWinWS()
		waitExit()
		return
	}

	fmt.Printf("\n[+] Оптимальная конфигурация найдена и активна: %s\n", workingStrat.Name)

	// 4. Предложение автозапуска
	suggestAutoRun(exePath)

	fmt.Println("\n[i] Лаунчер работает в фоне. Для остановки закройте это окно.")
	waitExit()
}

func fetchUpstreamStrategies(baseDir string) []Strategy {
	fmt.Println("[*] Проверка обновлений и батников в репозитории Flowseal...")

	client := http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/Flowseal/zapret-discord-youtube/contents", nil)
	req.Header.Set("User-Agent", "Zapret-Auto-Launcher")

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var contents []GitHubContent
	if err := json.NewDecoder(resp.Body).Decode(&contents); err != nil {
		return nil
	}

	var list []Strategy
	argRegex := regexp.MustCompile(`winws\.exe\s+([^\r\n]+)`)

	for _, item := range contents {
		if item.Type == "file" && strings.HasPrefix(item.Name, "general") && strings.HasSuffix(item.Name, ".bat") {
			rawResp, err := client.Get(item.DownloadURL)
			if err != nil {
				continue
			}
			bodyBytes, _ := io.ReadAll(rawResp.Body)
			rawResp.Body.Close()

			matches := argRegex.FindStringSubmatch(string(bodyBytes))
			if len(matches) > 1 {
				rawArgs := matches[1]
				// Подставляем локальные пути вместо относительных путей автора
				rawArgs = strings.ReplaceAll(rawArgs, "%~dp0bin\\", filepath.Join(baseDir, "bin")+"\\")
				rawArgs = strings.ReplaceAll(rawArgs, "%~dp0lists\\", filepath.Join(baseDir, "lists")+"\\")
				rawArgs = strings.ReplaceAll(rawArgs, "\"%~dp0", "\""+baseDir+"\\")

				args := parseArgs(rawArgs)
				list = append(list, Strategy{
					Name: item.Name,
					Args: args,
				})
			}
		}
	}

	if len(list) > 0 {
		fmt.Printf("[+] Успешно получено батников от автора: %d шт.\n", len(list))
	}
	return list
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

func getFallbackStrategies(baseDir string) []Strategy {
	return []Strategy{
		{
			Name: "Default Fallback General",
			Args: []string{
				"--wf-raw=@" + filepath.Join(baseDir, "bin", "windivert.filter"),
				"--wf-tcp=80,443", "--wf-udp=443,50000-65535",
				"--filter-udp=443", "--hostlist=" + filepath.Join(baseDir, "lists", "list-general.txt"),
				"--dpi-desync=fake", "--dpi-desync-repeats=6",
				"--new", "--filter-tcp=443", "--hostlist=" + filepath.Join(baseDir, "lists", "list-general.txt"),
				"--dpi-desync=fake,split2", "--dpi-desync-autottl=2",
			},
		},
	}
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
		fmt.Println("[+] Статус автозагрузки Windows: ВКЛЮЧЕН")
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

	fmt.Print("\n[?] Запускать этот обход автоматически при старте Windows? (y/n): ")
	reader := bufio.NewReader(os.Stdin)
	ans, _ := reader.ReadString('\n')
	ans = strings.ToLower(strings.TrimSpace(ans))

	if ans == "y" || ans == "д" {
		kWrite, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
		if err == nil {
			defer kWrite.Close()
			_ = kWrite.SetStringValue("ZapretLauncher", fmt.Sprintf("\"%s\"", appPath))
			fmt.Println("[+] Добавлено в автозапуск Windows.")
		}
	}
}

func waitExit() {
	reader := bufio.NewReader(os.Stdin)
	_, _ = reader.ReadString('\n')
}
