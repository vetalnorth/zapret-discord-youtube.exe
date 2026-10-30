func scanBatFiles(baseDir, searchDir string) []Strategy {
	files, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}

	var strats []Strategy
	// Ищет winws.exe, опциональную кавычку после него и все аргументы до конца строки
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
				// Нормализуем пути к bin и lists для winws
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
