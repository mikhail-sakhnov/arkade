// Copyright (c) arkade author(s) 2020. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package apps

import (
	"bytes"
	"errors"
	"fmt"
	"log"

	"github.com/alexellis/arkade/pkg/config"
	"github.com/alexellis/arkade/pkg/k8s"

	"text/template"

	"github.com/alexellis/arkade/pkg"

	"github.com/spf13/cobra"
)

type RegInputData struct {
	IngressDomain    string
	CertmanagerEmail string
	IngressClass     string
	Namespace        string
	NginxMaxBuffer   string
	IssuerType       string
	IssuerAPI        string
}

func MakeInstallRegistryIngress() *cobra.Command {
	var registryIngress = &cobra.Command{
		Use:   "docker-registry-ingress",
		Short: "Install registry ingress with TLS",
		Long: `Install registry ingress. Requires cert-manager 0.11.0 or higher installation
in the cluster. Please set --domain to your custom domain and set --email
to your email - this email is used by letsencrypt for domain expiry etc.`,
		Example:      `  arkade install registry-ingress --domain registry.example.com --email openfaas@example.com`,
		SilenceUsage: true,
	}

	registryIngress.Flags().StringP("domain", "d", "", "Custom Ingress Domain")
	registryIngress.Flags().StringP("email", "e", "", "Letsencrypt Email")
	registryIngress.Flags().String("ingress-class", "nginx", "Ingress class to be used such as nginx or traefik")
	registryIngress.Flags().String("max-size", "200m", "the max size for the ingress proxy, default to 200m")
	registryIngress.Flags().StringP("namespace", "n", "default", "The namespace where the registry is installed")
	registryIngress.Flags().Bool("staging", false, "set --staging to true to use the staging Letsencrypt issuer")

	registryIngress.RunE = func(command *cobra.Command, args []string) error {
		kubeConfigPath, _ := command.Flags().GetString("kubeconfig")
		if err := config.SetKubeconfig(kubeConfigPath); err != nil {
			return err
		}

		email, _ := command.Flags().GetString("email")
		domain, _ := command.Flags().GetString("domain")
		ingressClass, _ := command.Flags().GetString("ingress-class")
		namespace, _ := command.Flags().GetString("namespace")
		maxSize, _ := command.Flags().GetString("max-size")

		if email == "" || domain == "" {
			return errors.New("both --email and --domain flags should be set and not empty, please set these values")
		}

		if ingressClass == "" {
			return errors.New("--ingress-class must be set")
		}

		caps, err := k8s.GetCapabilities()
		if err != nil {
			return err
		}

		hasNetworking := caps["networking.k8s.io/v1"]
		staging, _ := registryIngress.Flags().GetBool("staging")
		yamlBytes, templateErr := buildRegistryYAML(domain, email, ingressClass, namespace, maxSize, staging, hasNetworking)
		if templateErr != nil {
			log.Print("Unable to install the application. Could not build the templated yaml file for the resources")
			return templateErr
		}

		tempFile, tempFileErr := writeTempFile(yamlBytes, "temp_registry_ingress.yaml")
		if tempFileErr != nil {
			log.Print("Unable to save generated yaml file into the temporary directory")
			return tempFileErr
		}

		res, err := k8s.KubectlTask("apply", "-f", tempFile)

		if err != nil {
			log.Print(err)
			return err
		}

		if res.ExitCode != 0 {
			return fmt.Errorf(`Unable to apply YAML files.
Have you got the Registry running and cert-manager 0.11.0 or higher installed? %s`,
				res.Stderr)
		}

		fmt.Println(RegistryIngressInstallMsg)

		return nil
	}

	return registryIngress
}

