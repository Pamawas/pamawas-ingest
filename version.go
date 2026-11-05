package main

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"
)

func loadVersion() string {
	if content, err := os.ReadFile("version.txt"); err == nil {
		if version := strings.TrimSpace(string(content)); version != "" {
			return version
		}
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func printStartupBanner(version, port string) {
	banner := ` _____       /\      __  __      /\     __          __     /\      _____
|  __ \     /  \    |  \/  |    /  \    \ \        / /    /  \    / ____|
| |__) |   / /\ \   | \  / |   / /\ \    \ \  /\  / /    / /\ \   \___ \
|  ___/   / ____ \  | |\/| |  / ____ \    \ \/  \/ /    / ____ \   ___) |
|_|      /_/    \_\ |_|  |_| /_/    \_\    \__/\__/    /_/    \_\ |____/`
	serviceArt := ` _____   _   _    _____   ______   _____   _______
|_   _| | \ | |  / ____| |  ____| / ____| |__   __|
  | |   |  \| | | |  __  | |__    \___ \     | |
  | |   | |\  | | | |_ | |  __|    ___) |    | |
 _| |_  |_| \_|  \_____| |______| |____/     |_|`
	fmt.Printf("\033[1;36m%s\n%s\033[0m\nVersion: %s | Port: :%s\n\n", banner, serviceArt, version, port)
}
