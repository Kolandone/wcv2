package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cloudflare/cloudflare-go/v4/kv"
)

type DeployType int

const (
	DTWorker DeployType = iota
	DTPage
)

var DeployTypeNames = map[DeployType]string{
	DTWorker: "worker",
	DTPage:   "page",
}

func (dt DeployType) String() string {
	return DeployTypeNames[dt]
}

type Deployment struct {
	Name string
	Type string
}

type SecretBinding struct {
	Key   string
	Value string
}

const (
	CharsetAlphaNumeric = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	CharsetSubDomain    = "abcdefghijklmnopqrstuvwxyz0123456789-"
	DomainRegex         = `^(?i)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}$`
)

func downloadFile(url string) ([]byte, error) {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			return nil
		},
		Timeout: 60 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if len(content) == 0 {
		return nil, fmt.Errorf("downloaded file is empty")
	}

	sum := sha256.Sum256(content)
	fmt.Printf("%s SHA-256: %x\n", info, sum)
	return content, nil
}

func promptWorkerURL() ([]byte, error) {
	for {
		url := promptUser("- Enter the Worker script URL (GitHub release or raw JS link): ", nil)
		url = strings.TrimSpace(url)
		if url == "" {
			failMessage("URL cannot be empty.")
			continue
		}

		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			failMessage("URL must start with http:// or https://")
			continue
		}

		fmt.Printf("\n%s Downloading worker script...\n", title)
		content, err := downloadFile(url)
		if err != nil {
			failMessage(fmt.Sprintf("Failed to download: %v", err))
			if response := promptUser("- Would you like to try a different URL? (y/n): ", []string{"y", "n"}); strings.ToLower(response) == "n" {
				return nil, fmt.Errorf("user cancelled")
			}
			continue
		}

		successMessage("Worker script downloaded successfully!")
		return content, nil
	}
}

func generateRandomString(charSet string, length int, isDomain bool) string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	randomBytes := make([]byte, length)

	for i := range randomBytes {
		for {
			char := charSet[r.Intn(len(charSet))]
			if isDomain && (i == 0 || i == length-1) && char == byte('-') {
				continue
			}
			randomBytes[i] = char
			break
		}
	}

	return string(randomBytes)
}

func generateRandomSubDomain(subDomainLength int) string {
	return generateRandomString(CharsetSubDomain, subDomainLength, true)
}

func generateShortID() string {
	return generateRandomString(CharsetAlphaNumeric, 8, false)
}

func isValidSubDomain(subDomain string) error {
	subdomainRegex := regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	isValid := subdomainRegex.MatchString(subDomain)
	if !isValid {
		message := fmt.Sprintf("Subdomain cannot start with %s and should only contain %s and %s. Please try again.\n", fmtStr("-", RED, true), fmtStr("A-Z", GREEN, true), fmtStr("0-9", GREEN, true))
		return fmt.Errorf("%s", message)
	}
	return nil
}

func isValidIpDomain(value string) bool {
	if net.ParseIP(value) != nil && !strings.Contains(value, ":") {
		return true
	}

	if isValidIPv6(value) {
		return true
	}

	domainRegex := regexp.MustCompile(DomainRegex)
	return domainRegex.MatchString(value)
}

func isValidIPv6(value string) bool {
	regex := regexp.MustCompile(`^\[(.+)\]$`)
	matches := regex.FindStringSubmatch(value)
	return matches != nil && net.ParseIP(matches[1]) != nil
}

func isValidHost(value string) bool {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return false
	}

	if !isValidIpDomain(host) {
		return false
	}

	intPort, err := strconv.Atoi(port)
	if err != nil || intPort < 1 || intPort > 65535 {
		return false
	}

	return true
}

func promptUser(prompt string, answers []string) string {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Printf("\n%s", prompt)
		input, err := reader.ReadString('\n')

		if err != nil {
			fmt.Printf("\n%s Exiting...\n", title)
			if err == io.EOF {
				os.Exit(0)
			}
			os.Exit(1)
		}

		input = strings.TrimSpace(input)

		if answers == nil {
			return input
		} else {
			for _, ans := range answers {
				if strings.EqualFold(input, ans) {
					return input
				}
			}

			failMessage("Invalid answer. Try again...")
		}
	}
}

func promptUserWithDefault(prompt string, defaultValue string) string {
	reader := bufio.NewReader(os.Stdin)

	fmt.Printf("\n%s", prompt)
	input, err := reader.ReadString('\n')

	if err != nil {
		fmt.Printf("\n%s Exiting...\n", title)
		if err == io.EOF {
			os.Exit(0)
		}
		os.Exit(1)
	}

	input = strings.TrimSpace(input)
	if input == "" {
		return defaultValue
	}
	return input
}

