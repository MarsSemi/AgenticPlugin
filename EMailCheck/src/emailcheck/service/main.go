package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"agentic-plugin/emailcheck/src/emailcheck/api"
)

const emailCheckPluginManifestPath = "plugins/email-check/plugin.json"

func main() {
	addr := flag.String("addr", "127.0.0.1:18185", "EMAIL Check plugin service listen address")
	configPath := flag.String("config", "plugins/email-check/config.json", "EMAIL Check plugin config path")
	flag.Parse()

	pluginRoot, err := ensureEmailCheckPluginWorkdir()
	if err != nil {
		log.Fatal(err)
	}

	api.EmailCheckConfigPath = *configPath
	handler := &api.HttpAPI_Plugin{}
	log.Printf("email-check-service listening on %s, root=%s, config=%s", *addr, pluginRoot, *configPath)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatal(err)
	}
}

func ensureEmailCheckPluginWorkdir() (string, error) {
	if root := strings.TrimSpace(os.Getenv("EMAIL_CHECK_PLUGIN_ROOT")); root != "" {
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
	return "", fmt.Errorf("找不到 EMAIL Check plugin 根目錄；請從包含 %s 的目錄或其子目錄啟動，或設定 EMAIL_CHECK_PLUGIN_ROOT", emailCheckPluginManifestPath)
}

func chdirToPluginRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(abs, emailCheckPluginManifestPath)); err != nil {
		return "", fmt.Errorf("EMAIL Check plugin 根目錄無效: %s: %w", abs, err)
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
		if _, err := os.Stat(filepath.Join(dir, emailCheckPluginManifestPath)); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
