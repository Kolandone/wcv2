package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	RED       = "1"
	GREEN     = "2"
	ORANGE    = "208"
	BLUE      = "39"
	LAVENDER  = "141"
	TEAL      = "14"
	PINK      = "212"
	YELLOW    = "220"
	CYAN      = "51"
	MUTED     = "243"
)

var (
	title   = fmtStr(">>", BLUE, true)
	ask     = fmtStr("-", "", true)
	info    = fmtStr("+", "", true)
	warning = fmtStr("Warning", RED, true)
)

func checkAndroid() {
	path := os.Getenv("PATH")
	if runtime.GOOS == "android" || strings.Contains(path, "com.termux") {
		prefix := os.Getenv("PREFIX")
		certPath := filepath.Join(prefix, "etc/tls/cert.pem")
		if err := os.Setenv("SSL_CERT_FILE", certPath); err != nil {
			failMessage("Failed to set Termux cert file.")
			log.Fatalln(err)
		}
		isAndroid = true
	}
}

func setDNS() {
	http.DefaultTransport.(*http.Transport).DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		d := net.Dialer{
			Resolver: &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
					conn, err := net.Dial("udp", "8.8.8.8:53")
					if err != nil {
						failMessage("Failed to dial DNS. Please disconnect your VPN and try again...")
						log.Fatal(err)
					}
					return conn, nil
				},
			},
		}
		conn, err := d.DialContext(ctx, network, addr)
		if err != nil {
			failMessage("DNS resolution failed. Please disconnect your VPN and try again...")
			log.Fatal(err)
		}
		return conn, nil
	}

}

func renderHeader() {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("39")).
		Align(lipgloss.Center)

	versionStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("208"))

	subStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("243"))

	borderColor := lipgloss.Color("39")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 2).
		Width(44).
		Align(lipgloss.Center)

	header := fmt.Sprintf(
		"%s  %s",
		titleStyle.Render("CF Worker Wizard"),
		versionStyle.Render(VERSION),
	)
	footer := subStyle.Render("Telegram: @kolandjs1  |  GitHub: @kolandone")

	fmt.Println()
	fmt.Println(box.Render(header))
	fmt.Println(box.Render(footer))
}

func initPaths() {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		dir = os.TempDir()
	}

	cacheDir := filepath.Join(dir, "CF-Wizard")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		failMessage("Failed to create cache directory.")
		log.Fatalln(err)
	}

	cachePath = filepath.Join(cacheDir, "tld.cache")
}

func fmtStr(str string, color string, isBold bool) string {
	style := lipgloss.NewStyle().Bold(isBold)

	if color != "" {
		style = style.Foreground(lipgloss.Color(color))
	}

	return style.Render(str)
}
