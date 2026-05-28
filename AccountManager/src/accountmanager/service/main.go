package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"agentic-plugin/account-manager/src/accountmanager/internal/accountmanager"
)

const accountManagerManifestPath = "plugins/account-manager/plugin.json"

func main() {
	addr := flag.String("addr", "127.0.0.1:18186", "Account Manager plugin service listen address")
	configPath := flag.String("config", "plugins/account-manager/config.json", "Account Manager plugin config path")
	flag.Parse()

	pluginRoot, err := ensureAccountManagerPluginWorkdir()
	if err != nil {
		log.Fatal(err)
	}

	accountmanager.AccountManagerConfigPath = *configPath
	api := &accountmanager.HttpAPI_Plugin{}
	log.Printf("account-manager-service listening on %s, root=%s, config=%s", *addr, pluginRoot, *configPath)
	if err := http.ListenAndServe(*addr, api); err != nil {
		log.Fatal(err)
	}
}

func ensureAccountManagerPluginWorkdir() (string, error) {
	if root := strings.TrimSpace(os.Getenv("ACCOUNT_MANAGER_PLUGIN_ROOT")); root != "" {
		return chdirToPluginRoot(root)
	}
	if cwd, err := os.Getwd(); err == nil {
		if root, ok := findPluginRoot(cwd); ok {
			return chdirToPluginRoot(root)
		}
	}
	if exe, err := os.Executable(); err == nil {
		if root, ok := findPluginRoot(filepath.Dir(exe)); ok {
			return chdirToPluginRoot(root)
		}
	}
	return "", fmt.Errorf("找不到 Account Manager plugin 根目錄；請從包含 %s 的目錄或其子目錄啟動，或設定 ACCOUNT_MANAGER_PLUGIN_ROOT", accountManagerManifestPath)
}

func chdirToPluginRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(abs, accountManagerManifestPath)); err != nil {
		return "", fmt.Errorf("Account Manager plugin 根目錄無效: %s: %w", abs, err)
	}
	if err := os.Chdir(abs); err != nil {
		return "", err
	}
	return abs, nil
}

func findPluginRoot(start string) (string, bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, accountManagerManifestPath)); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
