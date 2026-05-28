package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"agentic-plugin/sample/src/sampleplugin"
)

const samplePluginManifestPath = "plugins/sample/plugin.json"

func main() {
	addr := flag.String("addr", "127.0.0.1:18182", "Sample plugin service listen address")
	configPath := flag.String("config", "plugins/sample/config.json", "Sample plugin config path")
	flag.Parse()

	pluginRoot, err := ensureSamplePluginWorkdir()
	if err != nil {
		log.Fatal(err)
	}

	sampleplugin.SamplePluginConfigPath = *configPath
	api := &sampleplugin.HttpAPI_Plugin{}
	log.Printf("sample-service listening on %s, root=%s, config=%s", *addr, pluginRoot, *configPath)
	if err := http.ListenAndServe(*addr, api); err != nil {
		log.Fatal(err)
	}
}

func ensureSamplePluginWorkdir() (string, error) {
	if root := strings.TrimSpace(os.Getenv("SAMPLE_PLUGIN_ROOT")); root != "" {
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
	return "", fmt.Errorf("找不到 Sample plugin 根目錄；請從包含 %s 的目錄或其子目錄啟動，或設定 SAMPLE_PLUGIN_ROOT", samplePluginManifestPath)
}

func chdirToPluginRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(abs, samplePluginManifestPath)); err != nil {
		return "", fmt.Errorf("Sample plugin 根目錄無效: %s: %w", abs, err)
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
		if _, err := os.Stat(filepath.Join(dir, samplePluginManifestPath)); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