func promptYesNo(prompt string) bool {
	response := promptUser(prompt, []string{"y", "n"})
	return strings.ToLower(response) == "y"
}

func failMessage(message string) {
	errMark := fmtStr("✗", RED, true)
	fmt.Printf("%s %s\n", errMark, message)
}

func successMessage(message string) {
	succMark := fmtStr("✓", GREEN, true)
	fmt.Printf("%s %s\n", succMark, message)
}

func openURL(url string) error {
	var cmd string
	var args = []string{url}

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		if isAndroid {
			termuxBin := os.Getenv("PATH")
			cmd = filepath.Join(termuxBin, "termux-open-url")
		} else {
			cmd = "xdg-open"
		}
	}

	err := exec.Command(cmd, args...).Start()
	if err != nil {
		return err
	}

	return nil
}

func promptSecretBindings() []SecretBinding {
	var bindings []SecretBinding

	for {
		if !promptYesNo("\n- Do you want to add a Secret/Environment Binding? (y/n): ") {
			break
		}

		key := promptUser("- Enter the Secret Key / Variable Name: ", nil)
		key = strings.TrimSpace(key)
		if key == "" {
			failMessage("Key cannot be empty.")
			continue
		}

		value := promptUser("- Enter the Secret Value: ", nil)
		value = strings.TrimSpace(value)

		bindings = append(bindings, SecretBinding{Key: key, Value: value})
		successMessage(fmt.Sprintf("Binding added: %s", fmtStr(key, GREEN, true)))
	}

	return bindings
}

func runWizard() {
	renderHeader()
	fmt.Printf("\n%s Welcome to %s!\n", title, fmtStr("Worker Wizard", GREEN, true))
	fmt.Printf("%s This wizard will help you to deploy a custom Worker or Pages project on Cloudflare.\n", info)
	fmt.Printf("%s Please make sure you have a verified %s account.\n", info, fmtStr("Cloudflare", ORANGE, true))

	ctx := context.Background()
	if err := ensureCloudflareAuth(ctx); err != nil {
		failMessage("Failed to login Cloudflare.")
		log.Fatalln(err)
	}

	for {
		message := fmt.Sprintf("1- %s a new deployment.\n2- %s an existing deployment.\n\n- Select: ", fmtStr("CREATE", GREEN, true), fmtStr("MODIFY", RED, true))
		response := promptUser(message, []string{"1", "2"})
		switch response {
		case "1":
			createDeployment()
		case "2":
			modifyDeployment()
		}

		res := promptUser("- Would you like to run the wizard again? (y/n): ", []string{"y", "n"})
		if strings.ToLower(res) == "n" {
			fmt.Printf("\n%s Exiting...\n", title)
			return
		}
	}
}

func createDeployment() {
	ctx := context.Background()

	fmt.Printf("\n%s Get settings...\n", title)
	fmt.Printf("\n%s You can use %s or %s method to deploy.\n", info, fmtStr("Workers", ORANGE, true), fmtStr("Pages", ORANGE, true))

	var deployType DeployType
	response := promptUser("1- Workers method.\n2- Pages method.\n\n- Select: ", []string{"1", "2"})
	switch response {
	case "1":
		deployType = DTWorker
	case "2":
		deployType = DTPage
	}

	workerJSContent, err := promptWorkerURL()
	if err != nil {
		failMessage("Failed to get worker script.")
		log.Fatalln(err)
	}
	workerJS = workerJSContent

	defaultName := "worker-" + generateShortID()
	fmt.Printf("\n%s Default script name: %s\n", info, fmtStr(defaultName, ORANGE, true))
	projectName := promptUserWithDefault("- Enter a script name or press ENTER for default: ", defaultName)

	if err := isValidSubDomain(projectName); err != nil {
		failMessage(err.Error())
		log.Fatalln(err)
	}

	if deployType == DTWorker {
		isAvailable := isWorkerAvailable(ctx, projectName)
		if !isAvailable {
			prompt := fmt.Sprintf("- Name already exists! This will %s existing settings, continue? (y/n): ", fmtStr("OVERWRITE", RED, true))
			if !promptYesNo(prompt) {
				return
			}
		}
	} else {
		isAvailable := isPagesProjectAvailable(ctx, projectName)
		if !isAvailable {
			prompt := fmt.Sprintf("- Name already exists! This will %s existing settings, continue? (y/n): ", fmtStr("OVERWRITE", RED, true))
			if !promptYesNo(prompt) {
				return
			}
		}
	}

	successMessage(fmt.Sprintf("Using script name: %s", projectName))

	var kvNamespace *kv.Namespace
	if promptYesNo("\n- Do you want to create a KV Namespace for this deployment? (y/n): ") {
		kvName := promptUserWithDefault("- Enter a KV Namespace name or press ENTER for default: ", "kv-"+generateShortID())
		for {
			fmt.Printf("\n%s Creating KV namespace...\n", title)
			kvNamespace, err = createKVNamespace(ctx, kvName)
			if err != nil {
				failMessage("Failed to create KV.")
				log.Printf("%v\n\n", err)
				if response := promptUser("- Would you like to try again? (y/n): ", []string{"y", "n"}); strings.ToLower(response) == "n" {
					return
				}
				continue
			}
			successMessage("KV namespace created successfully!")
			break
		}
	}

	bindings := promptSecretBindings()

	var customDomain string
	fmt.Printf("\n%s You can set a custom domain ONLY if the domain is registered on this Cloudflare account.\n", info)
	if response := promptUser("- Enter a custom domain (if you have one) or press ENTER to skip: ", nil); response != "" {
		customDomain = response
	}

	var panelURL string
	switch deployType {
	case DTWorker:
		panelURL, err = deployWorker(ctx, projectName, workerJS, kvNamespace, bindings, customDomain)
	case DTPage:
		panelURL, err = deployPagesProject(ctx, projectName, workerJS, kvNamespace, bindings, customDomain)
	}

	if err != nil {
		failMessage("Failed to deploy.")
		log.Fatalln(err)
	}

	fmt.Printf("\n%s Deployment URL: %s\n", title, fmtStr(panelURL, GREEN, true))
}

