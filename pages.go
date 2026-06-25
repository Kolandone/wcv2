package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"mime/multipart"
	"net/textproto"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	cf "github.com/cloudflare/cloudflare-go/v4"
	"github.com/cloudflare/cloudflare-go/v4/kv"
	"github.com/cloudflare/cloudflare-go/v4/option"
	"github.com/cloudflare/cloudflare-go/v4/pages"
)

type projectDeploymentNewParams struct {
	AccountID string                `form:"account_id,required"`
	Branch    string                `form:"branch"`
	Manifest  string                `form:"manifest"`
	WorkerJS  *multipart.FileHeader `form:"_worker.js"`
	jsContent []byte
}

func (pdp projectDeploymentNewParams) MarshalMultipart() ([]byte, string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	manifestHeaders := textproto.MIMEHeader{
		"Content-Disposition": []string{`form-data; name="manifest"`},
	}

	manifestPart, err := writer.CreatePart(manifestHeaders)
	if err != nil {
		return nil, "", fmt.Errorf("error creating manifest part: %w", err)
	}

	_, err = manifestPart.Write([]byte("{}"))
	if err != nil {
		return nil, "", fmt.Errorf("error writing manifest content: %w", err)
	}

	branchHeaders := textproto.MIMEHeader{
		"Content-Disposition": []string{`form-data; name="branch"`},
	}

	branchPart, err := writer.CreatePart(branchHeaders)
	if err != nil {
		return nil, "", fmt.Errorf("error creating branch part: %w", err)
	}

	_, err = branchPart.Write([]byte("main"))
	if err != nil {
		return nil, "", fmt.Errorf("error writing branch content: %w", err)
	}

	fileHeaders := textproto.MIMEHeader{
		"Content-Disposition": []string{`form-data; name="_worker.js"; filename="_worker.js"`},
		"Content-Type":        []string{"application/javascript"},
	}

	filePart, err := writer.CreatePart(fileHeaders)
	if err != nil {
		return nil, "", fmt.Errorf("error creating file part: %w", err)
	}

	if len(pdp.jsContent) == 0 {
		return nil, "", fmt.Errorf("worker.js content is empty")
	}

	_, err = filePart.Write(pdp.jsContent)
	if err != nil {
		return nil, "", fmt.Errorf("error copying file content: %w", err)
	}

	err = writer.Close()
	if err != nil {
		return nil, "", fmt.Errorf("error closing multipart writer: %w", err)
	}

	return body.Bytes(), writer.FormDataContentType(), nil
}

func buildPagesBindings(kvNs *kv.Namespace, bindings []SecretBinding) map[string]pages.ProjectDeploymentConfigsProductionEnvVarsUnionParam {
	envVars := make(map[string]pages.ProjectDeploymentConfigsProductionEnvVarsUnionParam)

	for _, b := range bindings {
		envVars[b.Key] = pages.ProjectDeploymentConfigsProductionEnvVarsPagesPlainTextEnvVarParam{
			Type:  cf.F(pages.ProjectDeploymentConfigsProductionEnvVarsPagesPlainTextEnvVarTypePlainText),
			Value: cf.F(b.Value),
		}
	}

	return envVars
}