func buildRegistryYAML(domain, email, ingressClass, namespace, maxSize string, staging, hasNetworking bool) ([]byte, error) {
	tmplString := registryIngressExtensionsYamlTemplate
	tmpl, err := template.New("yaml").Parse(tmplString)

	if err != nil {
		return nil, err
	}

	inputData := RegInputData{
		IngressDomain:    domain,
		CertmanagerEmail: email,
		IngressClass:     ingressClass,
		Namespace:        namespace,
		IssuerType:       "letsencrypt-prod-issuer",
		IssuerAPI:        "https://acme-v02.api.letsencrypt.org/directory",
		NginxMaxBuffer:   "",
	}

	if staging {
		inputData.IssuerType = "letsencrypt-staging-issuer"
		inputData.IssuerAPI = "https://acme-staging-v02.api.letsencrypt.org/directory"
	}

	if ingressClass == "nginx" {
		inputData.NginxMaxBuffer = fmt.Sprintf("    nginx.ingress.kubernetes.io/proxy-body-size: %s", maxSize)
	}

	var tpl bytes.Buffer

	err = tmpl.Execute(&tpl, inputData)

	if err != nil {
		return nil, err
	}

	return tpl.Bytes(), nil
}

const RegistryIngressInfoMsg = `# You will need to ensure that your domain points to your cluster and is
# accessible through ports 80 and 443.
#
# This is used to validate your ownership of this domain by LetsEncrypt
# and then you can use https with your installation.

# Ingress to your domain has been installed for the Registry
# to see the ingress record run
kubectl get -n <installed-namespace> ingress docker-registry

# Check the cert-manager logs with:
kubectl logs -n cert-manager deploy/cert-manager

# A cert-manager Issuer has been installed into the provided
# namespace - to see the resource run
kubectl describe -n <installed-namespace> Issuer letsencrypt-prod-registry

# To check the status of your certificate you can run
kubectl describe -n <installed-namespace> Certificate docker-registry

# It may take a while to be issued by LetsEncrypt, in the meantime a
# self-signed cert will be installed`

const RegistryIngressInstallMsg = `=======================================================================
= Docker Registry Ingress and cert-manager Issuer have been installed =
=======================================================================` +
	"\n\n" + RegistryIngressInfoMsg + "\n\n" + pkg.ThanksForUsing

// Ingress in extensions/v1beta1 are removed in k8s 1.22+, July 2021
var registryIngressExtensionsYamlTemplate = `
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: docker-registry
  namespace: {{.Namespace}}
  annotations:
    cert-manager.io/issuer: {{.IssuerType}}
    kubernetes.io/ingress.class: {{.IngressClass}}
{{.NginxMaxBuffer}}
spec:
  rules:
  - host: {{.IngressDomain}}
    http:
      paths:
      - backend:
          serviceName: docker-registry
          servicePort: 5000
        path: /
  tls:
  - hosts:
    - {{.IngressDomain}}
    secretName: docker-registry
---
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: {{.IssuerType}}
  namespace: {{.Namespace}}
spec:
  acme:
    email: {{.CertmanagerEmail}}
    server: {{.IssuerAPI}}
    privateKeySecretRef:
      name: {{.IssuerType}}
    solvers:
    - http01:
        ingress:
          class: {{.IngressClass}}`

// Ingress in networking.k8s.io/v1 was added in k8s 1.19+
// this includes the pathType change added in 1.18
var registryIngressNetworkingYamlTemplate = `
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: docker-registry
  namespace: {{.Namespace}}
  annotations:
    cert-manager.io/issuer: {{.IssuerType}}
    kubernetes.io/ingress.class: {{.IngressClass}}
{{.NginxMaxBuffer}}
spec:
  rules:
  - host: {{.IngressDomain}}
    http:
      paths:
      - path: /
        pathType: ImplementationSpecific
        backend:
          service
            name: docker-registry
            port:
              number: 5000
  tls:
  - hosts:
    - {{.IngressDomain}}
    secretName: docker-registry
---
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: {{.IssuerType}}
  namespace: {{.Namespace}}
spec:
  acme:
    email: {{.CertmanagerEmail}}
    server: {{.IssuerAPI}}
    privateKeySecretRef:
      name: {{.IssuerType}}
    solvers:
    - http01:
        ingress:
          class: {{.IngressClass}}`
