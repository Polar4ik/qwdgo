package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

const (
	defaultConfigName = "config.txt"
	deviceIDFileName  = "device.id"
)

const exampleConfigText = `# Конфиг консольного клиента (тот же протокол, что у Android-приложения).
# Строки с # — комментарии. Ключи можно писать с дефисом или подчёркиванием.
# Запуск: vk-turn.exe
# Или:    vk-turn.exe -config путь\к\config.txt
# Флаги командной строки перекрывают значения из файла.

# Обязательные
peer = 1.2.3.4:56000
vk = HASH_ЗВОНКА
password = пароль_подключения

# На ПК удобнее SOCKS5 (userspace WireGuard, без системного TUN).
# vpn — только UDP-слушатель + файл wg-turn.conf (как на Android до VpnService).
mode = socks
socks = 127.0.0.1:1080
# socks-auth = false
# socks-user =
# socks-pass =

# Сколько TURN-потоков. Анонимный VK: кратно 9 (9, 18, ...). Аккаунт VK: не больше 4.
workers = 9
listen = 127.0.0.1:9000

# Пустое значение — клиент сам создаст и сохранит device.id рядом с конфигом.
device-id =

# VK: anonymous (по умолчанию) или account (+ vk-creds-file с TURN-кредами).
vk-auth = anonymous
vk-anon-path = vkcalls
captcha-mode = auto
go-dns = yandex
obfs = audio

# turn-tcp = false
# notls = false
# turn =
# port =
# vk-creds-file =
`

var configKeyAliases = map[string]string{
	"workers":    "n",
	"hashes":     "vk",
	"vk-hashes":  "vk",
	"hash":       "vk",
	"server":     "peer",
	"endpoint":   "peer",
	"pass":       "password",
	"deviceid":   "device-id",
	"no-dtls":    "notls",
	"no-tls":     "notls",
	"turntcp":    "turn-tcp",
	"socksaddr":  "socks",
	"socks-addr": "socks",
}

func canonicalizeConfigKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "_", "-")
	if alias, ok := configKeyAliases[key]; ok {
		return alias
	}
	return key
}

func configPathFromArgs(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-config" || a == "--config":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				return args[i+1]
			}
		case strings.HasPrefix(a, "-config="):
			return strings.TrimPrefix(a, "-config=")
		case strings.HasPrefix(a, "--config="):
			return strings.TrimPrefix(a, "--config=")
		}
	}
	return ""
}

func cliHasPeerAndVK(args []string) bool {
	hasPeer, hasVK := false, false
	for _, a := range args {
		switch {
		case a == "-peer", a == "--peer", strings.HasPrefix(a, "-peer="), strings.HasPrefix(a, "--peer="):
			hasPeer = true
		case a == "-vk", a == "--vk", strings.HasPrefix(a, "-vk="), strings.HasPrefix(a, "--vk="):
			hasVK = true
		}
	}
	return hasPeer && hasVK
}

func findDefaultConfigPath() string {
	candidates := []string{defaultConfigName}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), defaultConfigName))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, defaultConfigName))
	}
	seen := map[string]struct{}{}
	for _, p := range candidates {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if _, dup := seen[abs]; dup {
			continue
		}
		seen[abs] = struct{}{}
		st, err := os.Stat(abs)
		if err == nil && !st.IsDir() {
			return abs
		}
	}
	return ""
}

func loadConfigFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := make(map[string]string)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: ожидается key = value", path, lineNo)
		}
		key = canonicalizeConfigKey(key)
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if key == "" || key == "config" {
			continue
		}
		out[key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func applyConfigDefaults(path string) error {
	kv, err := loadConfigFile(path)
	if err != nil {
		return err
	}
	known := map[string]struct{}{}
	flag.VisitAll(func(f *flag.Flag) {
		known[f.Name] = struct{}{}
	})
	for key, val := range kv {
		if _, ok := known[key]; !ok {
			fmt.Fprintf(os.Stderr, "[КОНФИГ] неизвестный ключ %q в %s, пропускаю\n", key, path)
			continue
		}
		if err := flag.Set(key, val); err != nil {
			return fmt.Errorf("%s: ключ %s: %w", path, key, err)
		}
	}
	return nil
}

func writeExampleConfig(path string) error {
	return os.WriteFile(path, []byte(exampleConfigText), 0600)
}

func deviceIDPathNear(configPath string) string {
	dir := ""
	if configPath != "" {
		dir = filepath.Dir(configPath)
	}
	if dir == "" || dir == "." {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	return filepath.Join(dir, deviceIDFileName)
}

func loadOrCreateDeviceID(configPath string) (string, error) {
	path := deviceIDPathNear(configPath)
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" && id != "unknown" {
			return id, nil
		}
	}
	id := uuid.New().String()
	if err := os.WriteFile(path, []byte(id+"\n"), 0600); err != nil {
		return "", err
	}
	return id, nil
}