func createPagesProject(
	ctx context.Context,
	name string,
	jsContent []byte,
	kvNs *kv.Namespace,
	bindings []SecretBinding,
) (
	*pages.Project,
	error,
) {
	envVars := buildPagesBindings(kvNs, bindings)

	var kvNamespaces map[string]pages.ProjectDeploymentConfigsProductionKVNamespaceParam
	if kvNs != nil {
		kvNamespaces = map[string]pages.ProjectDeploymentConfigsProductionKVNamespaceParam{
			"kv": {
				NamespaceID: cf.F(kvNs.ID),
			},
		}
	}

	project, err := cfClient.Pages.Projects.New(
		ctx,
		pages.ProjectNewParams{
			AccountID: cf.F(cfAccount.ID),
			Project: pages.ProjectParam{
				Name:             cf.F(name),
				ProductionBranch: cf.F("main"),
				DeploymentConfigs: cf.F(pages.ProjectDeploymentConfigsParam{
					Production: cf.F(pages.ProjectDeploymentConfigsProductionParam{
						Browsers:           cf.F(map[string]pages.ProjectDeploymentConfigsProductionBrowserParam{}),
						CompatibilityDate:  cf.F(time.Now().AddDate(0, 0, -1).Format("2006-01-02")),
						CompatibilityFlags: cf.F([]string{"nodejs_compat"}),
						KVNamespaces:       cf.F(kvNamespaces),
						EnvVars:            cf.F(envVars),
					}),
				}),
			},
		})

	if err != nil {
		return nil, fmt.Errorf("error creating pages project: %w", err)
	}

	return project, nil
}

func createPagesDeployment(ctx context.Context, project *pages.Project, jsContent []byte) (*pages.Deployment, error) {
	param := projectDeploymentNewParams{
		AccountID: cfAccount.ID,
		Branch:    "main",
		Manifest:  "{}",
		WorkerJS:  &multipart.FileHeader{Filename: "worker.js"},
		jsContent: jsContent,
	}
	data, ct, err := param.MarshalMultipart()
	if err != nil {
		return nil, fmt.Errorf("error marshalling pages multipart data: %w", err)
	}
	r := bytes.NewBuffer(data)

	deployment, err := cfClient.Pages.Projects.Deployments.New(
		ctx,
		project.Name,
		pages.ProjectDeploymentNewParams{AccountID: cf.F(cfAccount.ID)},
		option.WithRequestBody(ct, r),
	)

	if err != nil {
		return nil, fmt.Errorf("error creating pages deployment: %w", err)
	}

	return deployment, nil
}

func addPagesProjectCustomDomain(ctx context.Context, projectName string, customDomain string) (string, error) {
	res, err := cfClient.Pages.Projects.Domains.New(ctx, projectName, pages.ProjectDomainNewParams{
		AccountID: cf.F(cfAccount.ID),
		Name:      cf.F(customDomain),
	})

	if err != nil {
		return "", fmt.Errorf("error adding custom domain to pages: %w", err)
	}

	return res.Name, nil
}

func isPagesProjectAvailable(ctx context.Context, projectName string) bool {
	_, err := cfClient.Pages.Projects.Get(ctx, projectName, pages.ProjectGetParams{AccountID: cf.F(cfAccount.ID)})
	return err != nil
}

func listPages(ctx context.Context) ([]string, error) {
	projects, err := cfClient.Pages.Projects.List(ctx, pages.ProjectListParams{
		AccountID: cf.F(cfAccount.ID),
	})

	if err != nil {
		return nil, fmt.Errorf("error listing pages projects: %w", err)
	}

	if len(projects.Result) == 0 {
		return nil, nil
	}

	var projectNames []string
	for _, project := range projects.Result {
		rawName := project.JSON.ExtraFields["name"].Raw()
		var name string
		if err := json.Unmarshal([]byte(rawName), &name); err != nil {
			return nil, fmt.Errorf("error unmarshalling project name: %w", err)
		}

		projectNames = append(projectNames, name)
	}

	return projectNames, nil
}