func modifyDeployment() {
	ctx := context.Background()
	if err := ensureCloudflareAuth(ctx); err != nil {
		failMessage("Failed to login Cloudflare.")
		log.Fatalln(err)
	}

	for {
		var deployments []Deployment

		fmt.Printf("\n%s Getting deployments list...\n", title)
		workersList, err := listWorkers(ctx)
		if err != nil {
			failMessage("Failed to get workers list.")
			log.Println(err)
		} else {
			for _, worker := range workersList {
				deployments = append(deployments, Deployment{
					Name: worker,
					Type: "workers",
				})
			}
		}

		pagesList, err := listPages(ctx)
		if err != nil {
			failMessage("Failed to get pages list.")
			log.Println(err)
		} else {
			for _, pages := range pagesList {
				deployments = append(deployments, Deployment{
					Name: pages,
					Type: "pages",
				})
			}
		}

		if len(deployments) == 0 {
			failMessage("No Workers or Pages found, Exiting...")
			return
		}

		message := fmt.Sprintf("Found %d deployments:\n", len(deployments))
		successMessage(message)
		for i, dep := range deployments {
			fmt.Printf(" %s %s - %s\n", fmtStr(strconv.Itoa(i+1)+".", BLUE, true), dep.Name, fmtStr(dep.Type, ORANGE, true))
		}

		var index int
		for {
			response := promptUser("- Please select the number you want to modify: ", nil)
			index, err = strconv.Atoi(response)
			if err != nil || index < 1 || index > len(deployments) {
				failMessage("Invalid selection, please try again.")
				continue
			}
			break
		}

		depName := deployments[index-1].Name
		depType := deployments[index-1].Type

		message = fmt.Sprintf("1- %s deployment.\n2- %s deployment.\n\n- Select: ", fmtStr("UPDATE", GREEN, true), fmtStr("DELETE", RED, true))
		response := promptUser(message, []string{"1", "2"})

		switch response {
		case "1":
			workerJSContent, err := promptWorkerURL()
			if err != nil {
				failMessage("Failed to get worker script.")
				log.Fatalln(err)
			}
			workerJS = workerJSContent

			if depType == "workers" {
				if err := updateWorker(ctx, depName); err != nil {
					failMessage("Failed to update deployment.")
					log.Fatalln(err)
				}
				successMessage("Deployment updated successfully!")
			} else {
				if err := updatePagesProject(ctx, depName); err != nil {
					failMessage("Failed to update deployment.")
					log.Fatalln(err)
				}
				successMessage("Deployment updated successfully!")
			}

		case "2":
			if depType == "workers" {
				if err := deleteWorker(ctx, depName); err != nil {
					failMessage("Failed to delete deployment.")
					log.Fatalln(err)
				}
				successMessage("Deployment deleted successfully!")
			} else {
				if err := deletePagesProject(ctx, depName); err != nil {
					failMessage("Failed to delete deployment.")
					log.Fatalln(err)
				}
				successMessage("Deployment deleted successfully!")
			}
		}

		if response := promptUser("- Would you like to modify another deployment? (y/n): ", []string{"y", "n"}); strings.ToLower(response) == "n" {
			break
		}
	}
}
