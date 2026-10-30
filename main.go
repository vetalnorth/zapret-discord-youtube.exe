package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	fmt.Println("==================================================")
	fmt.Println("         Zapret Launcher (Windows)                ")
	fmt.Println("    Lightweight Runner for winws by Flowseal      ")
	fmt.Println("==================================================")

	exePath, err := os.Executable()
	if err != nil {
		fmt.Printf("[-] Ошибка определения пути: %v\n", err)
		return
	}
	baseDir := filepath.Dir(exePath)
	winwsPath := filepath.Join(baseDir, "bin", "winws.exe")

	if _, err := os.Stat(winwsPath); os.IsNotExist(err) {
		fmt.Printf("[-] Внимание: %s не найден рядом с лаунчером!\n", winwsPath)
		fmt.Println("[-] Убедитесь, что папка bin находится в одной директории с файлом.")
	} else {
		fmt.Println("[+] Ядро winws.exe успешно обнаружено.")
	}

	strategies := map[string]struct {
		name string
		args []string
	}{
		"1": {
			name: "General (Стандартная Flowseal)",
			args: []string{
				"--wf-raw=@" + filepath.Join(baseDir, "bin", "windivert.filter"),
				"--wf-tcp=80,443",
				"--wf-udp=443,50000-65535",
				"--filter-udp=443",
				"--hostlist=" + filepath.Join(baseDir, "lists", "list-general.txt"),
				"--dpi-desync=fake",
				"--dpi-desync-repeats=6",
				"--new",
				"--filter-tcp=443",
				"--hostlist=" + filepath.Join(baseDir, "lists", "list-general.txt"),
				"--dpi-desync=fake,split2",
				"--dpi-desync-autottl=2",
			},
		},
		"2": {
			name: "General (ALT 1 - Сплит)",
			args: []string{
				"--wf-raw=@" + filepath.Join(baseDir, "bin", "windivert.filter"),
				"--wf-tcp=80,443",
				"--wf-udp=443,50000-65535",
				"--filter-tcp=443",
				"--hostlist=" + filepath.Join(baseDir, "lists", "list-general.txt"),
				"--dpi-desync=split2",
				"--dpi-desync-split-pos=2",
			},
		},
		"3": {
			name: "Discord Voice + YouTube",
			args: []string{
				"--wf-raw=@" + filepath.Join(baseDir, "bin", "windivert.filter"),
				"--wf-tcp=80,443",
				"--wf-udp=443,50000-65535",
				"--filter-udp=50000-65535",
				"--dpi-desync=fake",
				"--dpi-desync-any-protocol",
				"--new",
				"--filter-tcp=443",
				"--hostlist=" + filepath.Join(baseDir, "lists", "list-general.txt"),
				"--dpi-desync=fake,split2",
			},
		},
	}

	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Println("\nВыберите режим:")
		fmt.Println("1. Запустить: General (Стандартная)")
		fmt.Println("2. Запустить: General (ALT 1)")
		fmt.Println("3. Запустить: Discord Voice + YouTube")
		fmt.Println("S. Остановить все процессы winws")
		fmt.Println("Q. Выход")
		fmt.Print("Ввод: ")

		input, _ := reader.ReadString('\n')
		choice := strings.TrimSpace(input)

		switch strings.ToUpper(choice) {
		case "1", "2", "3":
			strat := strategies[choice]
			stopWinWS()
			fmt.Printf("\n[>] Запуск стратегии: %s\n", strat.name)

			cmd := exec.Command(winwsPath, strat.args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			if err := cmd.Start(); err != nil {
				fmt.Printf("[-] Ошибка запуска: %v\n", err)
			} else {
				fmt.Println("[+] Процесс запущен в фоне. Нажмите S в меню для остановки.")
			}
		case "S":
			stopWinWS()
		case "Q":
			return
		default:
			fmt.Println("[-] Неверный выбор.")
		}
	}
}

func stopWinWS() {
	fmt.Println("[*] Завершение запущенных процессов winws.exe...")
	cmd := exec.Command("taskkill", "/F", "/IM", "winws.exe", "/T")
	_ = cmd.Run()
	fmt.Println("[+] Готово.")
}