func deletePagesProject(ctx context.Context, projectName string) error {
	domains, err := cfClient.Pages.Projects.Domains.List(
		ctx,
		projectName,
		pages.ProjectDomainListParams{AccountID: cf.F(cfAccount.ID)},
	)

	if err != nil {
		return fmt.Errorf("error listing project domains: %w", err)
	}

	if len(domains.Result) > 0 {
		fmt.Printf("\n%s Detaching custom domains...\n", title)
		for _, domain := range domains.Result {
			_, err := cfClient.Pages.Projects.Domains.Delete(
				ctx,
				projectName,
				domain.Name,
				pages.ProjectDomainDeleteParams{AccountID: cf.F(cfAccount.ID)},
			)
			if err != nil {
				return fmt.Errorf("error detaching custom domain: %w", err)
			}

			message := fmt.Sprintf("Custom domain %s detached successfully!", domain.Name)
			successMessage(message)
		}
	}

	_, er := cfClient.Pages.Projects.Delete(ctx, projectName, pages.ProjectDeleteParams{
		AccountID: cf.F(cfAccount.ID),
	})

	if er != nil {
		return fmt.Errorf("error deleting pages project: %w", er)
	}

	return nil
}

func updatePagesProject(ctx context.Context, projectName string) error {
	project, err := cfClient.Pages.Projects.Get(ctx, projectName, pages.ProjectGetParams{
		AccountID: cf.F(cfAccount.ID),
	})
	if err != nil {
		return fmt.Errorf("could not get project: %w", err)
	}

	param := projectDeploymentNewParams{
		AccountID: cfAccount.ID,
		Branch:    "main",
		Manifest:  "{}",
		WorkerJS:  &multipart.FileHeader{Filename: "worker.js"},
		jsContent: workerJS,
	}
	data, ct, err := param.MarshalMultipart()
	if err != nil {
		return fmt.Errorf("error marshalling pages multipart data: %w", err)
	}
	r := bytes.NewBuffer(data)

	_, err = cfClient.Pages.Projects.Deployments.New(
		ctx,
		project.Name,
		pages.ProjectDeploymentNewParams{AccountID: cf.F(cfAccount.ID)},
		option.WithRequestBody(ct, r),
	)

	if err != nil {
		return fmt.Errorf("error updating pages project: %w", err)
	}

	return nil
}

func deployPagesProject(
	ctx context.Context,
	name string,
	jsContent []byte,
	kvNamespace *kv.Namespace,
	bindings []SecretBinding,
	customDomain string,
) (
	panelURL string,
	err error,
) {
	var project *pages.Project

	for {
		fmt.Printf("\n%s Creating Pages project...\n", title)

		project, err = createPagesProject(ctx, name, jsContent, kvNamespace, bindings)
		if err != nil {
			failMessage("Failed to create project.")
			log.Printf("%v\n\n", err)
			if response := promptUser("- Would you like to try again? (y/n): ", []string{"y", "n"}); strings.ToLower(response) == "n" {
				return "", nil
			}
			continue
		}

		successMessage("Page created successfully!")
		break
	}

	for {
		fmt.Printf("\n%s Deploying Pages project...\n", title)

		_, err = createPagesDeployment(ctx, project, jsContent)
		if err != nil {
			failMessage("Failed to deploy project.")
			log.Printf("%v\n\n", err)
			if response := promptUser("- Would you like to try again? (y/n): ", []string{"y", "n"}); strings.ToLower(response) == "n" {
				return "", nil
			}
			continue
		}

		successMessage("Page deployed successfully!")
		break
	}

	if customDomain != "" {
		for {
			recordName, err := addPagesProjectCustomDomain(ctx, name, customDomain)
			if err != nil {
				failMessage("Failed to add custom domain.")
				log.Printf("%v\n\n", err)
				if response := promptUser("- Would you like to try again? (y/n): ", []string{"y", "n"}); strings.ToLower(response) == "n" {
					return "", nil
				}
				continue
			}

			successMessage("Custom domain added to pages successfully!")
			warnStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("208")).
				Bold(true)
			fmt.Printf("\n%s %s\n", warnStyle.Render("⚠ CNAME Record Required:"), fmt.Sprintf("Create Name: %s → Target: %s", fmtStr(recordName, "2", true), fmtStr(name+".pages.dev", "2", true)))
			return "https://" + customDomain, nil
		}
	}

	successMessage("Pages project deployed successfully.")
	return "https://" + project.Subdomain, nil
}
